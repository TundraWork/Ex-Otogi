package reminders

import (
	"context"
	"database/sql"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

type timerHost struct {
	otogi.Host
	timers map[string]otogi.Timer
}

func (h *timerHost) SetTimer(_ context.Context, key string, at time.Time, payload []byte) error {
	h.timers[key] = otogi.Timer{Key: key, At: at, Payload: payload}
	return nil
}
func (h *timerHost) Logger() *slog.Logger                      { return slog.Default() }
func (h *timerHost) Database(context.Context) (*sql.DB, error) { return nil, nil }

type invocation struct {
	otogi.Invocation
	host *timerHost
	sent []struct {
		to  otogi.ConversationID
		msg otogi.Message
	}
}

func (i *invocation) Host() otogi.Host { return i.host }
func (i *invocation) Send(ctx context.Context, slot string, m otogi.Message) (otogi.EventRef, error) {
	return i.SendTo(ctx, slot, otogi.Destination{Conversation: "here"}, m)
}
func (i *invocation) SendTo(_ context.Context, _ string, to otogi.Destination, m otogi.Message) (otogi.EventRef, error) {
	i.sent = append(i.sent, struct {
		to  otogi.ConversationID
		msg otogi.Message
	}{to.Conversation, m})
	return otogi.EventRef{}, nil
}

func TestRemind(t *testing.T) {
	m := New(Config{MaxDelay: time.Hour})
	h := &timerHost{timers: map[string]otogi.Timer{}}
	remind := func(args string) string {
		inv := &invocation{host: h}
		e := otogi.Event{ID: otogi.EventID("e-" + args), Platform: "p", Account: "a", Conversation: otogi.Conversation{ID: "chat"}, SourceID: "m1", Command: &otogi.Command{Name: "remind", Args: args}}
		if err := m.Handle(context.Background(), e, inv); err != nil {
			t.Fatal(err)
		}
		return inv.sent[0].msg.PlainText()
	}
	for args, want := range map[string]string{"": "Usage", "soon tea": "Usage", "10m": "Usage", "2h tea": "too far ahead"} {
		if got := remind(args); !strings.Contains(got, want) {
			t.Errorf("/remind %q = %q, want %q", args, got, want)
		}
	}
	if len(h.timers) != 0 {
		t.Fatalf("invalid commands set timers: %v", h.timers)
	}
	if got := remind("10m buy tea"); !strings.Contains(got, "10m0s") {
		t.Fatalf("ack = %q", got)
	}
	timer := h.timers["e-10m buy tea"]
	if d := time.Until(timer.At); d < 9*time.Minute || d > 10*time.Minute {
		t.Fatalf("timer at %v", timer.At)
	}

	inv := &invocation{host: h}
	if err := m.HandleTimer(context.Background(), timer, inv); err != nil {
		t.Fatal(err)
	}
	got := inv.sent[0]
	if got.to != "chat" || got.msg.PlainText() != "⏰ buy tea" || got.msg.Relations[0].Target.SourceEvent != "m1" {
		t.Fatalf("reminder = %+v", got)
	}
}
