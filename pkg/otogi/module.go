package otogi

import (
	"context"
)

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
	Subscribe(ctx context.Context, interest InterestSet, spec SubscriptionSpec, handler EventHandler) (Subscription, error)
}

// Module is a lifecycle-aware plugin contract.
//
// Modules must be deterministic and concurrency-safe because handlers can run
// on multiple workers.
type Module interface {
	// Name returns a stable module identifier.
	Name() string
	// Spec returns declarative handler/capability metadata for registration.
	Spec() ModuleSpec
	// OnStart is called when the kernel begins runtime execution.
	OnStart(ctx context.Context) error
	// OnShutdown is called during orderly shutdown.
	OnShutdown(ctx context.Context) error
}

// ModuleRegistrar is an optional imperative registration hook.
//
// Use this only for advanced setup that cannot be represented declaratively
// in ModuleSpec, such as dynamic subscriptions or one-time service resolution.
type ModuleRegistrar interface {
	// OnRegister is called once when the module is registered.
	OnRegister(ctx context.Context, runtime ModuleRuntime) error
}

// ModuleSpec describes declarative module registration.
//
// Handler capabilities are the primary declaration path. AdditionalCapabilities
// exists for advanced cases where ModuleRegistrar registers extra subscriptions.
type ModuleSpec struct {
	// Handlers declares declarative capability and subscription wiring.
	Handlers []ModuleHandler
	// AdditionalCapabilities declares extra interests used by imperative registration.
	AdditionalCapabilities []Capability
}

// Capabilities returns a defensive copy of all declared capabilities.
func (s ModuleSpec) Capabilities() []Capability {
	capabilities := make([]Capability, 0, len(s.Handlers)+len(s.AdditionalCapabilities))
	for _, handler := range s.Handlers {
		capabilities = append(capabilities, cloneCapability(handler.Capability))
	}
	for _, capability := range s.AdditionalCapabilities {
		capabilities = append(capabilities, cloneCapability(capability))
	}

	return capabilities
}

// ModuleHandler declares one capability + subscription wiring pair.
// Event selection is defined by Capability.Interest.
type ModuleHandler struct {
	// Capability declares interest and dependency requirements.
	Capability Capability
	// Subscription configures buffering, concurrency, and backpressure behavior.
	Subscription SubscriptionSpec
	// Handler processes matching events for this declaration.
	Handler EventHandler
}

// cloneCapability copies all owned slices/maps so runtime mutation cannot leak.
func cloneCapability(capability Capability) Capability {
	cloned := capability
	cloned.Interest = cloneInterestSet(capability.Interest)
	if len(capability.RequiredServices) > 0 {
		cloned.RequiredServices = append([]string(nil), capability.RequiredServices...)
	}
	if len(capability.Metadata) > 0 {
		cloned.Metadata = make(map[string]string, len(capability.Metadata))
		for key, value := range capability.Metadata {
			cloned.Metadata[key] = value
		}
	}

	return cloned
}

// cloneInterestSet copies the owned slices in an interest set.
func cloneInterestSet(interest InterestSet) InterestSet {
	cloned := interest
	if len(interest.Kinds) > 0 {
		cloned.Kinds = append([]EventKind(nil), interest.Kinds...)
	}
	if len(interest.MediaTypes) > 0 {
		cloned.MediaTypes = append([]MediaType(nil), interest.MediaTypes...)
	}

	return cloned
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
