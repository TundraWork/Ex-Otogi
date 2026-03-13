package core

import (
	"context"

	"ex-otogi/pkg/otogi/platform"
)

// EventHandler processes one Otogi event selected by subscription filters.
//
// Handlers should treat event as immutable input owned by the kernel for the
// duration of the callback.
type EventHandler func(ctx context.Context, event *platform.Event) error

// EventDispatcher accepts normalized Otogi events for dispatching into the
// kernel.
type EventDispatcher interface {
	// Publish submits one event to downstream subscribers.
	//
	// Drivers should publish only otogi.Event payloads through this boundary and
	// should not depend on subscriber implementation details.
	Publish(ctx context.Context, event *platform.Event) error
}

// ModuleRuntime provides kernel facilities to modules during registration.
//
// Modules consume Otogi services through this interface rather than by
// depending on concrete driver implementations.
type ModuleRuntime interface {
	// Services exposes the service registry for dependency registration and
	// resolution.
	Services() ServiceRegistry
	// Config exposes the per-module configuration registry.
	Config() ConfigRegistry
	// Subscribe registers an asynchronous event handler owned by the module.
	//
	// The kernel is responsible for routing, worker management, and shutdown of
	// the resulting subscription.
	Subscribe(ctx context.Context, interest InterestSet, spec SubscriptionSpec, handler EventHandler) (Subscription, error)
}

// Module is a lifecycle-aware plugin contract.
//
// Modules must be deterministic and concurrency-safe because handlers can run
// on multiple workers. Modules consume inbound content as otogi.Event values
// and should produce outbound content only through resolved Otogi services such
// as SinkDispatcher and MediaDownloader.
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
	// Commands declares command registrations handled by this module.
	Commands []platform.CommandSpec
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
		cloned.Kinds = append([]platform.EventKind(nil), interest.Kinds...)
	}
	if len(interest.Sources) > 0 {
		cloned.Sources = append([]platform.EventSource(nil), interest.Sources...)
	}
	if len(interest.MediaTypes) > 0 {
		cloned.MediaTypes = append([]platform.MediaType(nil), interest.MediaTypes...)
	}
	if len(interest.CommandNames) > 0 {
		cloned.CommandNames = append([]string(nil), interest.CommandNames...)
	}

	return cloned
}

// Driver adapts one external platform into Otogi Protocol contracts.
//
// Drivers own transport/session concerns and must publish only normalized
// otogi.Event values. Platform SDK payloads, transport clients, and other
// driver-specific details must not leak across this boundary.
type Driver interface {
	// Name returns a stable driver identifier.
	Name() string
	// Start starts consuming external updates and publishing Otogi events.
	//
	// It should return only after context cancellation or fatal error.
	Start(ctx context.Context, dispatcher EventDispatcher) error
	// Shutdown stops external resources that are not tied to Start context alone.
	Shutdown(ctx context.Context) error
}
