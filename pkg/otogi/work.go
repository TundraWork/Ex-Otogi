package otogi

import "context"

// Work identifies the unit of module work a context belongs to. The kernel
// attaches it to the context of every event delivery, job run, and timer
// run, so shared services that a module calls, such as an LLM provider, can
// attribute what they record to the module and the event it served.
type Work struct {
	// Module is the module doing the work.
	Module ModuleID `json:"module"`
	// Origin identifies the unit of work: an event ID, or a job or timer run
	// ID, as in Effect.Origin.
	Origin string `json:"origin"`
	// Event is the event being handled; empty for jobs and timers.
	Event EventID `json:"event,omitempty"`
	// Destination is the event's conversation; zero for jobs and timers.
	Destination Destination `json:"destination,omitzero"`
}

type workKey struct{}

// ContextWithWork returns ctx carrying w.
func ContextWithWork(ctx context.Context, w Work) context.Context {
	return context.WithValue(ctx, workKey{}, w)
}

// WorkFrom returns the unit of work ctx belongs to, if any.
func WorkFrom(ctx context.Context) (Work, bool) {
	w, ok := ctx.Value(workKey{}).(Work)
	return w, ok
}
