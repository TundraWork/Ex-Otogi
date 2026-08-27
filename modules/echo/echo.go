// Package echo provides a module that echoes text messages.
package echo

import (
	"context"
	"errors"
	"fmt"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

const prefix = "echo: "

// Module sends each supported input prefixed with "echo: ".
type Module struct {
	descriptor otogi.ModuleDescriptor
}

// New returns an echo module consuming text.message events.
func New() *Module {
	id, err := otogi.NewModuleID("echo")
	if err != nil {
		panic(err)
	}
	kind, err := otogi.NewEventKind("text.message")
	if err != nil {
		panic(err)
	}
	descriptor, err := otogi.NewModuleDescriptor(id, []otogi.EventKind{kind})
	if err != nil {
		panic(err)
	}
	return &Module{descriptor: descriptor}
}

// Descriptor returns the echo module identity and text.message routing declaration.
func (m *Module) Descriptor() otogi.ModuleDescriptor {
	return m.descriptor
}

// Handle sends a bounded echo and maps cancellation and send failures to terminal results.
func (m *Module) Handle(ctx context.Context, event otogi.Event, invocation otogi.Invocation) otogi.Result {
	if err := ctx.Err(); err != nil {
		return otogi.NewCanceledResult(err)
	}
	if len(event.Text().String()) > 4090 {
		return otogi.NewFailedResult(fmt.Errorf("echo text: %w", otogi.ErrInvalid))
	}
	text, err := otogi.NewText(prefix + event.Text().String())
	if err != nil {
		return otogi.NewFailedResult(fmt.Errorf("echo text: %w", err))
	}
	if err := invocation.SendText(ctx, "echo", text); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return otogi.NewCanceledResult(err)
		}
		return otogi.NewFailedResult(err)
	}
	return otogi.NewSucceededResult()
}
