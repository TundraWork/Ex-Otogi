// Package notes provides the /note command: a per-conversation notepad kept
// in the module's store. It is the reference example of module state. It
// also offers agents the tools add_note and list_notes.
//
//	/note <text>   add a note
//	/note          list notes
//	/note clear    delete all notes
package notes

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// Config configures the module.
type Config struct {
	// MaxNotes caps notes per conversation; default 50.
	MaxNotes int `yaml:"max_notes"`
}

// Module handles /note and offers agents its tools.
type Module struct {
	max  int
	host otogi.Host
}

// New returns the module.
func New(cfg Config) *Module {
	if cfg.MaxNotes <= 0 {
		cfg.MaxNotes = 50
	}
	return &Module{max: cfg.MaxNotes}
}

// Descriptor declares the /note command.
func (*Module) Descriptor() otogi.ModuleDescriptor {
	return otogi.ModuleDescriptor{ID: "notes", Commands: []otogi.CommandSpec{
		{Name: "note", Usage: "[text | clear]", Description: "add, list, or clear this chat's notes"},
	},
		Provides: []string{llm.ToolsService("notes")},
	}
}

// Start offers the module's tools to agents: the same notes as /note, of the
// chat the agent is answering in.
func (m *Module) Start(_ context.Context, host otogi.Host) error {
	m.host = host
	return host.Provide(llm.ToolsService("notes"), []llm.Tool{
		llm.NewTool(llm.Function{
			Name:        "add_note",
			Description: "Add a note to this chat's notepad, the one people see with /note.",
			Parameters:  json.RawMessage(`{"type": "object", "properties": {"text": {"type": "string"}}, "required": ["text"]}`),
		}, func(ctx context.Context, e otogi.Event, args json.RawMessage) (any, error) {
			var in struct{ Text string }
			if err := json.Unmarshal(args, &in); err != nil {
				return nil, fmt.Errorf("arguments: %w", err)
			}
			n, err := m.add(ctx, m.host.Store(), noteKey(e), strings.TrimSpace(in.Text))
			if err != nil {
				return nil, err
			}
			return map[string]int{"notes": n}, nil
		}),
		llm.NewTool(llm.Function{
			Name:        "list_notes",
			Description: "List the notes in this chat's notepad, oldest first.",
			Parameters:  json.RawMessage(`{"type": "object", "properties": {}}`),
		}, func(ctx context.Context, e otogi.Event, _ json.RawMessage) (any, error) {
			notes, _, err := otogi.GetJSON[[]string](ctx, m.host.Store(), noteKey(e))
			return map[string][]string{"notes": notes}, err
		}),
	})
}

func noteKey(e otogi.Event) string {
	return fmt.Sprintf("conversation/%s/%s/%s", e.Platform, e.Account, e.Conversation.ID)
}

// add appends a note and returns how many there are.
func (m *Module) add(ctx context.Context, store otogi.Store, key, text string) (int, error) {
	notes, _, err := otogi.GetJSON[[]string](ctx, store, key)
	switch {
	case err != nil:
		return 0, err
	case text == "":
		return 0, fmt.Errorf("the note is empty")
	case len(notes) >= m.max:
		return 0, fmt.Errorf("this chat already has %d notes, the limit; /note clear first", m.max)
	}
	return len(notes) + 1, otogi.PutJSON(ctx, store, key, append(notes, text))
}

// Handle adds, lists, or clears the conversation's notes.
func (m *Module) Handle(ctx context.Context, e otogi.Event, inv otogi.Invocation) error {
	store := inv.Host().Store()
	key := noteKey(e)
	notes, _, err := otogi.GetJSON[[]string](ctx, store, key)
	if err != nil {
		return err
	}
	var reply string
	switch args := e.Command.Args; {
	case args == "":
		if len(notes) == 0 {
			reply = "No notes yet. Add one with /note <text>."
		} else {
			var b strings.Builder
			for i, n := range notes {
				fmt.Fprintf(&b, "%d. %s\n", i+1, n)
			}
			reply = strings.TrimSuffix(b.String(), "\n")
		}
	case args == "clear":
		if err := store.Delete(ctx, key); err != nil {
			return err
		}
		reply = "Notes cleared."
	case len(notes) >= m.max:
		reply = fmt.Sprintf("This chat already has %d notes; /note clear first.", m.max)
	default:
		n, err := m.add(ctx, store, key, args)
		if err != nil {
			return err
		}
		reply = fmt.Sprintf("Noted (%d).", n)
	}
	_, err = inv.Send(ctx, "reply", otogi.Reply(reply, e.Ref()))
	return err
}
