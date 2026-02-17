package otogi

import (
	"context"
	"fmt"
	"time"
)

// ServiceArticleStore is the canonical service registry key for article cache lookup.
const ServiceArticleStore = "otogi.article_store"

// ArticleStore provides read access to article projections and event history
// remembered from inbound events.
//
// Implementations must be concurrency-safe because handlers can resolve cache entries
// from multiple workers at the same time.
type ArticleStore interface {
	// GetArticle returns a cached article snapshot for the provided lookup key.
	//
	// When no entry exists, found is false and err is nil.
	GetArticle(ctx context.Context, lookup ArticleLookup) (cached CachedArticle, found bool, err error)
	// GetRepliedArticle resolves the article referenced by event.Article.ReplyToArticleID.
	//
	// When the event has no reply target or the cache has no entry, found is false and err is nil.
	GetRepliedArticle(ctx context.Context, event *Event) (cached CachedArticle, found bool, err error)
	// GetEvents returns the event history captured for one article lookup key.
	//
	// When no entry exists, found is false and err is nil.
	GetEvents(ctx context.Context, lookup ArticleLookup) (events []Event, found bool, err error)
	// GetRepliedEvents resolves and returns event history for event.Article.ReplyToArticleID.
	//
	// When the event has no reply target or no entry exists, found is false and err is nil.
	GetRepliedEvents(ctx context.Context, event *Event) (events []Event, found bool, err error)
}

// ArticleLookup identifies one cached article in a conversation scope.
type ArticleLookup struct {
	// TenantID scopes lookup for multi-tenant deployments.
	TenantID string
	// Platform identifies which upstream platform produced the cached article.
	Platform Platform
	// ConversationID identifies the conversation containing the article.
	ConversationID string
	// ArticleID identifies the cached article.
	ArticleID string
}

// Validate checks that mandatory lookup fields are present.
func (l ArticleLookup) Validate() error {
	if l.Platform == "" {
		return fmt.Errorf("validate article lookup: missing platform")
	}
	if l.ConversationID == "" {
		return fmt.Errorf("validate article lookup: missing conversation id")
	}
	if l.ArticleID == "" {
		return fmt.Errorf("validate article lookup: missing article id")
	}

	return nil
}

// ArticleLookupFromEvent creates a lookup key for the event's primary article payload.
func ArticleLookupFromEvent(event *Event) (ArticleLookup, error) {
	if event == nil {
		return ArticleLookup{}, fmt.Errorf("article lookup from event: nil event")
	}
	if event.Article == nil {
		return ArticleLookup{}, fmt.Errorf("article lookup from event %s: missing article payload", event.Kind)
	}
	if event.Article.ID == "" {
		return ArticleLookup{}, fmt.Errorf("article lookup from event %s: missing article id", event.Kind)
	}

	lookup, err := articleLookupWithID(event, "article lookup from event", event.Article.ID)
	if err != nil {
		return ArticleLookup{}, err
	}

	return lookup, nil
}

// ReplyArticleLookupFromEvent creates a lookup key for the article referenced by ReplyToArticleID.
func ReplyArticleLookupFromEvent(event *Event) (ArticleLookup, error) {
	if event == nil {
		return ArticleLookup{}, fmt.Errorf("reply article lookup from event: nil event")
	}
	if event.Article == nil {
		return ArticleLookup{}, fmt.Errorf("reply article lookup from event %s: missing article payload", event.Kind)
	}
	if event.Article.ReplyToArticleID == "" {
		return ArticleLookup{}, fmt.Errorf("reply article lookup from event %s: missing reply_to_article_id", event.Kind)
	}

	lookup, err := articleLookupWithID(event, "reply article lookup from event", event.Article.ReplyToArticleID)
	if err != nil {
		return ArticleLookup{}, err
	}

	return lookup, nil
}

// MutationArticleLookupFromEvent creates a lookup key for the article targeted by mutation payload.
func MutationArticleLookupFromEvent(event *Event) (ArticleLookup, error) {
	if event == nil {
		return ArticleLookup{}, fmt.Errorf("mutation article lookup from event: nil event")
	}
	if event.Mutation == nil {
		return ArticleLookup{}, fmt.Errorf("mutation article lookup from event %s: missing mutation payload", event.Kind)
	}
	if event.Mutation.TargetArticleID == "" {
		return ArticleLookup{}, fmt.Errorf("mutation article lookup from event %s: missing target article id", event.Kind)
	}

	lookup, err := articleLookupWithID(event, "mutation article lookup from event", event.Mutation.TargetArticleID)
	if err != nil {
		return ArticleLookup{}, err
	}

	return lookup, nil
}

// ReactionArticleLookupFromEvent creates a lookup key for the article targeted by reaction payload.
func ReactionArticleLookupFromEvent(event *Event) (ArticleLookup, error) {
	if event == nil {
		return ArticleLookup{}, fmt.Errorf("reaction article lookup from event: nil event")
	}
	if event.Reaction == nil {
		return ArticleLookup{}, fmt.Errorf("reaction article lookup from event %s: missing reaction payload", event.Kind)
	}
	if event.Reaction.ArticleID == "" {
		return ArticleLookup{}, fmt.Errorf("reaction article lookup from event %s: missing reaction article id", event.Kind)
	}

	lookup, err := articleLookupWithID(event, "reaction article lookup from event", event.Reaction.ArticleID)
	if err != nil {
		return ArticleLookup{}, err
	}

	return lookup, nil
}

// TargetArticleLookupFromEvent creates a lookup key for the event's target article by kind.
func TargetArticleLookupFromEvent(event *Event) (ArticleLookup, error) {
	if event == nil {
		return ArticleLookup{}, fmt.Errorf("target article lookup from event: nil event")
	}

	switch event.Kind {
	case EventKindArticleCreated:
		return ArticleLookupFromEvent(event)
	case EventKindArticleEdited, EventKindArticleRetracted:
		return MutationArticleLookupFromEvent(event)
	case EventKindArticleReactionAdded, EventKindArticleReactionRemoved:
		return ReactionArticleLookupFromEvent(event)
	default:
		return ArticleLookup{}, fmt.Errorf("target article lookup from event: unsupported kind %s", event.Kind)
	}
}

func articleLookupWithID(event *Event, operation string, articleID string) (ArticleLookup, error) {
	if event == nil {
		return ArticleLookup{}, fmt.Errorf("%s: nil event", operation)
	}
	if articleID == "" {
		return ArticleLookup{}, fmt.Errorf("%s %s: missing article id", operation, event.Kind)
	}

	lookup := ArticleLookup{
		TenantID:       event.TenantID,
		Platform:       event.Platform,
		ConversationID: event.Conversation.ID,
		ArticleID:      articleID,
	}
	if err := lookup.Validate(); err != nil {
		return ArticleLookup{}, fmt.Errorf("%s %s: %w", operation, event.Kind, err)
	}

	return lookup, nil
}

// CachedArticle is an immutable snapshot returned by ArticleStore lookups.
type CachedArticle struct {
	// TenantID scopes this article snapshot for multi-tenant deployments.
	TenantID string
	// Platform identifies the source platform.
	Platform Platform
	// Conversation identifies where the article was sent.
	Conversation Conversation
	// Actor identifies who authored the article when known.
	Actor Actor
	// Article stores the normalized article payload.
	Article Article
	// CreatedAt records when this article was first observed.
	CreatedAt time.Time
	// UpdatedAt records when this article snapshot was last updated.
	UpdatedAt time.Time
}
