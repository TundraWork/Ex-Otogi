package qq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	qqstore "github.com/TundraWork/Ex-Otogi/internal/adapter/qq/store"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// sendServer answers message and file posts, numbering the messages, and
// lets a test script failures by request.
type sendServer struct {
	mu     sync.Mutex
	posts  []map[string]any // bodies of message posts, in order
	paths  []string
	fail   func(path string, body map[string]any) (status int, response string, ok bool)
	nextID int
}

func newEffects(t *testing.T) (*effects, *sendServer, *qqstore.Store) {
	t.Helper()
	f := newFakeQQ(t)
	srv := &sendServer{}
	f.handle = func(w http.ResponseWriter, r *http.Request, body string) bool {
		if !strings.HasPrefix(r.URL.Path, "/v2/") {
			return false
		}
		var in map[string]any
		_ = json.Unmarshal([]byte(body), &in)
		srv.mu.Lock()
		defer srv.mu.Unlock()
		srv.paths = append(srv.paths, r.Method+" "+r.URL.Path)
		if srv.fail != nil {
			if status, resp, ok := srv.fail(r.URL.Path, in); ok {
				w.WriteHeader(status)
				_, _ = w.Write([]byte(resp))
				return true
			}
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/files"):
			_, _ = w.Write([]byte(`{"file_uuid":"u","file_info":"INFO","ttl":3600}`))
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusOK)
		default:
			srv.posts = append(srv.posts, in)
			srv.nextID++
			_, _ = fmt.Fprintf(w, `{"id":"s%d","timestamp":"2026-11-06T10:00:00+08:00","ext_info":{"ref_idx":"REFIDX_s%d"}}`, srv.nextID, srv.nextID)
		}
		return true
	}
	s, err := qqstore.Open(context.Background(), filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	x := &effects{
		api:   &api{base: f.srv.URL, appID: "1001", secret: "secret", http: http.DefaultClient, now: time.Now},
		store: s, account: "bot", wake: make(chan struct{}, 1), log: quiet, now: time.Now,
		self: func() (string, string) { return "B1", "Otogi" }, retries: 2, backoff: time.Millisecond,
	}
	return x, srv, s
}

func sendText(x *effects, key, origin, conversation string, msg otogi.Message) (otogi.EventRef, error) {
	return x.Apply(context.Background(), otogi.Effect{Kind: otogi.EffectSend, Key: otogi.IdempotencyKey(key), Origin: origin, To: otogi.Destination{Platform: Platform, Account: "bot", Conversation: otogi.ConversationID(conversation)}, Message: msg})
}

func (s *sendServer) post(i int) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.posts[i]
}

func (s *sendServer) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.posts)
}

// A reply to a message is passive, numbered per message, and sent once per
// key; past the window or the replies QQ allows, sends are proactive.
func TestPassiveRepliesAndReplays(t *testing.T) {
	x, srv, s := newEffects(t)
	ctx := context.Background()
	if err := s.PutOrigin(ctx, "e1", "m1", "user:U1", time.Now()); err != nil {
		t.Fatal(err)
	}
	ref, err := sendText(x, "k1", "e1", "user:U1", otogi.TextMessage("hi *there*"))
	if err != nil || ref.SourceEvent != "s1" || ref.Conversation != "user:U1" {
		t.Fatalf("send = %+v, %v", ref, err)
	}
	if p := srv.post(0); p["msg_id"] != "m1" || p["msg_seq"] != 1.0 || p["msg_type"] != 0.0 || p["content"] != "hi *there*" {
		t.Fatalf("post = %v", p)
	}
	// A replay sends nothing and returns the same message.
	if ref, err := sendText(x, "k1", "e1", "user:U1", otogi.TextMessage("hi *there*")); err != nil || ref.SourceEvent != "s1" || srv.count() != 1 {
		t.Fatalf("replay = %+v, %v, %d posts", ref, err, srv.count())
	}
	// The C2C message is queued as the bot's own event.
	if rows, _ := s.PendingInbox(ctx, 0, 10); len(rows) != 1 || !strings.Contains(string(rows[0].Payload), sentType) {
		t.Fatalf("inbox = %v", rows)
	}
	for i := 2; i <= 5; i++ {
		if _, err := sendText(x, fmt.Sprintf("k%d", i), "e1", "user:U1", otogi.TextMessage("more")); err != nil {
			t.Fatal(err)
		}
	}
	// QQ allows 4 passive replies in C2C; the fifth is proactive.
	if p := srv.post(3); p["msg_seq"] != 4.0 {
		t.Fatalf("fourth = %v", p)
	}
	if p := srv.post(4); p["msg_id"] != nil {
		t.Fatalf("fifth = %v", p)
	}
	// A timer's send has no message to reply to.
	if _, err := sendText(x, "timer", "timer:reminders:x", "group:G1", otogi.TextMessage("tea")); err != nil || srv.post(5)["msg_id"] != nil {
		t.Fatalf("proactive = %v, %v", srv.post(5), err)
	}
	if srv.paths[5] != "POST /v2/groups/G1/messages" {
		t.Fatalf("path = %s", srv.paths[5])
	}
}

