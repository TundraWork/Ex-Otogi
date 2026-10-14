// Package notes provides the /note command: a per-conversation notepad kept
// in the module's store. It is the reference example of module state.
//
//	/note <text>   add a note
//	/note          list notes
//	/note clear    delete all notes
package notes

import (
	"context"
	"fmt"
	"strings"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// Config configures the module.
type Config struct {
	// MaxNotes caps notes per conversation; default 50.
	MaxNotes int `yaml:"max_notes"`
}

// Module handles /note.
type Module struct{ max int }

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
	}}
}

// Handle adds, lists, or clears the conversation's notes.
func (m *Module) Handle(ctx context.Context, e otogi.Event, inv otogi.Invocation) error {
	store := inv.Host().Store()
	key := fmt.Sprintf("conversation/%s/%s/%s", e.Platform, e.Account, e.Conversation.ID)
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
		if err := otogi.PutJSON(ctx, store, key, append(notes, args)); err != nil {
			return err
		}
		reply = fmt.Sprintf("Noted (%d).", len(notes)+1)
	}
	_, err = inv.Send(ctx, "reply", otogi.Reply(reply, e.Ref()))
	return err
}
