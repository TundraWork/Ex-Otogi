// Package scripted provides a synchronous finite event source.
package scripted

import (
	"context"
	"fmt"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// EventSink accepts one event input from a source.
type EventSink interface {
	// Submit admits input or returns why admission failed.
	Submit(context.Context, otogi.EventInput) error
}

// Source synchronously submits a copied finite sequence of event inputs.
type Source struct {
	inputs []otogi.EventInput
}

// New copies inputs and returns an inert finite source.
func New(inputs []otogi.EventInput) *Source {
	return &Source{inputs: append([]otogi.EventInput(nil), inputs...)}
}

// Run submits inputs serially in construction order and stops on cancellation or sink failure.
func (s *Source) Run(ctx context.Context, sink EventSink) error {
	if sink == nil {
		return fmt.Errorf("scripted run: %w", otogi.ErrInvalid)
	}
	for _, input := range s.inputs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := sink.Submit(ctx, input); err != nil {
			return fmt.Errorf("scripted submit: %w", err)
		}
	}
	return nil
}
