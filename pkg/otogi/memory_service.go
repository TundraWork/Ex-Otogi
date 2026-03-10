package otogi

import (
	"context"
	"fmt"
	"time"
)

// ServiceMemory is the canonical service registry key for memory lookups.
const ServiceMemory = "otogi.memory"

// MemoryService provides read access to projected article state and its
// captured event history.
//
// Implementations must be concurrency-safe because handlers can resolve memory
// entries from multiple workers at the same time.
type MemoryService interface {
	// Get returns memory for one lookup key.
	//
	// When no entry exists, found is false and err is nil.
	Get(ctx context.Context, lookup MemoryLookup) (memory Memory, found bool, err error)
	// GetReplied resolves and returns memory for event.Article.ReplyToArticleID.
	//
	// When the event has no reply target or the memory has no entry, found is
	// false and err is nil.
	GetReplied(ctx context.Context, event *Event) (memory Memory, found bool, err error)
	// GetReplyChain resolves one reply chain for the event article.
	//
	// The returned chain is ordered oldest -> newest and always includes the
	// current inbound event article as the last entry when event is valid.
	GetReplyChain(ctx context.Context, event *Event) ([]ReplyChainEntry, error)
	// ListConversationContextBefore resolves articles immediately preceding one
	// anchor position within the same conversation scope.
	//
	// The returned slice is ordered oldest -> newest. Implementations should use
	// AnchorArticleID when it can be resolved and fall back to AnchorOccurredAt
	// otherwise. ExcludeArticleIDs must never appear in the result.
	ListConversationContextBefore(
		ctx context.Context,
		query ConversationContextBeforeQuery,
	) ([]ConversationContextEntry, error)
}

// MemoryLookup identifies one memory entry in a conversation scope.
type MemoryLookup struct {
	// TenantID scopes lookup for multi-tenant deployments.
	TenantID string
	// Platform identifies which upstream platform produced this memory entry.
	Platform Platform
	// ConversationID identifies the conversation containing the article.
	ConversationID string
	// ArticleID identifies the tracked article.
	ArticleID string
}

// Validate checks that mandatory lookup fields are present.
func (l MemoryLookup) Validate() error {
	if l.Platform == "" {
		return fmt.Errorf("validate memory lookup: missing platform")
	}
	if l.ConversationID == "" {
		return fmt.Errorf("validate memory lookup: missing conversation id")
	}
	if l.ArticleID == "" {
		return fmt.Errorf("validate memory lookup: missing article id")
	}

	return nil
}

// MemoryLookupFromEvent creates a lookup key for the event's primary article payload.
func MemoryLookupFromEvent(event *Event) (MemoryLookup, error) {
	if event == nil {
		return MemoryLookup{}, fmt.Errorf("memory lookup from event: nil event")
	}
	if event.Article == nil {
		return MemoryLookup{}, fmt.Errorf("memory lookup from event %s: missing article payload", event.Kind)
	}
	if event.Article.ID == "" {
		return MemoryLookup{}, fmt.Errorf("memory lookup from event %s: missing article id", event.Kind)
	}

	lookup, err := memoryLookupWithID(event, "memory lookup from event", event.Article.ID)
	if err != nil {
		return MemoryLookup{}, err
	}

	return lookup, nil
}

// ReplyMemoryLookupFromEvent creates a lookup key for event.Article.ReplyToArticleID.
func ReplyMemoryLookupFromEvent(event *Event) (MemoryLookup, error) {
	if event == nil {
		return MemoryLookup{}, fmt.Errorf("reply memory lookup from event: nil event")
	}
	if event.Article == nil {
		return MemoryLookup{}, fmt.Errorf("reply memory lookup from event %s: missing article payload", event.Kind)
	}
	if event.Article.ReplyToArticleID == "" {
		return MemoryLookup{}, fmt.Errorf("reply memory lookup from event %s: missing reply_to_article_id", event.Kind)
	}

	lookup, err := memoryLookupWithID(event, "reply memory lookup from event", event.Article.ReplyToArticleID)
	if err != nil {
		return MemoryLookup{}, err
	}

	return lookup, nil
}

// MutationMemoryLookupFromEvent creates a lookup key for mutation target article.
func MutationMemoryLookupFromEvent(event *Event) (MemoryLookup, error) {
	if event == nil {
		return MemoryLookup{}, fmt.Errorf("mutation memory lookup from event: nil event")
	}
	if event.Mutation == nil {
		return MemoryLookup{}, fmt.Errorf("mutation memory lookup from event %s: missing mutation payload", event.Kind)
	}
	if event.Mutation.TargetArticleID == "" {
		return MemoryLookup{}, fmt.Errorf("mutation memory lookup from event %s: missing target article id", event.Kind)
	}

	lookup, err := memoryLookupWithID(event, "mutation memory lookup from event", event.Mutation.TargetArticleID)
	if err != nil {
		return MemoryLookup{}, err
	}

	return lookup, nil
}

