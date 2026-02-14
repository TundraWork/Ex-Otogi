package telegram

import (
	"context"
	"fmt"
)

// UpdateHandler consumes decoded Telegram updates.
type UpdateHandler func(ctx context.Context, update Update) error

// UpdateSource streams Telegram updates into the adapter.
type UpdateSource interface {
	// Consume runs the update loop until context cancellation or fatal error.
	Consume(ctx context.Context, handler UpdateHandler) error
}

// NoopSource is a passive source useful for bootstrap wiring tests.
type NoopSource struct{}

// Consume blocks until context cancellation.
func (NoopSource) Consume(ctx context.Context, _ UpdateHandler) error {
	<-ctx.Done()

	return nil
}

// ChannelSource reads updates from a channel.
type ChannelSource struct {
	// Updates is the owned input stream consumed by the source loop.
	Updates <-chan Update
}

// Consume forwards channel updates until closure or cancellation.
func (s ChannelSource) Consume(ctx context.Context, handler UpdateHandler) error {
	if handler == nil {
		return fmt.Errorf("channel source: nil handler")
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case update, ok := <-s.Updates:
			if !ok {
				return nil
			}
			if err := handler(ctx, update); err != nil {
				return fmt.Errorf("channel source handle update %s: %w", update.Type, err)
			}
		}
	}
}
