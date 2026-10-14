// Package echo provides a module that replies to each text message with the
// same text prefixed by "echo: ". It is the reference example of a module.
package echo

import (
	"context"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// Module echoes text messages.
type Module struct{}

// New returns an echo module.
func New() *Module { return &Module{} }

// Descriptor subscribes to new messages.
func (*Module) Descriptor() otogi.ModuleDescriptor {
	return otogi.ModuleDescriptor{ID: "echo", Subscribes: []otogi.EventAction{otogi.ActionMessageCreated}}
}

// Handle replies with the message's text; messages without text are ignored.
func (*Module) Handle(ctx context.Context, event otogi.Event, inv otogi.Invocation) error {
	text := event.Message.PlainText()
	if text == "" {
		return nil
	}
	return inv.Send(ctx, "echo", otogi.TextMessage("echo: "+text))
}
