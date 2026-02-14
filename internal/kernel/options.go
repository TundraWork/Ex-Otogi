package kernel

import (
	"context"
	"log/slog"
	"time"
)

const (
	defaultModuleHookTimeout  = 5 * time.Second
	defaultShutdownTimeout    = 10 * time.Second
	defaultSubscriptionBuffer = 256
	defaultSubscriptionWorker = 1
	defaultHandlerTimeout     = 3 * time.Second
)

// config stores resolved kernel runtime settings after option application.
type config struct {
	moduleHookTimeout  time.Duration
	shutdownTimeout    time.Duration
	subscriptionBuffer int
	subscriptionWorker int
	handlerTimeout     time.Duration
	logger             *slog.Logger
	onAsyncError       func(context.Context, string, error)
}

// Option mutates kernel construction configuration.
type Option func(*config)

// defaultConfig returns production-safe defaults for kernel runtime controls.
func defaultConfig() config {
	logger := slog.Default()

	return config{
		moduleHookTimeout:  defaultModuleHookTimeout,
		shutdownTimeout:    defaultShutdownTimeout,
		subscriptionBuffer: defaultSubscriptionBuffer,
		subscriptionWorker: defaultSubscriptionWorker,
		handlerTimeout:     defaultHandlerTimeout,
		logger:             logger,
		onAsyncError: func(ctx context.Context, scope string, err error) {
			logger.ErrorContext(ctx, "otogi async error", "scope", scope, "error", err)
		},
	}
}

// WithModuleHookTimeout configures OnRegister/OnStart/OnShutdown timeout boundaries.
func WithModuleHookTimeout(timeout time.Duration) Option {
	return func(cfg *config) {
		if timeout > 0 {
			cfg.moduleHookTimeout = timeout
		}
	}
}

// WithShutdownTimeout configures overall kernel shutdown timeout.
func WithShutdownTimeout(timeout time.Duration) Option {
	return func(cfg *config) {
		if timeout > 0 {
			cfg.shutdownTimeout = timeout
		}
	}
}

// WithDefaultSubscriptionBuffer configures default subscriber queue depth.
func WithDefaultSubscriptionBuffer(size int) Option {
	return func(cfg *config) {
		if size > 0 {
			cfg.subscriptionBuffer = size
		}
	}
}

// WithDefaultSubscriptionWorkers configures default subscriber worker count.
func WithDefaultSubscriptionWorkers(workers int) Option {
	return func(cfg *config) {
		if workers > 0 {
			cfg.subscriptionWorker = workers
		}
	}
}

// WithDefaultHandlerTimeout configures default per-event handler timeout.
func WithDefaultHandlerTimeout(timeout time.Duration) Option {
	return func(cfg *config) {
		if timeout > 0 {
			cfg.handlerTimeout = timeout
		}
	}
}

// WithLogger configures logger used by kernel and default async error sink.
func WithLogger(logger *slog.Logger) Option {
	return func(cfg *config) {
		if logger == nil {
			return
		}

		cfg.logger = logger
		cfg.onAsyncError = func(ctx context.Context, scope string, err error) {
			logger.ErrorContext(ctx, "otogi async error", "scope", scope, "error", err)
		}
	}
}

// WithAsyncErrorHandler configures asynchronous worker error reporting.
func WithAsyncErrorHandler(handler func(context.Context, string, error)) Option {
	return func(cfg *config) {
		if handler != nil {
			cfg.onAsyncError = handler
		}
	}
}
