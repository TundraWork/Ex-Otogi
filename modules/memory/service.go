package memory

import (
	"context"
	"fmt"

	"ex-otogi/pkg/otogi"
)

// Get returns one memory entry for the provided lookup key.
func (m *Module) Get(ctx context.Context, lookup otogi.MemoryLookup) (otogi.Memory, bool, error) {
	if err := ctx.Err(); err != nil {
		return otogi.Memory{}, false, fmt.Errorf("memory get: %w", err)
	}
	if err := lookup.Validate(); err != nil {
		return otogi.Memory{}, false, fmt.Errorf("memory get: %w", err)
	}

	cached, found, err := m.getSnapshot(ctx, lookup)
	if err != nil {
		return otogi.Memory{}, false, fmt.Errorf("memory get snapshot: %w", err)
	}
	if !found {
		return otogi.Memory{}, false, nil
	}

	history, _, err := m.getHistory(ctx, lookup)
	if err != nil {
		return otogi.Memory{}, false, fmt.Errorf("memory get history: %w", err)
	}

	return otogi.Memory{
		TenantID:     cached.TenantID,
		Platform:     cached.Platform,
		Conversation: cached.Conversation,
		Actor:        cached.Actor,
		Article:      cloneArticle(cached.Article),
		History:      history,
		CreatedAt:    cached.CreatedAt,
		UpdatedAt:    cached.UpdatedAt,
	}, true, nil
}

// GetReplied resolves and returns memory for event.Article.ReplyToArticleID.
func (m *Module) GetReplied(ctx context.Context, event *otogi.Event) (otogi.Memory, bool, error) {
	if err := ctx.Err(); err != nil {
		return otogi.Memory{}, false, fmt.Errorf("memory get replied: %w", err)
	}
	if event == nil {
		return otogi.Memory{}, false, fmt.Errorf("memory get replied: nil event")
	}
	if event.Article == nil || event.Article.ReplyToArticleID == "" {
		return otogi.Memory{}, false, nil
	}

	lookup, err := otogi.ReplyMemoryLookupFromEvent(event)
	if err != nil {
		return otogi.Memory{}, false, fmt.Errorf("memory get replied: %w", err)
	}

	return m.Get(ctx, lookup)
}

func (m *Module) getSnapshot(ctx context.Context, lookup otogi.MemoryLookup) (memorySnapshot, bool, error) {
	if err := ctx.Err(); err != nil {
		return memorySnapshot{}, false, fmt.Errorf("memory get snapshot: %w", err)
	}
	if err := lookup.Validate(); err != nil {
		return memorySnapshot{}, false, fmt.Errorf("memory get snapshot: %w", err)
	}

	now := m.now()
	key := cacheKeyFromLookup(lookup)

	m.mu.Lock()
	if !m.ensureNotExpiredLocked(key, now) {
		m.mu.Unlock()
		return memorySnapshot{}, false, nil
	}
	m.touchLocked(key)
	cached, exists := m.entities[key]
	if !exists {
		m.mu.Unlock()
		return memorySnapshot{}, false, nil
	}
	cloned := cloneMemorySnapshot(cached)
	m.mu.Unlock()

	return cloned, true, nil
}

func (m *Module) getHistory(ctx context.Context, lookup otogi.MemoryLookup) ([]otogi.Event, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, fmt.Errorf("memory get history: %w", err)
	}
	if err := lookup.Validate(); err != nil {
		return nil, false, fmt.Errorf("memory get history: %w", err)
	}

	now := m.now()
	key := cacheKeyFromLookup(lookup)

	m.mu.Lock()
	if !m.ensureNotExpiredLocked(key, now) {
		m.mu.Unlock()
		return nil, false, nil
	}
	m.touchLocked(key)
	history, exists := m.events[key]
	if !exists {
		m.mu.Unlock()
		return nil, false, nil
	}
	cloned := cloneEventStream(history)
	m.mu.Unlock()

	return cloned, true, nil
}

func (m *Module) getRepliedHistory(ctx context.Context, event *otogi.Event) ([]otogi.Event, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, fmt.Errorf("memory get replied history: %w", err)
	}
	if event == nil {
		return nil, false, fmt.Errorf("memory get replied history: nil event")
	}
	if event.Article == nil || event.Article.ReplyToArticleID == "" {
		return nil, false, nil
	}

	lookup, err := otogi.ReplyMemoryLookupFromEvent(event)
	if err != nil {
		return nil, false, fmt.Errorf("memory get replied history: %w", err)
	}

	return m.getHistory(ctx, lookup)
}
