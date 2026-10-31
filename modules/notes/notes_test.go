package notes

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

type memoryStore map[string][]byte

func (s memoryStore) Get(_ context.Context, k string) ([]byte, bool, error) {
	v, ok := s[k]
	return v, ok, nil
}
func (s memoryStore) Put(_ context.Context, k string, v []byte) error { s[k] = v; return nil }
func (s memoryStore) Delete(_ context.Context, k string) error        { delete(s, k); return nil }
func (s memoryStore) List(context.Context, string) ([]string, error)  { return nil, nil }

// host implements the Host methods notes uses; the embedded interface stands
// in for the rest.
type host struct {
	otogi.Host
	store memoryStore
}

func (h host) Module() otogi.ModuleID                    { return "notes" }
func (h host) Logger() *slog.Logger                      { return slog.Default() }
func (h host) Record(context.Context, otogi.Record)      {}
func (h host) Store() otogi.Store                        { return h.store }
func (h host) Database(context.Context) (*sql.DB, error) { return nil, nil }

type invocation struct {
	otogi.Invocation
	host    host
	replies []string
}

func (i *invocation) Host() otogi.Host { return i.host }
func (i *invocation) Send(_ context.Context, _ string, m otogi.Message) (otogi.EventRef, error) {
	i.replies = append(i.replies, m.PlainText())
	return otogi.EventRef{}, nil
}

func TestNotes(t *testing.T) {
	m, h := New(Config{MaxNotes: 2}), host{store: memoryStore{}}
	run := func(conversation, args string) string {
		inv := &invocation{host: h}
		e := otogi.Event{Platform: "p", Account: "a", Conversation: otogi.Conversation{ID: otogi.ConversationID(conversation)}, Command: &otogi.Command{Name: "note", Args: args}}
		if err := m.Handle(context.Background(), e, inv); err != nil {
			t.Fatal(err)
		}
		return inv.replies[0]
	}
	steps := []struct{ conversation, args, want string }{
		{"c1", "", "No notes yet"},
		{"c1", "buy tea", "Noted (1)"},
		{"c1", "call mum", "Noted (2)"},
		{"c1", "third", "already has 2 notes"},
		{"c1", "", "1. buy tea\n2. call mum"},
		{"c2", "", "No notes yet"}, // notes are per conversation
		{"c1", "clear", "cleared"},
		{"c1", "", "No notes yet"},
	}
	for _, s := range steps {
		if got := run(s.conversation, s.args); !strings.Contains(got, s.want) {
			t.Fatalf("/note %q in %s = %q, want %q", s.args, s.conversation, got, s.want)
		}
	}
}

// toolHost is a host that keeps what the module provides.
type toolHost struct {
	host
	provided map[string]any
}

func (h toolHost) Provide(name string, service any) error {
	h.provided[name] = service
	return nil
}

func TestNoteTools(t *testing.T) {
	m := New(Config{MaxNotes: 2})
	h := toolHost{host: host{store: memoryStore{}}, provided: map[string]any{}}
	if err := m.Start(context.Background(), h); err != nil {
		t.Fatal(err)
	}
	tools := map[string]llm.Tool{}
	for _, tool := range h.provided[llm.ToolsService("notes")].([]llm.Tool) {
		tools[tool.Function().Name] = tool
	}
	e := otogi.Event{Platform: "p", Account: "a", Conversation: otogi.Conversation{ID: "chat"}}
	call := func(name, args string) (any, error) {
		return tools[name].Call(context.Background(), e, json.RawMessage(args))
	}
	for _, text := range []string{"milk", "eggs"} {
		if _, err := call("add_note", `{"text":"`+text+`"}`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := call("add_note", `{"text":"bread"}`); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("over the limit: %v", err)
	}
	got, err := call("list_notes", `{}`)
	if err != nil || strings.Join(got.(map[string][]string)["notes"], ",") != "milk,eggs" {
		t.Fatalf("list %v, %v", got, err)
	}
	// /note sees what the tool added.
	inv := &invocation{host: h.host}
	if err := m.Handle(context.Background(), otogi.Event{Platform: "p", Account: "a", Conversation: otogi.Conversation{ID: "chat"}, Command: &otogi.Command{Name: "note"}}, inv); err != nil {
		t.Fatal(err)
	}
	if len(inv.replies) != 1 || inv.replies[0] != "1. milk\n2. eggs" {
		t.Fatalf("/note replied %q", inv.replies)
	}
}
