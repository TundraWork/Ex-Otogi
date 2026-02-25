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

// GetReplyChain resolves one reply chain for the event article.
func (m *Module) GetReplyChain(ctx context.Context, event *otogi.Event) ([]otogi.ReplyChainEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("memory get reply chain: %w", err)
	}
	if event == nil {
		return nil, fmt.Errorf("memory get reply chain: nil event")
	}
	if event.Article == nil {
		return nil, fmt.Errorf("memory get reply chain: nil article")
	}
	if event.Article.ID == "" {
		return nil, fmt.Errorf("memory get reply chain: missing article id")
	}
	if event.Conversation.ID == "" {
		return nil, fmt.Errorf("memory get reply chain: missing conversation id")
	}

	platform := event.Source.Platform
	if platform == "" {
		platform = event.Platform
	}
	if platform == "" {
		return nil, fmt.Errorf("memory get reply chain: missing platform")
	}

	seen := map[string]struct{}{
		event.Article.ID: {},
	}
	parents := make([]otogi.ReplyChainEntry, 0)
	parentID := event.Article.ReplyToArticleID
	for parentID != "" {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("memory get reply chain: %w", err)
		}
		if _, exists := seen[parentID]; exists {
			return nil, fmt.Errorf("memory get reply chain: cycle detected at article id %s", parentID)
		}
		seen[parentID] = struct{}{}

		lookup := otogi.MemoryLookup{
			TenantID:       event.TenantID,
			Platform:       platform,
			ConversationID: event.Conversation.ID,
			ArticleID:      parentID,
		}
		parentSnapshot, found, err := m.getSnapshot(ctx, lookup)
		if err != nil {
			return nil, fmt.Errorf("memory get reply chain get snapshot for %s: %w", parentID, err)
		}
		if !found {
			break
		}

		parents = append(parents, otogi.ReplyChainEntry{
			Conversation: parentSnapshot.Conversation,
			Actor:        parentSnapshot.Actor,
			Article:      cloneArticle(parentSnapshot.Article),
			IsCurrent:    false,
		})
		parentID = parentSnapshot.Article.ReplyToArticleID
	}

	reverseReplyChainEntries(parents)
	chain := make([]otogi.ReplyChainEntry, 0, len(parents)+1)
	chain = append(chain, parents...)
	chain = append(chain, otogi.ReplyChainEntry{
		Conversation: event.Conversation,
		Actor:        event.Actor,
		Article:      cloneArticle(*event.Article),
		IsCurrent:    true,
	})

	return chain, nil
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

func reverseReplyChainEntries(entries []otogi.ReplyChainEntry) {
	for left, right := 0, len(entries)-1; left < right; left, right = left+1, right-1 {
		entries[left], entries[right] = entries[right], entries[left]
	}
}