// QQ's answers that matter: a passive reply refused goes out proactively, a
// repeated msg_seq means it was delivered, a refusal is unavailable and can
// be tried again, and a transient failure is retried.
func TestSendFailures(t *testing.T) {
	x, srv, s := newEffects(t)
	ctx := context.Background()
	_ = s.PutOrigin(ctx, "e1", "m1", "group:G1", time.Now())
	var calls int
	srv.fail = func(_ string, body map[string]any) (int, string, bool) {
		calls++
		switch {
		case body["content"] == "late" && body["msg_id"] != nil:
			return 400, `{"code":40034024,"message":"msg_id expired"}`, true
		case body["content"] == "again":
			return 400, `{"code":40054005,"message":"duplicate msg_seq"}`, true
		case body["content"] == "no" && calls < 10:
			return 400, `{"code":40034102,"message":"no proactive permission","trace_id":"T1"}`, true
		case body["content"] == "busy" && calls%2 == 1:
			return 500, `{"code":50055001,"message":"busy"}`, true
		}
		return 0, "", false
	}
	if _, err := sendText(x, "late", "e1", "group:G1", otogi.TextMessage("late")); err != nil {
		t.Fatal(err)
	}
	if p := srv.post(0); p["msg_id"] != nil {
		t.Fatalf("fallback = %v", p)
	}
	if ref, err := sendText(x, "again", "e1", "group:G1", otogi.TextMessage("again")); err != nil || ref.SourceEvent != "" {
		t.Fatalf("duplicate = %+v, %v", ref, err)
	}
	_, err := sendText(x, "no", "", "group:G1", otogi.TextMessage("no"))
	if !errors.Is(err, otogi.ErrUnavailable) || !strings.Contains(err.Error(), "code 40034102: no proactive permission (trace T1)") {
		t.Fatalf("refusal = %v", err)
	}
	calls = 10 // QQ accepts it now; the refused send was released
	if _, err := sendText(x, "no", "", "group:G1", otogi.TextMessage("no")); err != nil {
		t.Fatalf("after refusal: %v", err)
	}
	calls = 0
	if _, err := sendText(x, "busy", "", "group:G1", otogi.TextMessage("busy")); err != nil || calls != 2 {
		t.Fatalf("transient = %v after %d calls", err, calls)
	}
}

// A proactive send that QQ never answered is not sent again: QQ could not
// tell the repeat from a new message.
func TestInterruptedProactiveSendIsNotRepeated(t *testing.T) {
	x, srv, _ := newEffects(t)
	srv.fail = func(string, map[string]any) (int, string, bool) { return 0, "", false }
	x.api.http = &http.Client{Transport: failing{}}
	if _, err := sendText(x, "k", "", "group:G1", otogi.TextMessage("hi")); err == nil {
		t.Fatal("no error")
	}
	x.api.http = http.DefaultClient
	_, err := sendText(x, "k", "", "group:G1", otogi.TextMessage("hi"))
	if !errors.Is(err, otogi.ErrUnavailable) || !strings.Contains(err.Error(), "may have been delivered") || srv.count() != 0 {
		t.Fatalf("replay = %v, %d posts", err, srv.count())
	}
}

type failing struct{}

func (failing) RoundTrip(r *http.Request) (*http.Response, error) {
	if strings.HasSuffix(r.URL.Path, "/messages") {
		return nil, errors.New("connection reset")
	}
	return http.DefaultTransport.RoundTrip(r)
}

// Formatting becomes Markdown, mentions <@openid>, long text more messages,
// a reply a quote, and media an upload then a media message.
func TestEncoding(t *testing.T) {
	x, srv, s := newEffects(t)
	ctx := context.Background()
	_ = s.PutRef(ctx, "REFIDX_m1", "m1", "group:G1", time.Now())

	var text otogi.RichText
	text.Mark(otogi.AnnotationBold, "", func() { text.Write("hi") })
	text.Write(" ")
	text.Mark(otogi.AnnotationMention, "user:M2", func() { text.Write("@Bob") })
	msg := text.Message()
	msg.Relations = []otogi.Relation{{Kind: otogi.RelationReply, Target: otogi.EventRef{SourceEvent: "m1"}}}
	if _, err := sendText(x, "fmt", "", "group:G1", msg); err != nil {
		t.Fatal(err)
	}
	p := srv.post(0)
	if p["msg_type"] != 2.0 || p["markdown"].(map[string]any)["content"] != "**hi** <@M2>" || p["message_reference"].(map[string]any)["message_id"] != "REFIDX_m1" {
		t.Fatalf("markdown = %v", p)
	}

	long := strings.Repeat("长", maxText+10)
	if _, err := sendText(x, "long", "", "group:G1", otogi.TextMessage(long)); err != nil || srv.count() != 3 {
		t.Fatalf("long: %v, %d posts", err, srv.count())
	}
	if a, b := srv.post(1)["content"].(string), srv.post(2)["content"].(string); a+b != long {
		t.Fatal("split text does not add up")
	}

	img := otogi.Message{Roots: []otogi.PartID{"t", "i"}, Parts: []otogi.Part{
		{ID: "t", Kind: otogi.PartText, Text: "a cat"},
		{ID: "i", Kind: otogi.PartMedia, Media: &otogi.Media{Role: otogi.MediaImage, MediaType: "image/png", Source: otogi.MediaSource{Kind: otogi.MediaSourceInline, Inline: []byte("png")}}},
	}}
	if _, err := sendText(x, "img", "", "user:U1", img); err != nil {
		t.Fatal(err)
	}
	if p := srv.post(4); p["msg_type"] != 7.0 || p["media"].(map[string]any)["file_info"] != "INFO" {
		t.Fatalf("media = %v", p)
	}
	if !strings.Contains(strings.Join(srv.paths, "\n"), "POST /v2/users/U1/files") {
		t.Fatalf("paths = %v", srv.paths)
	}

	if err := x.recall(ctx, otogi.EventRef{Conversation: "group:G1", SourceEvent: "s1"}); err != nil || srv.paths[len(srv.paths)-1] != "DELETE /v2/groups/G1/messages/s1" {
		t.Fatalf("recall: %v, %v", err, srv.paths)
	}
}
