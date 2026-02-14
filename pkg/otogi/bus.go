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
	Name           string
	Filter         InterestSet
	Buffer         int
	Workers        int
	HandlerTimeout time.Duration
	Backpressure   BackpressurePolicy
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
	Subscribe(ctx context.Context, spec SubscriptionSpec, handler EventHandler) (Subscription, error)
	// Close shuts down the bus and all active subscriptions.
	Close(ctx context.Context) error
}
