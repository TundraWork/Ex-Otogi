package otogi

import (
	"context"
	"fmt"
	"time"
)

// ServiceEventCache is the canonical service registry key for message event cache lookup.
const ServiceEventCache = "otogi.event_cache"

// EventCache provides read access to message entity projections and event history
// remembered from inbound events.
//
// Implementations must be concurrency-safe because handlers can resolve cache entries
// from multiple workers at the same time.
type EventCache interface {
	// GetMessage returns a cached message snapshot for the provided lookup key.
	//
	// When no entry exists, found is false and err is nil.
	GetMessage(ctx context.Context, lookup MessageLookup) (cached CachedMessage, found bool, err error)
	// GetRepliedMessage resolves the message referenced by event.Message.ReplyToID.
	//
	// When the event has no reply target or the cache has no entry, found is false and err is nil.
	GetRepliedMessage(ctx context.Context, event *Event) (cached CachedMessage, found bool, err error)
	// GetEvents returns the event history captured for one message lookup key.
	//
	// When no entry exists, found is false and err is nil.
	GetEvents(ctx context.Context, lookup MessageLookup) (events []Event, found bool, err error)
	// GetRepliedEvents resolves and returns event history for event.Message.ReplyToID.
	//
	// When the event has no reply target or no entry exists, found is false and err is nil.
	GetRepliedEvents(ctx context.Context, event *Event) (events []Event, found bool, err error)
}

// MessageLookup identifies one cached message in a conversation scope.
type MessageLookup struct {
	// TenantID scopes lookup for multi-tenant deployments.
	TenantID string
	// Platform identifies which upstream platform produced the cached message.
	Platform Platform
	// ConversationID identifies the conversation containing the message.
	ConversationID string
	// MessageID identifies the cached message.
	MessageID string
}

// Validate checks that mandatory lookup fields are present.
func (l MessageLookup) Validate() error {
	if l.Platform == "" {
		return fmt.Errorf("validate message lookup: missing platform")
	}
	if l.ConversationID == "" {
		return fmt.Errorf("validate message lookup: missing conversation id")
	}
	if l.MessageID == "" {
		return fmt.Errorf("validate message lookup: missing message id")
	}

	return nil
}

// MessageLookupFromEvent creates a lookup key for the event's primary message payload.
func MessageLookupFromEvent(event *Event) (MessageLookup, error) {
	if event == nil {
		return MessageLookup{}, fmt.Errorf("message lookup from event: nil event")
	}
	if event.Message == nil {
		return MessageLookup{}, fmt.Errorf("message lookup from event %s: missing message payload", event.Kind)
	}
	if event.Message.ID == "" {
		return MessageLookup{}, fmt.Errorf("message lookup from event %s: missing message id", event.Kind)
	}

	lookup := MessageLookup{
		TenantID:       event.TenantID,
		Platform:       event.Platform,
		ConversationID: event.Conversation.ID,
		MessageID:      event.Message.ID,
	}
	if err := lookup.Validate(); err != nil {
		return MessageLookup{}, fmt.Errorf("message lookup from event %s: %w", event.Kind, err)
	}

	return lookup, nil
}

// ReplyLookupFromEvent creates a lookup key for the message referenced by ReplyToID.
func ReplyLookupFromEvent(event *Event) (MessageLookup, error) {
	if event == nil {
		return MessageLookup{}, fmt.Errorf("reply lookup from event: nil event")
	}
	if event.Message == nil {
		return MessageLookup{}, fmt.Errorf("reply lookup from event %s: missing message payload", event.Kind)
	}
	if event.Message.ReplyToID == "" {
		return MessageLookup{}, fmt.Errorf("reply lookup from event %s: missing reply_to_id", event.Kind)
	}

	lookup := MessageLookup{
		TenantID:       event.TenantID,
		Platform:       event.Platform,
		ConversationID: event.Conversation.ID,
		MessageID:      event.Message.ReplyToID,
	}
	if err := lookup.Validate(); err != nil {
		return MessageLookup{}, fmt.Errorf("reply lookup from event %s: %w", event.Kind, err)
	}

	return lookup, nil
}

// CachedMessage is an immutable snapshot returned by EventCache lookups.
type CachedMessage struct {
	// TenantID scopes this message snapshot for multi-tenant deployments.
	TenantID string
	// Platform identifies the source platform.
	Platform Platform
	// Conversation identifies where the message was sent.
	Conversation Conversation
	// Actor identifies who authored the message when known.
	Actor Actor
	// Message stores the normalized message payload.
	Message Message
	// CreatedAt records when this message was first observed.
	CreatedAt time.Time
	// UpdatedAt records when this message snapshot was last updated.
	UpdatedAt time.Time
}