// ReactionMemoryLookupFromEvent creates a lookup key for reaction target article.
func ReactionMemoryLookupFromEvent(event *Event) (MemoryLookup, error) {
	if event == nil {
		return MemoryLookup{}, fmt.Errorf("reaction memory lookup from event: nil event")
	}
	if event.Reaction == nil {
		return MemoryLookup{}, fmt.Errorf("reaction memory lookup from event %s: missing reaction payload", event.Kind)
	}
	if event.Reaction.ArticleID == "" {
		return MemoryLookup{}, fmt.Errorf("reaction memory lookup from event %s: missing reaction article id", event.Kind)
	}

	lookup, err := memoryLookupWithID(event, "reaction memory lookup from event", event.Reaction.ArticleID)
	if err != nil {
		return MemoryLookup{}, err
	}

	return lookup, nil
}

// TargetMemoryLookupFromEvent creates a lookup key for the event's target article by kind.
func TargetMemoryLookupFromEvent(event *Event) (MemoryLookup, error) {
	if event == nil {
		return MemoryLookup{}, fmt.Errorf("target memory lookup from event: nil event")
	}

	switch event.Kind {
	case EventKindArticleCreated:
		return MemoryLookupFromEvent(event)
	case EventKindArticleEdited, EventKindArticleRetracted:
		return MutationMemoryLookupFromEvent(event)
	case EventKindArticleReactionAdded, EventKindArticleReactionRemoved:
		return ReactionMemoryLookupFromEvent(event)
	default:
		return MemoryLookup{}, fmt.Errorf("target memory lookup from event: unsupported kind %s", event.Kind)
	}
}

func memoryLookupWithID(event *Event, operation string, articleID string) (MemoryLookup, error) {
	if event == nil {
		return MemoryLookup{}, fmt.Errorf("%s: nil event", operation)
	}
	if articleID == "" {
		return MemoryLookup{}, fmt.Errorf("%s %s: missing article id", operation, event.Kind)
	}

	lookup := MemoryLookup{
		TenantID:       event.TenantID,
		Platform:       event.Source.Platform,
		ConversationID: event.Conversation.ID,
		ArticleID:      articleID,
	}
	if err := lookup.Validate(); err != nil {
		return MemoryLookup{}, fmt.Errorf("%s %s: %w", operation, event.Kind, err)
	}

	return lookup, nil
}

// Memory is an immutable memory entry for one article key at the current projection state.
type Memory struct {
	// TenantID scopes this memory entry for multi-tenant deployments.
	TenantID string
	// Platform identifies the source platform.
	Platform Platform
	// Conversation identifies where the article was sent.
	Conversation Conversation
	// Actor identifies who authored the article when known.
	Actor Actor
	// Article stores the current projected article payload.
	Article Article
	// History stores the projected article event history in append order.
	History []Event
	// CreatedAt records when this article was first observed.
	CreatedAt time.Time
	// UpdatedAt records when this memory entry was last updated.
	UpdatedAt time.Time
}

// ReplyChainEntry is one immutable entry in a resolved reply chain.
type ReplyChainEntry struct {
	// Conversation identifies where this article was sent.
	Conversation Conversation
	// Actor identifies who authored this article when known.
	Actor Actor
	// Article stores the projected article payload for this chain entry.
	Article Article
	// CreatedAt records when this article was first observed.
	CreatedAt time.Time
	// UpdatedAt records when this article projection was last updated.
	UpdatedAt time.Time
	// IsCurrent reports whether this entry is the current inbound event article.
	IsCurrent bool
}

// ConversationContextBeforeQuery identifies one anchored conversation window
// lookup that returns earlier articles only.
type ConversationContextBeforeQuery struct {
	// TenantID scopes lookup for multi-tenant deployments.
	TenantID string
	// Platform identifies which upstream platform produced this memory entry.
	Platform Platform
	// ConversationID identifies the conversation containing the anchor.
	ConversationID string
	// ThreadID optionally narrows the lookup to one thread/topic within the
	// conversation.
	ThreadID string
	// AnchorArticleID identifies the article before which context should be
	// returned when that article is already known to memory.
	AnchorArticleID string
	// AnchorOccurredAt provides a time anchor used when AnchorArticleID is
	// unavailable or unresolved.
	AnchorOccurredAt time.Time
	// BeforeLimit caps how many earlier articles can be returned.
	BeforeLimit int
	// ExcludeArticleIDs lists article ids that must not appear in the returned
	// context window.
	ExcludeArticleIDs []string
}

// Validate checks that mandatory conversation context lookup fields are present.
func (q ConversationContextBeforeQuery) Validate() error {
	if q.Platform == "" {
		return fmt.Errorf("validate conversation context before query: missing platform")
	}
	if q.ConversationID == "" {
		return fmt.Errorf("validate conversation context before query: missing conversation id")
	}
	if q.AnchorArticleID == "" && q.AnchorOccurredAt.IsZero() {
		return fmt.Errorf("validate conversation context before query: missing anchor article id or anchor occurred at")
	}
	if q.BeforeLimit < 0 {
		return fmt.Errorf("validate conversation context before query: before_limit must be >= 0")
	}

	return nil
}

// ConversationContextEntry is one immutable article entry returned from one
// anchored conversation context lookup.
type ConversationContextEntry struct {
	// Conversation identifies where this article was sent.
	Conversation Conversation
	// Actor identifies who authored this article when known.
	Actor Actor
	// Article stores the projected article payload for this context entry.
	Article Article
	// CreatedAt records when this article was first observed.
	CreatedAt time.Time
	// UpdatedAt records when this article projection was last updated.
	UpdatedAt time.Time
}
