package memory

import (
	"context"
	"fmt"
	"time"

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

	now := m.now()
	key := cacheKeyFromLookup(lookup)

	m.mu.RLock()
	cached, history, found, expired := m.memoryReadLocked(now, key)
	m.mu.RUnlock()
	if expired {
		m.reconcileReadKeys(now, nil, []cacheKey{key})
		return otogi.Memory{}, false, nil
	}
	if !found {
		return otogi.Memory{}, false, nil
	}
	m.reconcileReadKeys(now, []cacheKey{key}, nil)

	return buildMemory(cached, history), true, nil
}

// GetBatch returns memory for all provided lookup keys that currently exist.
func (m *Module) GetBatch(
	ctx context.Context,
	lookups []otogi.MemoryLookup,
) (map[otogi.MemoryLookup]otogi.Memory, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("memory get batch: %w", err)
	}
	if len(lookups) == 0 {
		return map[otogi.MemoryLookup]otogi.Memory{}, nil
	}

	batch := make([]otogi.MemoryLookup, 0, len(lookups))
	seen := make(map[cacheKey]struct{}, len(lookups))
	for _, lookup := range lookups {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("memory get batch: %w", err)
		}
		if err := lookup.Validate(); err != nil {
			return nil, fmt.Errorf("memory get batch: %w", err)
		}

		key := cacheKeyFromLookup(lookup)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		batch = append(batch, lookup)
	}

	now := m.now()
	results := make(map[otogi.MemoryLookup]otogi.Memory, len(batch))
	touchedKeys := make([]cacheKey, 0, len(batch))
	expiredKeys := make([]cacheKey, 0)

	m.mu.RLock()
	for _, lookup := range batch {
		key := cacheKeyFromLookup(lookup)
		cached, history, found, expired := m.memoryReadLocked(now, key)
		if expired {
			expiredKeys = append(expiredKeys, key)
			continue
		}
		if !found {
			continue
		}

		results[lookup] = buildMemory(cached, history)
		touchedKeys = append(touchedKeys, key)
	}
	m.mu.RUnlock()

	m.reconcileReadKeys(now, touchedKeys, expiredKeys)

	return results, nil
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
			CreatedAt:    parentSnapshot.CreatedAt,
			UpdatedAt:    parentSnapshot.UpdatedAt,
			IsCurrent:    false,
		})
		parentID = parentSnapshot.Article.ReplyToArticleID
	}

	reverseReplyChainEntries(parents)
	occurredAt := normalizeEventTime(event.OccurredAt, m.now())
	chain := make([]otogi.ReplyChainEntry, 0, len(parents)+1)
	chain = append(chain, parents...)
	chain = append(chain, otogi.ReplyChainEntry{
		Conversation: event.Conversation,
		Actor:        event.Actor,
		Article:      cloneArticle(*event.Article),
		CreatedAt:    occurredAt,
		UpdatedAt:    mutationChangedAtOrFallback(event.Mutation, occurredAt),
		IsCurrent:    true,
	})

	return chain, nil
}

// ListConversationContextBefore resolves articles immediately preceding one
// anchor position within the same conversation scope.
func (m *Module) ListConversationContextBefore(
	ctx context.Context,
	query otogi.ConversationContextBeforeQuery,
) ([]otogi.ConversationContextEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("memory list conversation context before: %w", err)
	}
	if err := query.Validate(); err != nil {
		return nil, fmt.Errorf("memory list conversation context before: %w", err)
	}
	if query.BeforeLimit == 0 {
		return []otogi.ConversationContextEntry{}, nil
	}

	now := m.now()
	excluded := make(map[string]struct{}, len(query.ExcludeArticleIDs)+1)
	for _, articleID := range query.ExcludeArticleIDs {
		if articleID == "" {
			continue
		}
		excluded[articleID] = struct{}{}
	}

	m.mu.RLock()
	entries, touchedKeys, expiredKeys := m.listConversationContextEntriesLocked(now, query, excluded)
	m.mu.RUnlock()

	reverseConversationContextEntries(entries)
	m.reconcileReadKeys(now, touchedKeys, expiredKeys)

	return entries, nil
}

