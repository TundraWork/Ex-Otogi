package memory

import "ex-otogi/pkg/otogi"

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
		m.removeConversationArticleFromStreamLocked(location.streamKey, key)
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
		sequence:  sequence,
	}
}

func (m *Module) removeConversationArticleLocked(key cacheKey) {
	location, exists := m.articleStreams[key]
	if !exists {
		return
	}

	m.removeConversationArticleFromStreamLocked(location.streamKey, key)
	delete(m.articleStreams, key)
}

func (m *Module) removeConversationArticleFromStreamLocked(streamKey conversationStreamKey, key cacheKey) {
	entries, exists := m.streams[streamKey]
	if !exists {
		return
	}

	index := locateConversationStreamEntry(entries, key)
	if index < 0 {
		return
	}

	entries = append(entries[:index], entries[index+1:]...)
	if len(entries) == 0 {
		delete(m.streams, streamKey)
		return
	}
	m.streams[streamKey] = entries
}

func insertConversationStreamEntry(
	entries []conversationStreamEntry,
	entry conversationStreamEntry,
) []conversationStreamEntry {
	insertAt := len(entries)
	for index, existing := range entries {
		if conversationStreamEntryLess(entry, existing) {
			insertAt = index
			break
		}
	}

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

func locateConversationStreamEntry(entries []conversationStreamEntry, key cacheKey) int {
	for index, entry := range entries {
		if entry.key == key {
			return index
		}
	}

	return -1
}

func reverseConversationContextEntries(entries []otogi.ConversationContextEntry) {
	for left, right := 0, len(entries)-1; left < right; left, right = left+1, right-1 {
		entries[left], entries[right] = entries[right], entries[left]
	}
}
