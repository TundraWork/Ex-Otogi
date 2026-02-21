package kernel

import (
	"context"
	"log/slog"
	"time"

	"ex-otogi/pkg/otogi"
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
	routing            routingConfig
}

// ModuleRoute configures inbound source filters and default outbound sink for one module.
type ModuleRoute struct {
	// Sources restricts inbound delivery to matching event sources.
	Sources []otogi.EventSource
	// Sink configures the default outbound sink used when request target omits sink.
	Sink *otogi.EventSink
}

type routingConfig struct {
	defaultRoute *ModuleRoute
	moduleRoutes map[string]ModuleRoute
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
		routing: routingConfig{
			moduleRoutes: make(map[string]ModuleRoute),
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

// WithModuleRouting configures module inbound source filters and default sink routing.
func WithModuleRouting(defaultRoute *ModuleRoute, routes map[string]ModuleRoute) Option {
	return func(cfg *config) {
		cfg.routing.defaultRoute = cloneRoute(defaultRoute)
		cfg.routing.moduleRoutes = make(map[string]ModuleRoute, len(routes))
		for moduleName, route := range routes {
			cfg.routing.moduleRoutes[moduleName] = *cloneRoute(&route)
		}
	}
}

func cloneRoute(route *ModuleRoute) *ModuleRoute {
	if route == nil {
		return nil
	}
	cloned := ModuleRoute{}
	if len(route.Sources) > 0 {
		cloned.Sources = append([]otogi.EventSource(nil), route.Sources...)
	}
	if route.Sink != nil {
		sink := *route.Sink
		cloned.Sink = &sink
	}

	return &cloned
}
