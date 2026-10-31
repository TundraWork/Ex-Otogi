package telegram

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	telegramstore "github.com/TundraWork/Ex-Otogi/internal/adapter/telegram/store"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// streamAnswer answers typing actions and sends; fail may fail a request
// first.
func streamAnswer(fail func(bin.Encoder) error) func(bin.Encoder, int) (bin.Encoder, error) {
	return func(request bin.Encoder, call int) (bin.Encoder, error) {
		if fail != nil {
			if err := fail(request); err != nil {
				return nil, err
			}
		}
		if _, ok := request.(*tg.MessagesSetTypingRequest); ok {
			return &tg.BoolTrue{}, nil
		}
		return sentOK(request, call)
	}
}

func openTestStream(t *testing.T, x *effects, conversation string) *stream {
	t.Helper()
	s, err := x.openStream(context.Background(), otogi.Effect{Kind: otogi.EffectStream, Key: "k", To: otogi.Destination{Platform: Platform, Account: "main", Conversation: otogi.ConversationID(conversation)}})
	if err != nil {
		t.Fatal(err)
	}
	s.pace = 0
	return s
}

// short abbreviates long text as its start and length.
func short(text string) string {
	if len(text) <= 20 {
		return text
	}
	return fmt.Sprintf("%s…(%d)", text[:10], len(text))
}

// calls describes the RPCs made so far, one line each.
func (r *rpcLog) calls() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var lines []string
	for _, request := range r.requests {
		switch q := request.(type) {
		case *tg.MessagesSetTypingRequest:
			switch a := q.Action.(type) {
			case *tg.SendMessageTypingAction:
				lines = append(lines, "typing")
			case *tg.SendMessageCancelAction:
				lines = append(lines, "cancel")
			case *tg.SendMessageTextDraftAction:
				lines = append(lines, fmt.Sprintf("draft %d %s", a.RandomID%1000, short(a.Text.Text)))
			}
		case *tg.MessagesSendMessageRequest:
			lines = append(lines, fmt.Sprintf("send %d %s", len(q.Entities), short(q.Message)))
		case *tg.MessagesSendMediaRequest:
			lines = append(lines, "media")
		case *tg.MessagesEditMessageRequest:
			lines = append(lines, fmt.Sprintf("edit %d %s", q.ID, short(q.Message)))
		case *tg.MessagesDeleteMessagesRequest:
			lines = append(lines, fmt.Sprintf("delete %v", q.ID))
		}
	}
	return strings.Join(lines, "\n")
}

func update(t *testing.T, s *stream, text string) bool {
	t.Helper()
	shown, err := s.Update(context.Background(), otogi.TextMessage(text))
	if err != nil {
		t.Fatal(err)
	}
	return shown
}

func closeStream(t *testing.T, s *stream, msg otogi.Message) []otogi.EventRef {
	t.Helper()
	refs, err := s.Close(context.Background(), msg)
	if err != nil {
		t.Fatal(err)
	}
	return refs
}

func wantCalls(t *testing.T, log *rpcLog, want ...string) {
	t.Helper()
	if got := log.calls(); got != strings.Join(want, "\n") {
		t.Fatalf("calls:\n%s\nwant:\n%s", got, strings.Join(want, "\n"))
	}
}

func TestStreamEditsOutsidePrivateChats(t *testing.T) {
	x, log, _ := newEffects(t, streamAnswer(nil))
	s := openTestStream(t, x, "chat:5")
	update(t, s, "Hel")
	update(t, s, "Hello")
	if refs := closeStream(t, s, otogi.TextMessage("Hello!")); len(refs) != 1 {
		t.Fatalf("refs %v", refs)
	}
	wantCalls(t, log, "typing", "send 0 Hel", "edit 77 Hello", "edit 77 Hello!", "cancel")
}

func TestStreamDraftsInPrivateChats(t *testing.T) {
	x, log, _ := newEffects(t, streamAnswer(nil))
	s := openTestStream(t, x, "user:7")
	update(t, s, "Hel")
	update(t, s, "Hello")
	closeStream(t, s, otogi.TextMessage("Hello!"))
	draft := RandomID("k#draft0") % 1000
	wantCalls(t, log, "typing", fmt.Sprintf("draft %d Hel", draft), fmt.Sprintf("draft %d Hello", draft), "send 0 Hello!", "cancel")
}

