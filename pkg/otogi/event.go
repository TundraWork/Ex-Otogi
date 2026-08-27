package otogi

import (
	"fmt"
	"time"
)

// EventInputParams supplies the validated values used to construct an EventInput.
type EventInputParams struct {
	// Platform identifies the source platform.
	Platform PlatformID
	// Account identifies the source platform account.
	Account AccountID
	// Tenant identifies the isolation tenant.
	Tenant TenantID
	// Conversation identifies the source conversation.
	Conversation ConversationID
	// SourceEventID optionally identifies the event at its source.
	SourceEventID SourceEventID
	// Actor optionally identifies the originating actor.
	Actor ActorID
	// Kind identifies the event schema and routing kind.
	Kind EventKind
	// SchemaVersion is the nonzero version of the event schema.
	SchemaVersion uint64
	// OccurredAt is when the event occurred at its source.
	OccurredAt time.Time
	// IngestedAt is when the event entered the runtime boundary.
	IngestedAt time.Time
	// TraceID optionally carries validated trace context.
	TraceID TraceID
	// CausalEventID optionally identifies the causing runtime event.
	CausalEventID CausalEventID
	// Text is the event's exact text content.
	Text Text
}

// EventInput is an immutable admitted event before the kernel assigns its runtime ID.
type EventInput struct {
	platform      PlatformID
	account       AccountID
	tenant        TenantID
	conversation  ConversationID
	sourceEventID SourceEventID
	actor         ActorID
	kind          EventKind
	schemaVersion uint64
	occurredAt    time.Time
	ingestedAt    time.Time
	traceID       TraceID
	causalEventID CausalEventID
	text          Text
}

// NewEventInput validates required scope, kind, version, timestamps, and text and canonicalizes timestamps to UTC.
func NewEventInput(p EventInputParams) (EventInput, error) {
	occurred := p.OccurredAt.UTC().Round(0)
	ingested := p.IngestedAt.UTC().Round(0)
	if p.Platform.value == "" || p.Account.value == "" || p.Tenant.value == "" || p.Conversation.value == "" || p.Kind.value == "" || p.SchemaVersion == 0 || p.OccurredAt.IsZero() || p.IngestedAt.IsZero() || occurred.After(ingested) || p.Text.value == "" {
		return EventInput{}, fmt.Errorf("event input: %w", ErrInvalid)
	}
	return EventInput{p.Platform, p.Account, p.Tenant, p.Conversation, p.SourceEventID, p.Actor, p.Kind, p.SchemaVersion, occurred, ingested, p.TraceID, p.CausalEventID, p.Text}, nil
}

// Platform returns the event platform.
func (v EventInput) Platform() PlatformID { return v.platform }

// Account returns the event platform account.
func (v EventInput) Account() AccountID { return v.account }

// Tenant returns the event tenant.
func (v EventInput) Tenant() TenantID { return v.tenant }

// Conversation returns the event conversation.
func (v EventInput) Conversation() ConversationID { return v.conversation }

// SourceEventID returns the source identifier and whether one was supplied.
func (v EventInput) SourceEventID() (SourceEventID, bool) {
	return v.sourceEventID, v.sourceEventID.value != ""
}

// Actor returns the actor identifier and whether one was supplied.
func (v EventInput) Actor() (ActorID, bool) { return v.actor, v.actor.value != "" }

// Kind returns the event kind.
func (v EventInput) Kind() EventKind { return v.kind }

// SchemaVersion returns the nonzero event schema version.
func (v EventInput) SchemaVersion() uint64 { return v.schemaVersion }

// OccurredAt returns the occurrence time canonicalized to UTC without monotonic data.
func (v EventInput) OccurredAt() time.Time { return v.occurredAt }

// IngestedAt returns the ingestion time canonicalized to UTC without monotonic data.
func (v EventInput) IngestedAt() time.Time { return v.ingestedAt }

// TraceID returns the trace identifier and whether one was supplied.
func (v EventInput) TraceID() (TraceID, bool) { return v.traceID, v.traceID.value != "" }

// CausalEventID returns the causal event identifier and whether one was supplied.
func (v EventInput) CausalEventID() (CausalEventID, bool) {
	return v.causalEventID, v.causalEventID.value != ""
}

// Text returns the event text.
func (v EventInput) Text() Text { return v.text }

// Event exposes read-only runtime event data to modules.
type Event interface {
	// ID returns the kernel-assigned runtime event identifier.
	ID() EventID
	// Platform returns the event platform.
	Platform() PlatformID
	// Account returns the event platform account.
	Account() AccountID
	// Tenant returns the event tenant.
	Tenant() TenantID
	// Conversation returns the event conversation.
	Conversation() ConversationID
	// SourceEventID returns the source identifier and whether one was supplied.
	SourceEventID() (SourceEventID, bool)
	// Actor returns the actor identifier and whether one was supplied.
	Actor() (ActorID, bool)
	// Kind returns the event kind.
	Kind() EventKind
	// SchemaVersion returns the nonzero event schema version.
	SchemaVersion() uint64
	// OccurredAt returns the canonical occurrence time.
	OccurredAt() time.Time
	// IngestedAt returns the canonical ingestion time.
	IngestedAt() time.Time
	// TraceID returns the trace identifier and whether one was supplied.
	TraceID() (TraceID, bool)
	// CausalEventID returns the causal identifier and whether one was supplied.
	CausalEventID() (CausalEventID, bool)
	// Text returns the exact event text.
	Text() Text
}
