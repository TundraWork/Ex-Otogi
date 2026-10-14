package notes

import (
	"context"
	"database/sql"
	"log/slog"
	"strings"
	"testing"

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
