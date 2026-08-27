// Package echo provides a module that echoes the first text part of rich messages.
package echo

import (
	"context"
	"errors"
	"fmt"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

const prefix = "echo: "

// Module sends each supported input prefixed with "echo: ".
type Module struct{ descriptor otogi.ModuleDescriptor }

// New returns an echo module consuming message-created events.
func New() *Module {
	id, err := otogi.NewModuleID("echo")
	if err != nil {
		panic(err)
	}
	descriptor, err := otogi.NewModuleDescriptor(id, []otogi.EventAction{otogi.ActionMessageCreated})
	if err != nil {
		panic(err)
	}
	return &Module{descriptor: descriptor}
}

// Descriptor returns the echo module identity and routing declaration.
func (m *Module) Descriptor() otogi.ModuleDescriptor { return m.descriptor }

// Handle echoes the first text part and rejects messages without text.
func (m *Module) Handle(ctx context.Context, event otogi.Event, invocation otogi.Invocation) otogi.Result {
	if err := ctx.Err(); err != nil {
		return otogi.NewCanceledResult(err)
	}
	var input string
	for _, part := range event.Message().Parts() {
		if text, _, ok := part.Text(); ok {
			input = text
			break
		}
	}
	if input == "" || len(input) > 64*1024-len(prefix) {
		return otogi.NewRejectedResult()
	}
	id, err := otogi.NewPartID("echo-text")
	if err != nil {
		return otogi.NewFailedResult(err)
	}
	part, err := otogi.NewTextPart(id, prefix+input, nil, nil, nil)
	if err != nil {
		return otogi.NewFailedResult(fmt.Errorf("echo text: %w", err))
	}
	message, err := otogi.NewMessage([]otogi.PartID{id}, []otogi.Part{part}, nil, nil)
	if err != nil {
		return otogi.NewFailedResult(fmt.Errorf("echo message: %w", err))
	}
	if err := invocation.Send(ctx, "echo", message); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return otogi.NewCanceledResult(err)
		}
		return otogi.NewFailedResult(err)
	}
	return otogi.NewSucceededResult()
}
