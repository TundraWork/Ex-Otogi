package telegram

import (
	"context"
	"errors"
	"fmt"
	"time"

	"ex-otogi/pkg/otogi"
)

const defaultPublishTimeout = 2 * time.Second

// driverConfig contains runtime controls for publish timeout and error reporting.
type driverConfig struct {
	name           string
	publishTimeout time.Duration
	onAsyncError   func(context.Context, error)
}

// DriverOption mutates Telegram driver configuration.
type DriverOption func(*driverConfig)

// WithName configures the driver identity exposed to the kernel.
func WithName(name string) DriverOption {
	return func(cfg *driverConfig) {
		if name != "" {
			cfg.name = name
		}
	}
}

// WithPublishTimeout configures sink publish timeout per event.
func WithPublishTimeout(timeout time.Duration) DriverOption {
	return func(cfg *driverConfig) {
		if timeout > 0 {
			cfg.publishTimeout = timeout
		}
	}
}

// WithErrorHandler configures async callback errors.
func WithErrorHandler(handler func(context.Context, error)) DriverOption {
	return func(cfg *driverConfig) {
		if handler != nil {
			cfg.onAsyncError = handler
		}
	}
}

// Driver adapts Telegram updates into neutral otogi events.
type Driver struct {
	cfg     driverConfig
	source  UpdateSource
	decoder Decoder
}

// NewDriver creates a Telegram driver.
func NewDriver(source UpdateSource, decoder Decoder, options ...DriverOption) (*Driver, error) {
	if source == nil {
		return nil, fmt.Errorf("new telegram driver: nil source")
	}
	if decoder == nil {
		return nil, fmt.Errorf("new telegram driver: nil decoder")
	}

	cfg := driverConfig{
		name:           DriverType,
		publishTimeout: defaultPublishTimeout,
		onAsyncError:   func(context.Context, error) {},
	}
	for _, option := range options {
		option(&cfg)
	}

	return &Driver{
		cfg:     cfg,
		source:  source,
		decoder: decoder,
	}, nil
}

// Name returns the stable driver identifier.
func (d *Driver) Name() string {
	return d.cfg.name
}

// Start consumes Telegram updates and publishes neutral events.
func (d *Driver) Start(ctx context.Context, sink otogi.EventDispatcher) error {
	if sink == nil {
		return fmt.Errorf("start telegram driver: nil sink")
	}

	handler := func(handlerCtx context.Context, update Update) error {
		return d.handleUpdate(handlerCtx, update, sink)
	}

	if err := d.source.Consume(ctx, handler); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil
		}

		return fmt.Errorf("start telegram driver: consume updates: %w", err)
	}

	return nil
}

// handleUpdate decodes one platform update and publishes it with bounded latency.
func (d *Driver) handleUpdate(ctx context.Context, update Update, sink otogi.EventDispatcher) error {
	event, err := d.decodeSafely(ctx, update)
	if err != nil {
		d.cfg.onAsyncError(ctx, err)
		return fmt.Errorf("handle update %s: %w", update.Type, err)
	}
	if event != nil {
		if event.Source.Platform == "" {
			event.Source.Platform = DriverPlatform
		}
		if event.Source.ID == "" {
			event.Source.ID = d.cfg.name
		}
		if event.Platform == "" {
			event.Platform = event.Source.Platform
		}
	}

	publishCtx := ctx
	cancel := func() {}
	if d.cfg.publishTimeout > 0 {
		publishCtxWithTimeout, publishCancel := context.WithTimeout(ctx, d.cfg.publishTimeout)
		publishCtx = publishCtxWithTimeout
		cancel = publishCancel
	}
	defer cancel()

	if err := sink.Publish(publishCtx, event); err != nil {
		return fmt.Errorf("handle update %s publish: %w", update.Type, err)
	}

	return nil
}

// decodeSafely protects decoder panics at the adapter boundary.
func (d *Driver) decodeSafely(ctx context.Context, update Update) (decoded *otogi.Event, err error) {
	defer func() {
		recovered := recover()
		if recovered == nil {
			return
		}
		err = fmt.Errorf("decode telegram update %s panic: %v", update.Type, recovered)
	}()

	decoded, err = d.decoder.Decode(ctx, update)
	if err != nil {
		return nil, fmt.Errorf("decode telegram update %s: %w", update.Type, err)
	}

	return decoded, nil
}

// Shutdown releases resources not controlled by Start context.
func (d *Driver) Shutdown(_ context.Context) error {
	return nil
}
