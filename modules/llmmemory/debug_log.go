package llmmemory

import (
	"context"

	"ex-otogi/pkg/otogi/ai"
)

func (m *Module) debugStore(ctx context.Context, entry ai.LLMMemoryEntry) {
	if m == nil || m.logger == nil {
		return
	}

	m.logger.DebugContext(ctx, "llmmemory store",
		"scope_platform", entry.Scope.Platform,
		"scope_conversation_id", entry.Scope.ConversationID,
		"category", entry.Category,
		"content_runes", len([]rune(entry.Content)),
		"embedding_dimensions", len(entry.Embedding),
	)
}

func (m *Module) debugStoreResult(ctx context.Context, record ai.LLMMemoryRecord) {
	if m == nil || m.logger == nil {
		return
	}

	m.logger.DebugContext(ctx, "llmmemory store result",
		"record_id", record.ID,
		"scope_platform", record.Scope.Platform,
		"scope_conversation_id", record.Scope.ConversationID,
		"category", record.Category,
	)
}

func (m *Module) debugSearch(ctx context.Context, query ai.LLMMemoryQuery) {
	if m == nil || m.logger == nil {
		return
	}

	m.logger.DebugContext(ctx, "llmmemory search",
		"scope_platform", query.Scope.Platform,
		"scope_conversation_id", query.Scope.ConversationID,
		"limit", query.Limit,
		"min_similarity", query.MinSimilarity,
		"embedding_dimensions", len(query.Embedding),
	)
}

func (m *Module) debugSearchResult(ctx context.Context, query ai.LLMMemoryQuery, matches []ai.LLMMemoryMatch) {
	if m == nil || m.logger == nil {
		return
	}

	var topSimilarity float32
	if len(matches) > 0 {
		topSimilarity = matches[0].Similarity
	}

	m.logger.DebugContext(ctx, "llmmemory search result",
		"scope_platform", query.Scope.Platform,
		"scope_conversation_id", query.Scope.ConversationID,
		"match_count", len(matches),
		"top_similarity", topSimilarity,
	)
}

func (m *Module) debugUpdate(ctx context.Context, id string, content string) {
	if m == nil || m.logger == nil {
		return
	}

	m.logger.DebugContext(ctx, "llmmemory update",
		"record_id", id,
		"content_runes", len([]rune(content)),
	)
}

func (m *Module) debugDelete(ctx context.Context, id string) {
	if m == nil || m.logger == nil {
		return
	}

	m.logger.DebugContext(ctx, "llmmemory delete",
		"record_id", id,
	)
}

func (m *Module) debugListByScope(ctx context.Context, scope ai.LLMMemoryScope, limit int) {
	if m == nil || m.logger == nil {
		return
	}

	m.logger.DebugContext(ctx, "llmmemory list by scope",
		"scope_platform", scope.Platform,
		"scope_conversation_id", scope.ConversationID,
		"limit", limit,
	)
}

func (m *Module) debugListByScopeResult(ctx context.Context, scope ai.LLMMemoryScope, recordCount int) {
	if m == nil || m.logger == nil {
		return
	}

	m.logger.DebugContext(ctx, "llmmemory list by scope result",
		"scope_platform", scope.Platform,
		"scope_conversation_id", scope.ConversationID,
		"record_count", recordCount,
	)
}

func (m *Module) debugPersistenceLoad(ctx context.Context, file string) {
	if m == nil || m.logger == nil || m.store == nil {
		return
	}

	m.logger.DebugContext(ctx, "llmmemory persistence loaded",
		"file", file,
		"record_count", m.store.recordCount(),
		"scope_count", m.store.scopeCount(),
	)
}

func (m *Module) debugPersistenceSave(ctx context.Context, file string) {
	if m == nil || m.logger == nil || m.store == nil {
		return
	}

	m.logger.DebugContext(ctx, "llmmemory persistence saved",
		"file", file,
		"record_count", m.store.recordCount(),
		"scope_count", m.store.scopeCount(),
	)
}