func TestStreamFallsBackToEditsWhenDraftsAreRejected(t *testing.T) {
	x, log, _ := newEffects(t, streamAnswer(func(request bin.Encoder) error {
		if q, ok := request.(*tg.MessagesSetTypingRequest); ok {
			if _, draft := q.Action.(*tg.SendMessageTextDraftAction); draft {
				return tgerr.New(400, "TEXTDRAFT_PEER_INVALID")
			}
		}
		return nil
	}))
	s := openTestStream(t, x, "user:7")
	update(t, s, "Hel")
	update(t, s, "Hello")
	closeStream(t, s, otogi.TextMessage("Hello"))
	wantCalls(t, log, "typing", fmt.Sprintf("draft %d Hel", RandomID("k#draft0")%1000), "send 0 Hel", "edit 77 Hello", "cancel")
}

func TestStreamPacesUpdates(t *testing.T) {
	x, log, _ := newEffects(t, streamAnswer(nil))
	s := openTestStream(t, x, "chat:5")
	s.pace = time.Hour
	if !update(t, s, "a") || update(t, s, "ab") {
		t.Fatal("want the first update shown and the second paced")
	}
	s.mu.Lock()
	s.next = time.Time{} // the pace has passed
	s.mu.Unlock()
	closeStream(t, s, otogi.TextMessage("abc"))
	wantCalls(t, log, "typing", "send 0 a", "edit 77 abc", "cancel")
}

func TestStreamPausesOnFloodWait(t *testing.T) {
	flooded := false
	x, log, _ := newEffects(t, streamAnswer(func(request bin.Encoder) error {
		if _, ok := request.(*tg.MessagesEditMessageRequest); ok && !flooded {
			flooded = true
			return tgerr.New(420, "FLOOD_WAIT_1")
		}
		return nil
	}))
	s := openTestStream(t, x, "chat:5")
	update(t, s, "a")
	if update(t, s, "ab") || update(t, s, "abc") {
		t.Fatal("updates during a FLOOD_WAIT were shown")
	}
	started := time.Now()
	closeStream(t, s, otogi.TextMessage("abcd"))
	if waited := time.Since(started); waited < 500*time.Millisecond {
		t.Fatalf("Close waited %v, want the rest of the FLOOD_WAIT", waited)
	}
	wantCalls(t, log, "typing", "send 0 a", "edit 77 ab", "edit 77 abcd", "cancel")
}

func TestStreamContinuesPastTheMessageLimit(t *testing.T) {
	line := strings.Repeat("a", 99) + "\n"
	text := strings.Repeat(line, 50) // 5000 characters
	rich := &otogi.RichText{}
	rich.Mark(otogi.AnnotationBold, "", func() { rich.Write(text) })
	for _, conversation := range []string{"chat:5", "user:7"} {
		t.Run(conversation, func(t *testing.T) {
			x, log, _ := newEffects(t, streamAnswer(nil))
			s := openTestStream(t, x, conversation)
			if _, err := s.Update(context.Background(), rich.Message()); err != nil {
				t.Fatal(err)
			}
			refs := closeStream(t, s, rich.Message())
			first := strings.TrimSuffix(strings.Repeat(line, 40), "\n")
			second := strings.Repeat(line, 10) // the text's own last line end stays
			want := []string{"typing", "send 1 " + short(first), "send 1 " + short(second), "cancel"}
			if conversation == "user:7" {
				want = []string{"typing", "send 1 " + short(first), fmt.Sprintf("draft %d %s", RandomID("k#draft1")%1000, short(second)), "send 1 " + short(second), "cancel"}
			}
			wantCalls(t, log, want...)
			if len(refs) != 2 {
				t.Fatalf("refs %v", refs)
			}
		})
	}
}

func TestStreamPlacesMediaInOrder(t *testing.T) {
	x, log, _ := newEffects(t, streamAnswer(nil))
	s := openTestStream(t, x, "chat:5")
	image := &otogi.Media{Role: otogi.MediaImage, MediaType: "image/png", Source: otogi.MediaSource{Kind: otogi.MediaSourceInline, Inline: []byte("png")}}
	msg := otogi.Message{Roots: []otogi.PartID{"a", "b", "c"}, Parts: []otogi.Part{
		{ID: "a", Kind: otogi.PartText, Text: "look"},
		{ID: "b", Kind: otogi.PartMedia, Media: image},
		{ID: "c", Kind: otogi.PartText, Text: "done"},
	}}
	if refs := closeStream(t, s, msg); len(refs) != 3 {
		t.Fatalf("refs %v", refs)
	}
	wantCalls(t, log, "typing", "send 0 look", "media", "send 0 done", "cancel")
}

