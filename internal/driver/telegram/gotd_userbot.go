package telegram

import (
	"context"
	"fmt"
	"time"
)

const defaultReactionPollInterval = 2 * time.Second

// GotdUserbotClient abstracts gotd/td userbot session execution.
type GotdUserbotClient interface {
	// Run starts the session and executes fn within the connected lifecycle.
	Run(ctx context.Context, fn func(runCtx context.Context) error) error
}

// GotdRawUpdateStream provides raw gotd updates from an active session.
type GotdRawUpdateStream interface {
	// Updates returns a channel of raw gotd updates bound to ctx lifetime.
	Updates(ctx context.Context) (<-chan any, error)
}

// GotdUpdateMapper maps raw gotd updates into adapter Update DTOs.
type GotdUpdateMapper interface {
	// Map converts a raw update into adapter DTO form.
	// The accepted flag allows skipping unsupported update classes.
	Map(ctx context.Context, raw any) (Update, bool, error)
}

// GotdUpdateBatchMapper maps one raw gotd update into zero or more adapter updates.
type GotdUpdateBatchMapper interface {
	// MapBatch converts one raw update into a batch of adapter DTO updates.
	//
	// Returning an empty slice means the update is intentionally skipped.
	MapBatch(ctx context.Context, raw any) ([]Update, error)
}

// GotdReactionUpdatePoller provides optional reaction backfill polling.
type GotdReactionUpdatePoller interface {
	// PollReactionUpdates returns synthetic reaction updates discovered by polling.
	PollReactionUpdates(ctx context.Context) ([]Update, error)
}

// GotdUserbotSource wires gotd userbot updates into UpdateSource.
type GotdUserbotSource struct {
	client GotdUserbotClient
	stream GotdRawUpdateStream
	mapper GotdUpdateMapper
}

// NewGotdUserbotSource creates a source backed by gotd userbot session APIs.
func NewGotdUserbotSource(
	client GotdUserbotClient,
	stream GotdRawUpdateStream,
	mapper GotdUpdateMapper,
) (*GotdUserbotSource, error) {
	if client == nil {
		return nil, fmt.Errorf("new gotd userbot source: nil client")
	}
	if stream == nil {
		return nil, fmt.Errorf("new gotd userbot source: nil stream")
	}
	if mapper == nil {
		return nil, fmt.Errorf("new gotd userbot source: nil mapper")
	}

	return &GotdUserbotSource{
		client: client,
		stream: stream,
		mapper: mapper,
	}, nil
}

// Consume runs a gotd session and forwards mapped updates to the handler.
func (s *GotdUserbotSource) Consume(ctx context.Context, handler UpdateHandler) error {
	if handler == nil {
		return fmt.Errorf("consume gotd userbot updates: nil handler")
	}

	err := s.client.Run(ctx, func(runCtx context.Context) error {
		updates, err := s.stream.Updates(runCtx)
		if err != nil {
			return fmt.Errorf("get gotd updates stream: %w", err)
		}

		var (
			reactionPoller GotdReactionUpdatePoller
			pollTick       <-chan time.Time
		)
		if poller, ok := s.mapper.(GotdReactionUpdatePoller); ok {
			reactionPoller = poller
			ticker := time.NewTicker(defaultReactionPollInterval)
			defer ticker.Stop()
			pollTick = ticker.C
		}

		for {
			select {
			case <-runCtx.Done():
				return nil
			case <-pollTick:
				if reactionPoller == nil {
					continue
				}

				polled, pollErr := reactionPoller.PollReactionUpdates(runCtx)
				if pollErr != nil {
					return fmt.Errorf("poll gotd reaction updates: %w", pollErr)
				}
				for _, update := range polled {
					if err := handler(runCtx, update); err != nil {
						return fmt.Errorf("consume polled gotd update %s: %w", update.Type, err)
					}
				}
			case rawUpdate, ok := <-updates:
				if !ok {
					return nil
				}

				mapped, mapErr := s.mapUpdateSafely(runCtx, rawUpdate)
				if mapErr != nil {
					return fmt.Errorf("map gotd update: %w", mapErr)
				}
				for _, update := range mapped {
					if err := handler(runCtx, update); err != nil {
						return fmt.Errorf("consume gotd update %s: %w", update.Type, err)
					}
				}
			}
		}
	})
	if err != nil {
		return fmt.Errorf("consume gotd userbot updates: %w", err)
	}

	return nil
}

// mapUpdateSafely isolates mapper panics so a bad mapping path cannot crash the process.
func (s *GotdUserbotSource) mapUpdateSafely(ctx context.Context, rawUpdate any) (mapped []Update, err error) {
	defer func() {
		recovered := recover()
		if recovered == nil {
			return
		}
		err = fmt.Errorf("map gotd update panic: %v", recovered)
	}()

	if batchMapper, ok := s.mapper.(GotdUpdateBatchMapper); ok {
		mapped, err = batchMapper.MapBatch(ctx, rawUpdate)
		if err != nil {
			return nil, fmt.Errorf("map gotd raw update: %w", err)
		}

		return mapped, nil
	}

	single, accepted, err := s.mapper.Map(ctx, rawUpdate)
	if err != nil {
		return nil, fmt.Errorf("map gotd raw update: %w", err)
	}
	if !accepted {
		return nil, nil
	}

	return []Update{single}, nil
}
