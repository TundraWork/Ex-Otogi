package otogi

import (
	"context"
	"time"
)

// Record is one entry in the structured event log: something that happened
// to an event, a module, a driver, or the process. See docs/observability.md
// for the kinds in use.
type Record struct {
	// At is when it happened.
	At time.Time
	// Kind names what happened, for example "event.received" or "module.failed".
	Kind string
	// Component is who reported it, for example "kernel", "module:echo",
	// "driver:telegram/main", or "process".
	Component string
	// Event is the related event, if any.
	Event EventID
	// Platform, Account, and Conversation scope the record, if relevant.
	Platform     PlatformID
	Account      AccountID
	Conversation ConversationID
	// Outcome summarizes the result when meaningful, for example "ok" or "error".
	Outcome string
	// Duration is how long the reported operation took, if measured.
	Duration time.Duration
	// Payload is recorded in full as JSON: the event, the error and stack, the
	// sent message, and so on.
	Payload any
}

// Recorder writes records to the event log. Record never fails its caller:
// a write failure is reported by the recorder itself and processing goes on.
type Recorder interface {
	// Record writes r.
	Record(ctx context.Context, r Record)
}

// NopRecorder discards records.
type NopRecorder struct{}

// Record discards r.
func (NopRecorder) Record(context.Context, Record) {}
