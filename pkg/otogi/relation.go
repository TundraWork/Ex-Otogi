package otogi

import (
	"fmt"
	"unicode/utf8"
)

// EventRefParams supplies provider identity for a relation target.
type EventRefParams struct {
	// Platform identifies the target platform.
	Platform PlatformID
	// Account identifies the target platform account.
	Account AccountID
	// Conversation identifies the target conversation.
	Conversation ConversationID
	// SourceEvent identifies the target provider event.
	SourceEvent SourceEventID
	// Locator optionally carries provider-owned refetch identity.
	Locator ProviderLocator
}

// EventRef is an immutable provider event reference.
type EventRef struct {
	platform     PlatformID
	account      AccountID
	conversation ConversationID
	sourceEvent  SourceEventID
	locator      ProviderLocator
}

// NewEventRef validates the complete routable provider identity.
func NewEventRef(p EventRefParams) (EventRef, error) {
	if p.Platform.value == "" || p.Account.value == "" || p.Conversation.value == "" || p.SourceEvent.value == "" {
		return EventRef{}, fmt.Errorf("event reference: %w", ErrInvalid)
	}
	return EventRef{platform: p.Platform, account: p.Account, conversation: p.Conversation, sourceEvent: p.SourceEvent, locator: p.Locator}, nil
}

// Platform returns the target platform.
func (v EventRef) Platform() PlatformID { return v.platform }

// Account returns the target platform account.
func (v EventRef) Account() AccountID { return v.account }

// Conversation returns the target conversation.
func (v EventRef) Conversation() ConversationID { return v.conversation }

// SourceEvent returns the provider source event identifier.
func (v EventRef) SourceEvent() SourceEventID { return v.sourceEvent }

// Locator returns the optional provider locator.
func (v EventRef) Locator() ProviderLocator { return v.locator }

// HasLocator reports whether a provider locator is present.
func (v EventRef) HasLocator() bool { return v.locator.namespaceURI != "" }

// RelationKind identifies portable event relation semantics.
type RelationKind string

const (
	// RelationReply relates a reply to its target.
	RelationReply RelationKind = "reply"
	// RelationThread relates an event to its thread.
	RelationThread RelationKind = "thread"
	// RelationReplacement relates an edit to the replaced event.
	RelationReplacement RelationKind = "replacement"
	// RelationAnnotation relates a reaction or annotation to its target.
	RelationAnnotation RelationKind = "annotation"
	// RelationReference records a general reference.
	RelationReference RelationKind = "reference"
	// RelationForward records a crosspost or forward source.
	RelationForward RelationKind = "forward"
)

// Relation is an immutable ordered message relation.
type Relation struct {
	kind       RelationKind
	target     EventRef
	key        string
	extensions []Extension
}

// NewRelation validates a relation and owns its extensions.
func NewRelation(kind RelationKind, target EventRef, key string, extensions []Extension) (Relation, error) {
	if len(extensions) > 64 {
		return Relation{}, fmt.Errorf("relation extensions: %w", ErrCapacity)
	}
	if !validRelationKind(kind) || target.sourceEvent.value == "" || !utf8.ValidString(key) {
		return Relation{}, fmt.Errorf("relation: %w", ErrInvalid)
	}
	return Relation{kind: kind, target: target, key: key, extensions: cloneExtensions(extensions)}, nil
}

// Kind returns the relation semantics.
func (v Relation) Kind() RelationKind { return v.kind }

// Target returns the target provider event reference.
func (v Relation) Target() EventRef { return v.target }

// Key returns the optional relation key.
func (v Relation) Key() string { return v.key }

// Extensions returns ordered relation extensions.
func (v Relation) Extensions() []Extension { return cloneExtensions(v.extensions) }

func validRelationKind(kind RelationKind) bool {
	switch kind {
	case RelationReply, RelationThread, RelationReplacement, RelationAnnotation, RelationReference, RelationForward:
		return true
	default:
		return false
	}
}
