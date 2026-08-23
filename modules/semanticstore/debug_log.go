package semanticstore

import (
	"context"

	"ex-otogi/pkg/otogi/ai"
)

func (m *Module) debugStore(ctx context.Context, entry ai.SemanticEntry) {
	if m == nil || m.logger == nil {
		return
	}

	m.logger.DebugContext(ctx, "semanticstore store",
		"scope_platform", entry.Scope.Platform,
		"scope_conversation_id", entry.Scope.ConversationID,
		"category", entry.Category,
		"content_runes", len([]rune(entry.Content)),
		"embedding_dimensions", len(entry.Embedding),
	)
}

func (m *Module) debugStoreResult(ctx context.Context, record ai.SemanticRecord) {
	if m == nil || m.logger == nil {
		return
	}

	m.logger.DebugContext(ctx, "semanticstore store result",
		"record_id", record.ID,
		"scope_platform", record.Scope.Platform,
		"scope_conversation_id", record.Scope.ConversationID,
		"category", record.Category,
	)
}

func (m *Module) debugSearch(ctx context.Context, query ai.SemanticQuery) {
	if m == nil || m.logger == nil {
		return
	}

	m.logger.DebugContext(ctx, "semanticstore search",
		"scope_platform", query.Scope.Platform,
		"scope_conversation_id", query.Scope.ConversationID,
		"limit", query.Limit,
		"min_similarity", query.MinSimilarity,
		"embedding_dimensions", len(query.Embedding),
	)
}

func (m *Module) debugSearchResult(ctx context.Context, query ai.SemanticQuery, matches []ai.SemanticMatch) {
	if m == nil || m.logger == nil {
		return
	}

	var topSimilarity float32
	if len(matches) > 0 {
		topSimilarity = matches[0].Similarity
	}

	m.logger.DebugContext(ctx, "semanticstore search result",
		"scope_platform", query.Scope.Platform,
		"scope_conversation_id", query.Scope.ConversationID,
		"match_count", len(matches),
		"top_similarity", topSimilarity,
	)
}

func (m *Module) debugUpdate(ctx context.Context, update ai.SemanticUpdate) {
	if m == nil || m.logger == nil {
		return
	}

	m.logger.DebugContext(ctx, "semanticstore update",
		"record_id", update.ID,
		"category", update.Category,
		"kind", update.Profile.Kind,
		"content_runes", len([]rune(update.Content)),
	)
}

func (m *Module) debugDelete(ctx context.Context, id string) {
	if m == nil || m.logger == nil {
		return
	}

	m.logger.DebugContext(ctx, "semanticstore delete",
		"record_id", id,
	)
}

func (m *Module) debugListByScope(ctx context.Context, scope ai.SemanticScope, limit int) {
	if m == nil || m.logger == nil {
		return
	}

	m.logger.DebugContext(ctx, "semanticstore list by scope",
		"scope_platform", scope.Platform,
		"scope_conversation_id", scope.ConversationID,
		"limit", limit,
	)
}

func (m *Module) debugListByScopeResult(ctx context.Context, scope ai.SemanticScope, recordCount int) {
	if m == nil || m.logger == nil {
		return
	}

	m.logger.DebugContext(ctx, "semanticstore list by scope result",
		"scope_platform", scope.Platform,
		"scope_conversation_id", scope.ConversationID,
		"record_count", recordCount,
	)
}
