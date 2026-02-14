package otogi

import "context"

// EventHandler processes a single neutral event.
type EventHandler func(ctx context.Context, event *Event) error

// EventSink accepts neutral events for dispatching into the kernel.
type EventSink interface {
	// Publish submits an event to downstream subscribers.
	Publish(ctx context.Context, event *Event) error
}

// ModuleRuntime provides kernel facilities to modules during registration.
type ModuleRuntime interface {
	// Services exposes the service registry for dependency lookup.
	Services() ServiceRegistry
	// Subscribe registers an asynchronous event handler owned by the module.
	Subscribe(ctx context.Context, spec SubscriptionSpec, handler EventHandler) (Subscription, error)
}

// Module is a lifecycle-aware plugin contract.
//
// Modules must be deterministic and concurrency-safe because handlers can run
// on multiple workers.
type Module interface {
	// Name returns a stable module identifier.
	Name() string
	// Capabilities returns declarative processing and dependency metadata.
	Capabilities() []Capability
	// OnRegister is called once when the module is registered.
	OnRegister(ctx context.Context, runtime ModuleRuntime) error
	// OnStart is called when the kernel begins runtime execution.
	OnStart(ctx context.Context) error
	// OnShutdown is called during orderly shutdown.
	OnShutdown(ctx context.Context) error
}

// Driver adapts external platforms into neutral events.
//
// Drivers own transport/session concerns and must publish only otogi.Event.
type Driver interface {
	// Name returns a stable driver identifier.
	Name() string
	// Start starts consuming external updates and publishing neutral events.
	// It should return only after context cancellation or fatal error.
	Start(ctx context.Context, sink EventSink) error
	// Shutdown stops external resources that are not tied to Start context alone.
	Shutdown(ctx context.Context) error
}
