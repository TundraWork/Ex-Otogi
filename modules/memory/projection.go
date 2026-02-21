package memory

import (
	"fmt"

	"ex-otogi/pkg/otogi"
)

func (m *Module) rememberCreated(event *otogi.Event) error {
	lookup, err := otogi.MemoryLookupFromEvent(event)
	if err != nil {
		return fmt.Errorf("remember created: %w", err)
	}

	now := m.now()
	occurredAt := normalizeEventTime(event.OccurredAt, now)
	updatedAt := mutationChangedAtOrFallback(event.Mutation, occurredAt)
	platform := event.Source.Platform
	if platform == "" {
		platform = event.Platform
	}
	cached := memorySnapshot{
		TenantID:     event.TenantID,
		Platform:     platform,
		Conversation: event.Conversation,
		Actor:        event.Actor,
		Article:      cloneArticle(*event.Article),
		CreatedAt:    occurredAt,
		UpdatedAt:    updatedAt,
	}

	key := cacheKeyFromLookup(lookup)

	m.mu.Lock()
	m.ensureNotExpiredLocked(key, now)
	if existing, exists := m.entities[key]; exists {
		if !existing.CreatedAt.IsZero() {
			cached.CreatedAt = existing.CreatedAt
		}
		if len(cached.Article.Reactions) == 0 && len(existing.Article.Reactions) > 0 {
			cached.Article.Reactions = cloneArticleReactions(existing.Article.Reactions)
		}
		if !existing.UpdatedAt.IsZero() && cached.UpdatedAt.Before(existing.UpdatedAt) {
			cached.UpdatedAt = existing.UpdatedAt
		}
	}
	if len(cached.Article.Reactions) == 0 {
		if history, exists := m.events[key]; exists {
			applyReactionHistoryToArticle(&cached.Article, history, cached.Article.ID)
		}
	}
	if event.Reaction != nil && event.Reaction.ArticleID == cached.Article.ID {
		applyReactionToArticle(&cached.Article, *event.Reaction)
	}
	if cached.UpdatedAt.IsZero() {
		cached.UpdatedAt = cached.CreatedAt
	}
	m.upsertEntityLocked(key, cached, now)
	m.mu.Unlock()

	return nil
}

func (m *Module) rememberEdit(event *otogi.Event) error {
	if event == nil {
		return fmt.Errorf("remember edit: nil event")
	}
	lookup, err := otogi.MutationMemoryLookupFromEvent(event)
	if err != nil {
		return fmt.Errorf("remember edit: %w", err)
	}

	now := m.now()
	occurredAt := normalizeEventTime(event.OccurredAt, now)
	updatedAt := mutationChangedAtOrFallback(event.Mutation, occurredAt)
	key := cacheKeyFromLookup(lookup)

	m.mu.Lock()
	m.ensureNotExpiredLocked(key, now)
	cached := memorySnapshot{
		TenantID:     lookup.TenantID,
		Platform:     lookup.Platform,
		Conversation: event.Conversation,
		Actor:        event.Actor,
		Article: otogi.Article{
			ID: lookup.ArticleID,
		},
		CreatedAt: occurredAt,
		UpdatedAt: occurredAt,
	}
	if existing, exists := m.entities[key]; exists {
		cached = cloneMemorySnapshot(existing)
	}
	if len(cached.Article.Reactions) == 0 {
		if history, exists := m.events[key]; exists {
			applyReactionHistoryToArticle(&cached.Article, history, lookup.ArticleID)
		}
	}
	if cached.Conversation.ID == "" {
		cached.Conversation = event.Conversation
	}
	if cached.Platform == "" {
		cached.Platform = event.Platform
		if event.Source.Platform != "" {
			cached.Platform = event.Source.Platform
		}
	}
	if cached.Article.ID == "" {
		cached.Article.ID = lookup.ArticleID
	}
	if event.Mutation.After != nil {
		cached.Article.Text = event.Mutation.After.Text
		cached.Article.Entities = append([]otogi.TextEntity(nil), event.Mutation.After.Entities...)
		cached.Article.Media = cloneMediaAttachments(event.Mutation.After.Media)
	}
	cached.UpdatedAt = updatedAt
	if cached.CreatedAt.IsZero() {
		cached.CreatedAt = occurredAt
	}
	m.upsertEntityLocked(key, cached, now)
	m.mu.Unlock()

	return nil
}

func (m *Module) rememberReaction(event *otogi.Event) error {
	if event == nil {
		return fmt.Errorf("remember reaction: nil event")
	}
	lookup, err := otogi.ReactionMemoryLookupFromEvent(event)
	if err != nil {
		return fmt.Errorf("remember reaction: %w", err)
	}

	now := m.now()
	key := cacheKeyFromLookup(lookup)

	m.mu.Lock()
	m.ensureNotExpiredLocked(key, now)
	existing, exists := m.entities[key]
	if !exists {
		m.mu.Unlock()
		return nil
	}

	cached := cloneMemorySnapshot(existing)
	applyReactionToArticle(&cached.Article, *event.Reaction)
	m.upsertEntityLocked(key, cached, now)
	m.mu.Unlock()

	return nil
}

func (m *Module) forgetRetracted(event *otogi.Event) error {
	if event == nil {
		return fmt.Errorf("forget retracted: nil event")
	}
	lookup, err := otogi.MutationMemoryLookupFromEvent(event)
	if err != nil {
		return fmt.Errorf("forget retracted: %w", err)
	}

	now := m.now()
	key := cacheKeyFromLookup(lookup)

	m.mu.Lock()
	m.ensureNotExpiredLocked(key, now)
	delete(m.entities, key)
	m.upsertKeyLocked(key, now)
	m.trimToCapacityLocked()
	m.mu.Unlock()

	return nil
}

func (m *Module) appendEvent(event *otogi.Event) error {
	if event == nil {
		return fmt.Errorf("append event: nil event")
	}
	lookup, err := otogi.TargetMemoryLookupFromEvent(event)
	if err != nil {
		return fmt.Errorf("append event: %w", err)
	}

	now := m.now()
	key := cacheKeyFromLookup(lookup)

	m.mu.Lock()
	m.upsertEventLocked(key, event, now)
	m.mu.Unlock()

	return nil
}
