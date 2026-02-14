package otogi

import (
	"context"
	"time"
)

// BackpressurePolicy defines how queues behave when subscriber buffers are full.
type BackpressurePolicy string

const (
	// BackpressureDropNewest drops the incoming event when full.
	BackpressureDropNewest BackpressurePolicy = "drop_newest"
	// BackpressureDropOldest evicts the oldest queued event before enqueue.
	BackpressureDropOldest BackpressurePolicy = "drop_oldest"
	// BackpressureBlock blocks until queue space is available or context is canceled.
	BackpressureBlock BackpressurePolicy = "block"
)

// SubscriptionSpec configures a single consumer subscription.
type SubscriptionSpec struct {
	// Name is the stable identifier used for diagnostics and lifecycle operations.
	Name string
	// Buffer is the per-subscription queue capacity before backpressure handling applies.
	// Zero or negative values defer to EventBus runtime defaults.
	Buffer int
	// Workers is the number of handler goroutines consuming from this subscription.
	// Zero or negative values defer to EventBus runtime defaults.
	Workers int
	// HandlerTimeout bounds each handler invocation.
	// Zero or negative values defer to EventBus runtime defaults.
	HandlerTimeout time.Duration
	// Backpressure defines how publish behaves when Buffer is full.
	// Empty value defers to EventBus runtime defaults.
	Backpressure BackpressurePolicy
}

// NewDefaultSubscriptionSpec returns a subscription spec that opts into runtime defaults.
//
// Callers typically set only Name and allow the kernel/event bus configuration
// to decide queue size, worker count, timeout, and backpressure policy.
func NewDefaultSubscriptionSpec(name string) SubscriptionSpec {
	return SubscriptionSpec{
		Name: name,
	}
}

// Subscription controls an active event stream registration.
type Subscription interface {
	// Name returns the subscription identifier.
	Name() string
	// Close stops delivery for this subscription.
	Close(ctx context.Context) error
}

// EventBus is the asynchronous pub/sub contract used by the kernel.
type EventBus interface {
	EventSink
	// Subscribe registers a handler with bounded buffering semantics.
	Subscribe(ctx context.Context, interest InterestSet, spec SubscriptionSpec, handler EventHandler) (Subscription, error)
	// Close shuts down the bus and all active subscriptions.
	Close(ctx context.Context) error
}