func (m *Module) listConversationContextEntriesLocked(
	now time.Time,
	query otogi.ConversationContextBeforeQuery,
	excluded map[string]struct{},
) ([]otogi.ConversationContextEntry, []cacheKey, []cacheKey) {
	if query.AnchorArticleID != "" {
		anchorKey := cacheKey{
			tenantID:       query.TenantID,
			platform:       query.Platform,
			conversationID: query.ConversationID,
			articleID:      query.AnchorArticleID,
		}
		if m.isRecordLiveLocked(anchorKey, now) {
			location, exists := m.articleStreams[anchorKey]
			if exists && (query.ThreadID == "" || location.streamKey.threadID == query.ThreadID) {
				streamEntries, exists := m.streams[location.streamKey]
				if exists {
					anchorIndex := locateConversationStreamEntry(streamEntries, conversationStreamEntry{
						key:       anchorKey,
						createdAt: location.createdAt,
						sequence:  location.sequence,
					})
					if anchorIndex >= 0 {
						return m.collectConversationContextEntriesLocked(
							now,
							streamEntries,
							anchorIndex-1,
							query,
							excluded,
						)
					}
				}
			}
		}
	}

	if query.AnchorOccurredAt.IsZero() {
		return nil, nil, nil
	}

	streamKey := conversationStreamKey{
		tenantID:       query.TenantID,
		platform:       query.Platform,
		conversationID: query.ConversationID,
		threadID:       query.ThreadID,
	}
	streamEntries, exists := m.streams[streamKey]
	if !exists {
		return nil, nil, nil
	}

	entries := make([]otogi.ConversationContextEntry, 0, minInt(query.BeforeLimit, len(streamEntries)))
	touchedKeys := make([]cacheKey, 0, minInt(query.BeforeLimit, len(streamEntries)))
	expiredKeys := make([]cacheKey, 0)
	for index := len(streamEntries) - 1; index >= 0 && len(entries) < query.BeforeLimit; index-- {
		entry := streamEntries[index]
		if entry.createdAt.After(query.AnchorOccurredAt.UTC()) {
			continue
		}
		if entry.key.articleID == query.AnchorArticleID {
			continue
		}
		if _, skip := excluded[entry.key.articleID]; skip {
			continue
		}

		snapshot, found, expired := m.snapshotReadLocked(now, entry.key)
		if expired {
			expiredKeys = append(expiredKeys, entry.key)
			continue
		}
		if !found {
			continue
		}

		entries = append(entries, otogi.ConversationContextEntry{
			Conversation: snapshot.Conversation,
			Actor:        snapshot.Actor,
			Article:      cloneArticle(snapshot.Article),
			CreatedAt:    snapshot.CreatedAt,
			UpdatedAt:    snapshot.UpdatedAt,
		})
		touchedKeys = append(touchedKeys, entry.key)
	}

	return entries, touchedKeys, expiredKeys
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

	m.mu.RLock()
	cloned, found, expired := m.snapshotReadLocked(now, key)
	m.mu.RUnlock()
	if expired {
		m.reconcileReadKeys(now, nil, []cacheKey{key})
		return memorySnapshot{}, false, nil
	}
	if !found {
		return memorySnapshot{}, false, nil
	}
	m.reconcileReadKeys(now, []cacheKey{key}, nil)

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

	m.mu.RLock()
	cloned, found, expired := m.historyReadLocked(now, key)
	m.mu.RUnlock()
	if expired {
		m.reconcileReadKeys(now, nil, []cacheKey{key})
		return nil, false, nil
	}
	if !found {
		return nil, false, nil
	}
	m.reconcileReadKeys(now, []cacheKey{key}, nil)

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

func minInt(left int, right int) int {
	if left < right {
		return left
	}

	return right
}

func (m *Module) collectConversationContextEntriesLocked(
	now time.Time,
	streamEntries []conversationStreamEntry,
	startIndex int,
	query otogi.ConversationContextBeforeQuery,
	excluded map[string]struct{},
) ([]otogi.ConversationContextEntry, []cacheKey, []cacheKey) {
	entries := make([]otogi.ConversationContextEntry, 0, minInt(query.BeforeLimit, len(streamEntries)))
	touchedKeys := make([]cacheKey, 0, minInt(query.BeforeLimit, len(streamEntries)))
	expiredKeys := make([]cacheKey, 0)
	for index := startIndex; index >= 0 && len(entries) < query.BeforeLimit; index-- {
		candidateKey := streamEntries[index].key
		if _, skip := excluded[candidateKey.articleID]; skip {
			continue
		}

		snapshot, found, expired := m.snapshotReadLocked(now, candidateKey)
		if expired {
			expiredKeys = append(expiredKeys, candidateKey)
			continue
		}
		if !found {
			continue
		}

		entries = append(entries, otogi.ConversationContextEntry{
			Conversation: snapshot.Conversation,
			Actor:        snapshot.Actor,
			Article:      cloneArticle(snapshot.Article),
			CreatedAt:    snapshot.CreatedAt,
			UpdatedAt:    snapshot.UpdatedAt,
		})
		touchedKeys = append(touchedKeys, candidateKey)
	}

	return entries, touchedKeys, expiredKeys
}

func (m *Module) snapshotReadLocked(now time.Time, key cacheKey) (memorySnapshot, bool, bool) {
	if !m.isRecordLiveLocked(key, now) {
		return memorySnapshot{}, false, m.isRecordExpiredLocked(key, now)
	}

	cached, exists := m.entities[key]
	if !exists {
		return memorySnapshot{}, false, false
	}

	return cloneMemorySnapshot(cached), true, false
}

func (m *Module) historyReadLocked(now time.Time, key cacheKey) ([]otogi.Event, bool, bool) {
	if !m.isRecordLiveLocked(key, now) {
		return nil, false, m.isRecordExpiredLocked(key, now)
	}

	history, exists := m.events[key]
	if !exists {
		return nil, false, false
	}

	return cloneEventStream(history), true, false
}

func (m *Module) memoryReadLocked(
	now time.Time,
	key cacheKey,
) (memorySnapshot, []otogi.Event, bool, bool) {
	snapshot, found, expired := m.snapshotReadLocked(now, key)
	if expired || !found {
		return memorySnapshot{}, nil, found, expired
	}

	history, _, historyExpired := m.historyReadLocked(now, key)
	if historyExpired {
		return memorySnapshot{}, nil, false, true
	}

	return snapshot, history, true, false
}

func buildMemory(snapshot memorySnapshot, history []otogi.Event) otogi.Memory {
	return otogi.Memory{
		TenantID:     snapshot.TenantID,
		Platform:     snapshot.Platform,
		Conversation: snapshot.Conversation,
		Actor:        snapshot.Actor,
		Article:      cloneArticle(snapshot.Article),
		History:      history,
		CreatedAt:    snapshot.CreatedAt,
		UpdatedAt:    snapshot.UpdatedAt,
	}
}

func (m *Module) isRecordLiveLocked(key cacheKey, now time.Time) bool {
	record, exists := m.records[key]
	if !exists {
		return false
	}

	return !m.isExpired(record, now)
}

func (m *Module) isRecordExpiredLocked(key cacheKey, now time.Time) bool {
	record, exists := m.records[key]
	if !exists {
		return false
	}

	return m.isExpired(record, now)
}

func (m *Module) reconcileReadKeys(now time.Time, touchedKeys []cacheKey, expiredKeys []cacheKey) {
	if len(touchedKeys) == 0 && len(expiredKeys) == 0 {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, key := range expiredKeys {
		m.ensureNotExpiredLocked(key, now)
	}
	for _, key := range touchedKeys {
		if m.ensureNotExpiredLocked(key, now) {
			m.touchLocked(key)
		}
	}
}
