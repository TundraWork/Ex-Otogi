package otogi

import (
	"context"
	"slices"
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
	// Commands are the commands the module handles. An event carrying one of
	// them goes only to the modules that declare it. Names are unique across
	// modules.
	Commands []CommandSpec
	// Filter narrows which events the module receives.
	Filter Filter
	// Provides lists the services the module registers with Host.Provide.
	Provides []string
	// Requires lists the services the module uses. Modules start after the
	// providers of what they require.
	Requires []string
}

// CommandSpec declares a command a module handles.
type CommandSpec struct {
	// Name is the command without prefix, for example "sleep".
	Name string
	// Usage shows the arguments, for example "<duration>".
	Usage string
	// Description says what the command does.
	Description string
}

// Filter narrows the events a module receives. Each empty field means "no
// restriction", except Directions: empty means incoming events only, so a
// module that replies never sees (and answers) the account's own messages.
// Events with no direction set pass.
type Filter struct {
	// Platforms limits events to these platforms.
	Platforms []PlatformID
	// ConversationKinds limits events to these conversation kinds.
	ConversationKinds []ConversationKind
	// Directions lists the directions the module receives.
	Directions []Direction
}

// Matches reports whether f admits e.
func (f Filter) Matches(e Event) bool {
	directions := f.Directions
	if len(directions) == 0 {
		directions = []Direction{DirectionIncoming}
	}
	return (len(f.Platforms) == 0 || slices.Contains(f.Platforms, e.Platform)) &&
		(len(f.ConversationKinds) == 0 || slices.Contains(f.ConversationKinds, e.Conversation.Kind)) &&
		(e.Direction == "" || slices.Contains(directions, e.Direction))
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

// Invocation gives a module the capabilities it may use for one unit of work.
// The kernel implements it; new capabilities are added as new methods.
//
// Every effect names a slot, unique within the unit of work, for example
// "reply" or "edit-2". The slot makes the effect's idempotency key stable, so
// a replayed event repeats no effect. Platforms that cannot perform an effect
// return ErrUnsupported.
type Invocation interface {
	// Host returns the module's long-lived capabilities.
	Host() Host
	// Origin says what this unit of work is for: an event, a job run, or a
	// timer.
	Origin() Origin
	// Send sends msg to the event's conversation and returns a reference to
	// the sent message. A replay returns the original reference. Its
	// SourceEvent may be empty when the platform confirms a duplicate without
	// identifying the message.
	Send(ctx context.Context, slot string, msg Message) (EventRef, error)
	// SendTo sends msg to another conversation.
	SendTo(ctx context.Context, slot string, to Destination, msg Message) (EventRef, error)
	// Edit replaces the content of the message at ref.
	Edit(ctx context.Context, slot string, ref EventRef, msg Message) error
	// Delete deletes the message at ref.
	Delete(ctx context.Context, slot string, ref EventRef) error
	// React sets the account's reaction to the message at ref; "" removes it.
	React(ctx context.Context, slot string, ref EventRef, reaction string) error
	// FetchMedia downloads media from a message seen at from.
	FetchMedia(ctx context.Context, from Destination, src MediaSource) ([]byte, error)
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
