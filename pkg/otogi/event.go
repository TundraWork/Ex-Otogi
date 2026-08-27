package otogi

import (
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

// EventAction identifies an event lifecycle action used for routing.
type EventAction string

const (
	// ActionMessageCreated identifies a newly created message.
	ActionMessageCreated EventAction = "message.created"
	// ActionMessageEdited identifies an edited message.
	ActionMessageEdited EventAction = "message.edited"
	// ActionMessageDeleted identifies a deleted message.
	ActionMessageDeleted EventAction = "message.deleted"
	// ActionMessageService identifies a protocol service message.
	ActionMessageService EventAction = "message.service"
)

// NewEventAction validates a core action or an absolute namespaced action.
func NewEventAction(value string) (EventAction, error) {
	action := EventAction(value)
	if !validEventAction(action) {
		return "", fmt.Errorf("event action: %w", ErrInvalid)
	}
	return action, nil
}

func validEventAction(action EventAction) bool {
	value := string(action)
	if value == "" || len(value) > 512 || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	switch action {
	case ActionMessageCreated, ActionMessageEdited, ActionMessageDeleted, ActionMessageService:
		return true
	}
	parsed, err := url.Parse(value)
	return err == nil && parsed.IsAbs()
}

// String returns the exact validated action.
func (a EventAction) String() string { return string(a) }

// Direction identifies protocol-defined event flow.
type Direction string

const (
	// DirectionIncoming identifies an inbound event.
	DirectionIncoming Direction = "incoming"
	// DirectionOutgoing identifies an outbound event.
	DirectionOutgoing Direction = "outgoing"
	// DirectionSystem identifies an event without peer flow semantics.
	DirectionSystem Direction = "system"
)

// Completeness describes availability of event content.
type Completeness string

const (
	// CompletenessComplete means all known content is present.
	CompletenessComplete Completeness = "complete"
	// CompletenessPartial means only some content is available.
	CompletenessPartial Completeness = "partial"
	// CompletenessRedacted means content was intentionally removed.
	CompletenessRedacted Completeness = "redacted"
	// CompletenessUnavailable means content could not be obtained or decoded.
	CompletenessUnavailable Completeness = "unavailable"
)

// EventInputParams supplies values used to construct an EventInput.
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
	// Action identifies the lifecycle action.
	Action EventAction
	// Direction identifies protocol-defined event flow.
	Direction Direction
	// OccurredAt is when the event occurred at its source.
	OccurredAt time.Time
	// IngestedAt is when the event entered the runtime boundary.
	IngestedAt time.Time
	// EditedAt optionally records the source edit time.
	EditedAt time.Time
	// TraceID optionally carries validated trace context.
	TraceID TraceID
	// CausalEventID optionally identifies the causing runtime event.
	CausalEventID CausalEventID
	// Completeness describes availability of Message.
	Completeness Completeness
	// ProviderLocator optionally carries adapter-owned source identity.
	ProviderLocator ProviderLocator
	// Message contains immutable rich content and relations.
	Message Message
	// Extensions contains ordered envelope preservation records.
	Extensions []Extension
}

// EventInput is an immutable admitted event before the kernel assigns its runtime ID.
type EventInput struct {
	platform        PlatformID
	account         AccountID
	tenant          TenantID
	conversation    ConversationID
	sourceEventID   SourceEventID
	actor           ActorID
	action          EventAction
	direction       Direction
	occurredAt      time.Time
	ingestedAt      time.Time
	editedAt        time.Time
	traceID         TraceID
	causalEventID   CausalEventID
	completeness    Completeness
	providerLocator ProviderLocator
	message         Message
	extensions      []Extension
}

