package otogi

import (
	"context"
	"time"
)

// Ordering says which of a module's deliveries the kernel runs in sequence.
type Ordering int

const (
	// OrderPerConversation runs a module's deliveries for one conversation one
	// at a time, in arrival order; different conversations run in parallel.
	// It is the default.
	OrderPerConversation Ordering = iota
	// OrderNone runs every delivery independently, fully concurrently.
	OrderNone
)

// ModuleDescriptor declares a module's identity and how the kernel routes and
// runs its deliveries. New fields are added over time; the zero value of each
// is the default.
type ModuleDescriptor struct {
	// ID identifies the module; it must be unique and stable.
	ID ModuleID
	// Subscribes lists the actions the module receives.
	Subscribes []EventAction
	// Ordering selects sequencing; the default is OrderPerConversation.
	Ordering Ordering
	// Timeout bounds one delivery; zero means the kernel default.
	Timeout time.Duration
}

// Module is bot behavior. The kernel calls Handle once per subscribed event,
// concurrently with other modules and, subject to Ordering, with other events.
//
// Modules run independently: there is no order between modules for the same
// event. Cross-cutting decisions that must happen first belong in Middleware.
// Optional capabilities (lifecycle hooks, configuration) are added as separate
// interfaces that the kernel detects, so existing modules keep compiling.
type Module interface {
	// Descriptor returns the module's declaration. It is read once at startup.
	Descriptor() ModuleDescriptor
	// Handle processes one event. A nil error means handled, including when
	// the module decides to ignore the event. A non-nil error is recorded as a
	// failed delivery; it does not affect other modules.
	Handle(ctx context.Context, event Event, inv Invocation) error
}

// Invocation gives a module the capabilities it may use while handling one
// event. The kernel implements it; new capabilities are added as new methods.
type Invocation interface {
	// Send sends msg to the event's conversation. slot names this effect
	// within the module's handling of the event, for example "reply", so a
	// replayed event derives the same idempotency key and is not sent twice.
	Send(ctx context.Context, slot string, msg Message) error
}

// Verdict is a middleware decision.
type Verdict int

const (
	// Continue passes the event on to the next middleware and then the modules.
	Continue Verdict = iota
	// Drop stops the event; no module receives it. The drop is recorded.
	Drop
)

// Middleware runs on every event before modules, synchronously and in
// registration order. It may modify the event or drop it, for example to apply
// an allowlist, sleep mode, or command parsing. It must be fast; slow work
// belongs in modules. An error drops the event and is recorded.
type Middleware interface {
	// ID identifies the middleware.
	ID() MiddlewareID
	// Intercept inspects and may modify event.
	Intercept(ctx context.Context, event *Event) (Verdict, error)
}

// Outcome reports how the kernel processed one event.
type Outcome struct {
	// EventID identifies the event.
	EventID EventID
	// Err is set when the event did not reach the modules: it was dropped by
	// a middleware error or abandoned at shutdown.
	Err error
	// DroppedBy names the middleware that dropped the event, if any.
	DroppedBy MiddlewareID
	// Modules has one entry per delivery, in module ID order.
	Modules []ModuleOutcome
}

// ModuleOutcome reports one module's handling of one event.
type ModuleOutcome struct {
	// Module identifies the module.
	Module ModuleID
	// Err is the module's error, a recovered panic, or a timeout.
	Err error
	// Duration is how long the delivery ran.
	Duration time.Duration
}
