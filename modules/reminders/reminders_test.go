package reminders

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/llm"
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

// provideHost keeps what a module provides.
type provideHost struct {
	*timerHost
	provided map[string]any
}

func (h provideHost) Provide(name string, service any) error {
	h.provided[name] = service
	return nil
}

func TestSetReminderTool(t *testing.T) {
	m := New(Config{MaxDelay: time.Hour})
	h := provideHost{timerHost: &timerHost{timers: map[string]otogi.Timer{}}, provided: map[string]any{}}
	if err := m.Start(context.Background(), h); err != nil {
		t.Fatal(err)
	}
	tools := h.provided[llm.ToolsService("reminders")].([]llm.Tool)
	if len(tools) != 1 || tools[0].Function().Name != "set_reminder" {
		t.Fatalf("tools %+v", tools)
	}
	e := otogi.Event{ID: "e1", Platform: "p", Account: "a", Conversation: otogi.Conversation{ID: "chat"}, SourceID: "m1"}
	call := func(args string) error {
		_, err := tools[0].Call(context.Background(), e, json.RawMessage(args))
		return err
	}
	for args, want := range map[string]string{`{"delay":"soon","text":"tea"}`: "positive duration", `{"delay":"2h","text":"tea"}`: "limit", `{"delay":"10m","text":" "}`: "empty"} {
		if err := call(args); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err %v, want %q", args, err, want)
		}
	}
	if err := call(`{"delay":"10m","text":"tea"}`); err != nil {
		t.Fatal(err)
	}
	if err := call(`{"delay":"10m","text":"tea"}`); err != nil { // a replay sets the same timer
		t.Fatal(err)
	}
	if len(h.timers) != 1 {
		t.Fatalf("%d timers, want 1", len(h.timers))
	}
	for _, timer := range h.timers {
		var r reminder
		if err := json.Unmarshal(timer.Payload, &r); err != nil || r.Text != "tea" || r.Ref.SourceEvent != "m1" || r.To.Conversation != "chat" {
			t.Fatalf("reminder %+v, %v", r, err)
		}
	}
}
