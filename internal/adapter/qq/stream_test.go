package qq

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// clocked gives the effects a clock the test moves.
func clocked(x *effects) *time.Time {
	now := time.Now()
	x.now = func() time.Time { return now }
	return &now
}

func openStream(t *testing.T, x *effects, key, origin, conversation string) otogi.DriverStream {
	t.Helper()
	s, err := x.OpenStream(context.Background(), otogi.Effect{Kind: otogi.EffectStream, Key: otogi.IdempotencyKey(key), Origin: origin, To: otogi.Destination{Platform: Platform, Account: "bot", Conversation: otogi.ConversationID(conversation)}})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func (s *sendServer) chunk(i int) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.streams[i]
}

func (s *sendServer) chunks() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.streams)
}

// A C2C reply streams: chunks with the whole text so far, paced, under one
// msg_id and msg_seq, the first naming no stream and the rest the one QQ
// returned, and the last marked done.
func TestC2CStream(t *testing.T) {
	x, srv, s := newEffects(t)
	ctx := context.Background()
	now := clocked(x)
	_ = s.PutOrigin(ctx, "e1", "m1", "user:U1", *now)
	st := openStream(t, x, "k", "e1", "user:U1")

	if shown, err := st.Update(ctx, otogi.TextMessage("Hel")); !shown || err != nil {
		t.Fatalf("first update: %v, %v", shown, err)
	}
	if shown, _ := st.Update(ctx, otogi.TextMessage("Hello")); shown {
		t.Fatal("an update within the chunk interval was sent")
	}
	*now = now.Add(time.Second)
	if shown, _ := st.Update(ctx, otogi.TextMessage("Hello wor")); !shown {
		t.Fatal("second chunk not sent")
	}
	refs, err := st.Close(ctx, otogi.TextMessage("Hello world"))
	if err != nil || len(refs) != 1 || refs[0].SourceEvent != "st1" {
		t.Fatalf("close = %+v, %v", refs, err)
	}
	if srv.chunks() != 3 || srv.count() != 0 {
		t.Fatalf("%d chunks, %d messages", srv.chunks(), srv.count())
	}
	first, last := srv.chunk(0), srv.chunk(2)
	if first["stream_msg_id"] != nil || first["index"] != 0.0 || first["input_state"] != 1.0 || first["content_raw"] != "Hel" || first["msg_id"] != "m1" || first["msg_seq"] != 1.0 {
		t.Fatalf("first chunk = %v", first)
	}
	if last["stream_msg_id"] != "st1" || last["index"] != 2.0 || last["input_state"] != 10.0 || last["content_raw"] != "Hello world" || last["msg_seq"] != 1.0 {
		t.Fatalf("last chunk = %v", last)
	}
	// A replay sends nothing: the kernel's ordinary send finds the stream.
	if _, err := x.OpenStream(ctx, otogi.Effect{Key: "k", Origin: "e1", To: otogi.Destination{Conversation: "user:U1"}}); !errors.Is(err, otogi.ErrUnsupported) {
		t.Fatalf("reopen = %v", err)
	}
	if ref, err := sendText(x, "k", "e1", "user:U1", otogi.TextMessage("Hello world")); err != nil || ref.SourceEvent != "st1" || srv.count() != 0 {
		t.Fatalf("replayed send = %+v, %v", ref, err)
	}
}

// Groups cannot stream, nor can a send with no message to reply to; the
// kernel then sends the final content, which goes out normally.
func TestWhereStreamsCannotGo(t *testing.T) {
	x, srv, _ := newEffects(t)
	ctx := context.Background()
	for _, c := range []struct{ origin, to string }{{"e1", "group:G1"}, {"timer:x", "user:U1"}} {
		if _, err := x.OpenStream(ctx, otogi.Effect{Key: "k", Origin: c.origin, To: otogi.Destination{Conversation: otogi.ConversationID(c.to)}}); !errors.Is(err, otogi.ErrUnsupported) {
			t.Fatalf("%s: %v", c.to, err)
		}
	}
	if _, err := sendText(x, "k", "timer:x", "user:U1", otogi.TextMessage("tea")); err != nil || srv.count() != 1 {
		t.Fatalf("send after an unsupported stream: %v, %d", err, srv.count())
	}
}

// A chunk QQ refuses ends the streaming, never the reply: Close sends the
// final content as a new message.
func TestRefusedChunkFallsBackToAMessage(t *testing.T) {
	x, srv, s := newEffects(t)
	ctx := context.Background()
	now := clocked(x)
	_ = s.PutOrigin(ctx, "e1", "m1", "user:U1", *now)
	srv.fail = func(path string, body map[string]any) (int, string, bool) {
		if strings.HasSuffix(path, "/stream_messages") && body["index"] == 1.0 {
			return 400, `{"code":40007,"message":"content prefix changed"}`, true
		}
		return 0, "", false
	}
	st := openStream(t, x, "k", "e1", "user:U1")
	_, _ = st.Update(ctx, otogi.TextMessage("Hel"))
	*now = now.Add(time.Second)
	if shown, err := st.Update(ctx, otogi.TextMessage("Hello")); shown || err != nil {
		t.Fatalf("refused update = %v, %v", shown, err)
	}
	refs, err := st.Close(ctx, otogi.TextMessage("Hello world"))
	if err != nil || len(refs) != 1 || srv.count() != 1 || srv.post(0)["content"] != "Hello world" {
		t.Fatalf("close = %+v, %v, %d messages", refs, err, srv.count())
	}
}

// A reply longer than one message finishes the stream with its first part
// and sends the rest.
func TestLongStreamedReply(t *testing.T) {
	x, srv, s := newEffects(t)
	ctx := context.Background()
	now := clocked(x)
	_ = s.PutOrigin(ctx, "e1", "m1", "user:U1", *now)
	st := openStream(t, x, "k", "e1", "user:U1")
	long := strings.Repeat("长", maxText+10)
	_, _ = st.Update(ctx, otogi.TextMessage(long[:30]))
	refs, err := st.Close(ctx, otogi.TextMessage(long))
	if err != nil || len(refs) != 2 || srv.count() != 1 {
		t.Fatalf("close = %+v, %v, %d messages", refs, err, srv.count())
	}
	if done := srv.chunk(srv.chunks() - 1)["content_raw"].(string); done+srv.post(0)["content"].(string) != long {
		t.Fatal("the stream and the message do not add up to the reply")
	}
}