func TestStreamReplayEditsTheMessagesItSent(t *testing.T) {
	x, log, store := newEffects(t, streamAnswer(nil))
	if err := store.PutSent(context.Background(), telegramstore.Sent{Key: "k#0", Conversation: "chat:5", MessageID: 50}, time.Now()); err != nil {
		t.Fatal(err)
	}
	s := openTestStream(t, x, "chat:5")
	refs := closeStream(t, s, otogi.TextMessage("regenerated"))
	wantCalls(t, log, "typing", "edit 50 regenerated", "cancel")
	if _, id, _ := messageTarget(refs[0]); id != 50 {
		t.Fatalf("refs %v", refs)
	}
}

func TestStreamClosedEmptyCancelsTyping(t *testing.T) {
	x, log, _ := newEffects(t, streamAnswer(nil))
	s := openTestStream(t, x, "chat:5")
	if refs := closeStream(t, s, otogi.Message{}); len(refs) != 0 {
		t.Fatalf("refs %v", refs)
	}
	wantCalls(t, log, "typing", "cancel")
}

func TestLayoutKeepsFormattingAcrossTheCut(t *testing.T) {
	t.Parallel()
	rich := &otogi.RichText{}
	rich.Mark(otogi.AnnotationItalic, "", func() { rich.Write(strings.Repeat("é", 5000)) })
	segments, err := layout(rich.Message())
	if err != nil {
		t.Fatal(err)
	}
	if len(segments) != 2 || utf16Len(segments[0].text.String()) != messageLimit {
		t.Fatalf("segments %d, first %d units", len(segments), utf16Len(segments[0].text.String()))
	}
	for _, seg := range segments {
		want := []otogi.Annotation{{Offset: 0, Length: seg.text.Len(), Kind: otogi.AnnotationItalic}}
		if !reflect.DeepEqual(seg.text.Annotations(), want) {
			t.Fatalf("annotations %+v", seg.text.Annotations())
		}
	}
}

// replyTo is a reply relation to message 9 of chat:5.
func replyTo(t *testing.T, x *effects) []otogi.Relation {
	t.Helper()
	return []otogi.Relation{{Kind: otogi.RelationReply, Target: x.ref("chat:5", 9)}}
}

func TestStreamRepliesOnlyWhenSending(t *testing.T) {
	x, log, _ := newEffects(t, streamAnswer(nil))
	s := openTestStream(t, x, "chat:5")
	for _, text := range []string{"Hel", "Hello"} {
		msg := otogi.TextMessage(text)
		msg.Relations = replyTo(t, x)
		if _, err := s.Update(context.Background(), msg); err != nil {
			t.Fatal(err)
		}
	}
	final := otogi.TextMessage("Hello!")
	final.Relations = replyTo(t, x)
	closeStream(t, s, final)
	wantCalls(t, log, "typing", "send 0 Hel", "edit 77 Hello", "edit 77 Hello!", "cancel")
	sent := log.find(t, func(r bin.Encoder) bool { _, ok := r.(*tg.MessagesSendMessageRequest); return ok }).(*tg.MessagesSendMessageRequest)
	if reply, ok := sent.ReplyTo.(*tg.InputReplyToMessage); !ok || reply.ReplyToMsgID != 9 {
		t.Fatalf("the first message does not reply: %+v", sent.ReplyTo)
	}
}

func TestStreamStopsTryingDraftsForUserAccounts(t *testing.T) {
	x, log, _ := newEffects(t, streamAnswer(func(request bin.Encoder) error {
		if q, ok := request.(*tg.MessagesSetTypingRequest); ok {
			if _, draft := q.Action.(*tg.SendMessageTextDraftAction); draft {
				return tgerr.New(400, "USER_BOT_REQUIRED")
			}
		}
		return nil
	}))
	first := openTestStream(t, x, "user:7")
	update(t, first, "Hi")
	closeStream(t, first, otogi.TextMessage("Hi"))
	second := openTestStream(t, x, "user:8")
	update(t, second, "Yo")
	closeStream(t, second, otogi.TextMessage("Yo"))
	drafts := strings.Count(log.calls(), "draft ")
	if drafts != 1 {
		t.Fatalf("%d draft attempts, want 1:\n%s", drafts, log.calls())
	}
}

func TestStreamKeepsTypingWhileEditing(t *testing.T) {
	x, log, _ := newEffects(t, streamAnswer(nil))
	x.streamRenew = 10 * time.Millisecond
	s := openTestStream(t, x, "chat:5")
	update(t, s, "partial")
	eventually(t, "typing renewed after content", func() bool { return strings.Count(log.calls(), "typing") >= 2 })
	closeStream(t, s, otogi.TextMessage("done"))
}