// NewEventInput validates context and defensively owns rich envelope data.
func NewEventInput(p EventInputParams) (EventInput, error) {
	occurred, ingested, edited := p.OccurredAt.UTC().Round(0), p.IngestedAt.UTC().Round(0), p.EditedAt.UTC().Round(0)
	if p.Platform.value == "" || p.Account.value == "" || p.Tenant.value == "" || p.Conversation.value == "" || !validEventAction(p.Action) || !validDirection(p.Direction) || !validCompleteness(p.Completeness) || p.OccurredAt.IsZero() || p.IngestedAt.IsZero() || occurred.After(ingested) || (!p.EditedAt.IsZero() && edited.Before(occurred)) || len(p.Extensions) > maxOwnerExtensions {
		return EventInput{}, fmt.Errorf("event input: %w", ErrInvalid)
	}
	if len(p.Message.parts) == 0 && p.Completeness == CompletenessComplete {
		return EventInput{}, fmt.Errorf("event input content: %w", ErrInvalid)
	}
	if len(p.Message.parts) == 0 && p.SourceEventID.value == "" && p.ProviderLocator.NamespaceURI() == "" {
		return EventInput{}, fmt.Errorf("event input source proof: %w", ErrInvalid)
	}
	for _, extension := range p.Extensions {
		if !validExtension(extension) {
			return EventInput{}, fmt.Errorf("event input extension: %w", ErrInvalid)
		}
	}
	if err := validateEventAggregates(p.Message, p.Extensions, p.ProviderLocator); err != nil {
		return EventInput{}, fmt.Errorf("event input aggregates: %w", err)
	}
	ownedExtensions := append([]Extension(nil), p.Extensions...)
	return EventInput{platform: p.Platform, account: p.Account, tenant: p.Tenant, conversation: p.Conversation, sourceEventID: p.SourceEventID, actor: p.Actor, action: p.Action, direction: p.Direction, occurredAt: occurred, ingestedAt: ingested, editedAt: edited, traceID: p.TraceID, causalEventID: p.CausalEventID, completeness: p.Completeness, providerLocator: p.ProviderLocator, message: p.Message, extensions: ownedExtensions}, nil
}

func validDirection(v Direction) bool {
	return v == DirectionIncoming || v == DirectionOutgoing || v == DirectionSystem
}
func validCompleteness(v Completeness) bool {
	return v == CompletenessComplete || v == CompletenessPartial || v == CompletenessRedacted || v == CompletenessUnavailable
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

// Action returns the lifecycle action.
func (v EventInput) Action() EventAction { return v.action }

// Direction returns protocol-defined event flow.
func (v EventInput) Direction() Direction { return v.direction }

// OccurredAt returns the canonical occurrence time.
func (v EventInput) OccurredAt() time.Time { return v.occurredAt }

// IngestedAt returns the canonical ingestion time.
func (v EventInput) IngestedAt() time.Time { return v.ingestedAt }

// EditedAt returns the canonical edit time and whether one was supplied.
func (v EventInput) EditedAt() (time.Time, bool) { return v.editedAt, !v.editedAt.IsZero() }

// TraceID returns the trace identifier and whether one was supplied.
func (v EventInput) TraceID() (TraceID, bool) { return v.traceID, v.traceID.value != "" }

// CausalEventID returns the causal event identifier and whether one was supplied.
func (v EventInput) CausalEventID() (CausalEventID, bool) {
	return v.causalEventID, v.causalEventID.value != ""
}

// Completeness returns content availability.
func (v EventInput) Completeness() Completeness { return v.completeness }

// ProviderLocator returns adapter-owned source identity and whether one was supplied.
func (v EventInput) ProviderLocator() (ProviderLocator, bool) {
	return v.providerLocator, v.providerLocator.NamespaceURI() != ""
}

// Message returns the immutable rich message.
func (v EventInput) Message() Message { return v.message }

// Extensions returns an independent ordered envelope-extension slice.
func (v EventInput) Extensions() []Extension { return append([]Extension(nil), v.extensions...) }

// Event exposes read-only runtime event data to modules.
type Event interface {
	// ID returns the kernel-assigned runtime identifier.
	ID() EventID
	// Platform returns the source platform.
	Platform() PlatformID
	// Account returns the source platform account.
	Account() AccountID
	// Tenant returns the isolation tenant.
	Tenant() TenantID
	// Conversation returns the source conversation.
	Conversation() ConversationID
	// SourceEventID returns the provider event identifier when supplied.
	SourceEventID() (SourceEventID, bool)
	// Actor returns the originating actor when supplied.
	Actor() (ActorID, bool)
	// Action returns the lifecycle action.
	Action() EventAction
	// Direction returns protocol-defined event flow.
	Direction() Direction
	// OccurredAt returns the canonical occurrence time.
	OccurredAt() time.Time
	// IngestedAt returns the canonical ingestion time.
	IngestedAt() time.Time
	// EditedAt returns the canonical edit time when supplied.
	EditedAt() (time.Time, bool)
	// TraceID returns trace context when supplied.
	TraceID() (TraceID, bool)
	// CausalEventID returns the causing runtime event when supplied.
	CausalEventID() (CausalEventID, bool)
	// Completeness returns content availability.
	Completeness() Completeness
	// ProviderLocator returns adapter-owned source identity when supplied.
	ProviderLocator() (ProviderLocator, bool)
	// Message returns immutable rich content.
	Message() Message
	// Extensions returns an independent ordered envelope-extension slice.
	Extensions() []Extension
}
