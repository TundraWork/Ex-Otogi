package memory

import (
	"time"

	"ex-otogi/pkg/otogi"
)

func (m *Module) upsertEntityLocked(key cacheKey, cached memorySnapshot, now time.Time) {
	m.upsertKeyLocked(key, now)
	m.entities[key] = cloneMemorySnapshot(cached)
	m.trimToCapacityLocked()
}

func (m *Module) upsertEventLocked(key cacheKey, event *otogi.Event, now time.Time) {
	if event == nil {
		return
	}

	cloned := cloneEvent(*event)
	cloned = m.enrichHistoryEventLocked(key, cloned)
	m.upsertKeyLocked(key, now)
	m.events[key] = append(m.events[key], cloned)
	m.trimToCapacityLocked()
}

func (m *Module) enrichHistoryEventLocked(key cacheKey, event otogi.Event) otogi.Event {
	if event.Kind != otogi.EventKindArticleEdited {
		return event
	}
	if event.Mutation == nil || event.Mutation.Before != nil {
		return event
	}

	existing, exists := m.entities[key]
	if !exists {
		return event
	}
	event.Mutation.Before = &otogi.ArticleSnapshot{
		Text:     existing.Article.Text,
		Entities: append([]otogi.TextEntity(nil), existing.Article.Entities...),
		Media:    cloneMediaAttachments(existing.Article.Media),
	}

	return event
}

func (m *Module) upsertKeyLocked(key cacheKey, now time.Time) {
	if m.ensureNotExpiredLocked(key, now) {
		record := m.records[key]
		record.expiresAt = m.expiryFrom(now)
		m.touchLocked(key)
		return
	}

	element := m.lru.PushFront(key)
	m.index[key] = element
	m.records[key] = &cacheRecord{expiresAt: m.expiryFrom(now)}
}

func (m *Module) ensureNotExpiredLocked(key cacheKey, now time.Time) bool {
	record, exists := m.records[key]
	if !exists {
		return false
	}
	if m.isExpired(record, now) {
		m.deleteLocked(key)
		return false
	}

	return true
}

func (m *Module) trimToCapacityLocked() {
	for len(m.records) > m.maxEntries {
		back := m.lru.Back()
		if back == nil {
			break
		}
		oldestKey, ok := back.Value.(cacheKey)
		if !ok {
			m.lru.Remove(back)
			continue
		}
		m.deleteLocked(oldestKey)
	}
}

func (m *Module) touchLocked(key cacheKey) {
	element, exists := m.index[key]
	if !exists {
		return
	}
	m.lru.MoveToFront(element)
}

func (m *Module) deleteLocked(key cacheKey) {
	if element, exists := m.index[key]; exists {
		m.lru.Remove(element)
		delete(m.index, key)
	}
	delete(m.records, key)
	delete(m.entities, key)
	delete(m.events, key)
}

func (m *Module) isExpired(record *cacheRecord, now time.Time) bool {
	if record == nil {
		return true
	}
	if record.expiresAt.IsZero() {
		return false
	}

	return !now.Before(record.expiresAt)
}

func (m *Module) expiryFrom(now time.Time) time.Time {
	if m.ttl <= 0 {
		return time.Time{}
	}

	return now.Add(m.ttl)
}

func (m *Module) now() time.Time {
	return m.clock().UTC()
}

func cacheKeyFromLookup(lookup otogi.MemoryLookup) cacheKey {
	return cacheKey{
		tenantID:       lookup.TenantID,
		platform:       lookup.Platform,
		conversationID: lookup.ConversationID,
		articleID:      lookup.ArticleID,
	}
}

func normalizeEventTime(occurredAt time.Time, fallback time.Time) time.Time {
	if occurredAt.IsZero() {
		return fallback
	}

	return occurredAt.UTC()
}

func mutationChangedAtOrFallback(mutation *otogi.ArticleMutation, fallback time.Time) time.Time {
	if mutation == nil || mutation.ChangedAt == nil || mutation.ChangedAt.IsZero() {
		return fallback
	}

	return mutation.ChangedAt.UTC()
}
