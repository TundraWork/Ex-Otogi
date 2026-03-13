package memory

import (
	"sort"

	"ex-otogi/pkg/otogi"
)

func conversationStreamKeyFromSnapshot(snapshot memorySnapshot) conversationStreamKey {
	return conversationStreamKey{
		tenantID:       snapshot.TenantID,
		platform:       snapshot.Platform,
		conversationID: snapshot.Conversation.ID,
		threadID:       snapshot.Article.ThreadID,
	}
}

func (m *Module) upsertConversationArticleLocked(key cacheKey, snapshot memorySnapshot) {
	streamKey := conversationStreamKeyFromSnapshot(snapshot)
	createdAt := snapshot.CreatedAt
	if createdAt.IsZero() {
		createdAt = snapshot.UpdatedAt
	}

	location, exists := m.articleStreams[key]
	sequence := location.sequence
	if exists {
		m.removeConversationArticleFromStreamLocked(location, key)
	} else {
		m.nextConversationSequence++
		sequence = m.nextConversationSequence
	}

	entry := conversationStreamEntry{
		key:       key,
		createdAt: createdAt,
		sequence:  sequence,
	}
	m.streams[streamKey] = insertConversationStreamEntry(m.streams[streamKey], entry)
	m.articleStreams[key] = conversationArticleStream{
		streamKey: streamKey,
		createdAt: createdAt,
		sequence:  sequence,
	}
}

func (m *Module) removeConversationArticleLocked(key cacheKey) {
	location, exists := m.articleStreams[key]
	if !exists {
		return
	}

	m.removeConversationArticleFromStreamLocked(location, key)
	delete(m.articleStreams, key)
}

func (m *Module) removeConversationArticleFromStreamLocked(
	location conversationArticleStream,
	key cacheKey,
) {
	entries, exists := m.streams[location.streamKey]
	if !exists {
		return
	}

	index := locateConversationStreamEntry(entries, conversationStreamEntry{
		key:       key,
		createdAt: location.createdAt,
		sequence:  location.sequence,
	})
	if index < 0 {
		return
	}

	entries = append(entries[:index], entries[index+1:]...)
	if len(entries) == 0 {
		delete(m.streams, location.streamKey)
		return
	}
	m.streams[location.streamKey] = entries
}

func insertConversationStreamEntry(
	entries []conversationStreamEntry,
	entry conversationStreamEntry,
) []conversationStreamEntry {
	insertAt := sort.Search(len(entries), func(index int) bool {
		return !conversationStreamEntryLess(entries[index], entry)
	})

	entries = append(entries, conversationStreamEntry{})
	copy(entries[insertAt+1:], entries[insertAt:])
	entries[insertAt] = entry

	return entries
}

func conversationStreamEntryLess(left conversationStreamEntry, right conversationStreamEntry) bool {
	switch {
	case left.createdAt.Before(right.createdAt):
		return true
	case right.createdAt.Before(left.createdAt):
		return false
	default:
		return left.sequence < right.sequence
	}
}

func locateConversationStreamEntry(entries []conversationStreamEntry, target conversationStreamEntry) int {
	index := sort.Search(len(entries), func(position int) bool {
		return !conversationStreamEntryLess(entries[position], target)
	})
	if index >= len(entries) {
		return -1
	}
	if !conversationStreamEntryEqual(entries[index], target) {
		return -1
	}

	return index
}

func conversationStreamEntryEqual(left conversationStreamEntry, right conversationStreamEntry) bool {
	return left.key == right.key &&
		left.sequence == right.sequence &&
		left.createdAt.Equal(right.createdAt)
}

func reverseConversationContextEntries(entries []otogi.ConversationContextEntry) {
	for left, right := 0, len(entries)-1; left < right; left, right = left+1, right-1 {
		entries[left], entries[right] = entries[right], entries[left]
	}
}
