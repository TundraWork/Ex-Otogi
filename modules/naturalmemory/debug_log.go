package naturalmemory

import (
	"context"
	"strings"

	"ex-otogi/pkg/otogi/ai"
)

func (m *Module) debugConfigLoaded(ctx context.Context, cfg Config) {
	if m == nil || m.logger == nil {
		return
	}

	m.logger.DebugContext(ctx, "naturalmemory config loaded",
		"enabled", cfg.Enabled,
		"extraction_provider", cfg.ExtractionProvider,
		"extraction_model", cfg.ExtractionModel,
		"embedding_provider", cfg.EmbeddingProvider,
		"consolidation_interval", cfg.ConsolidationInterval,
		"extraction_max_input_runes", cfg.ExtractionMaxInputRunes,
		"max_memories_per_scope", cfg.MaxMemoriesPerScope,
		"min_importance", cfg.MinImportance,
		"duplicate_similarity_threshold", cfg.DuplicateSimilarityThreshold,
		"retrieval_search_limit", cfg.RetrievalSearchLimit,
		"buffer_quiet_period", cfg.BufferQuietPeriod,
		"buffer_max_runes", cfg.BufferMaxRunes,
		"buffer_max_articles", cfg.BufferMaxArticles,
		"buffer_max_age", cfg.BufferMaxAge,
	)
}

func (m *Module) debugWindowEnqueue(ctx context.Context, scope ai.LLMMemoryScope, articleID string) {
	if m == nil || m.logger == nil {
		return
	}

	m.logger.DebugContext(ctx, "naturalmemory window enqueued",
		"scope_platform", scope.Platform,
		"scope_conversation_id", scope.ConversationID,
		"article_id", articleID,
	)
}

func (m *Module) debugConsolidationStart(ctx context.Context) {
	if m == nil || m.logger == nil {
		return
	}

	m.logger.DebugContext(ctx, "naturalmemory consolidation loop started",
		"interval", m.cfg.ConsolidationInterval,
		"max_memories_per_scope", m.cfg.MaxMemoriesPerScope,
	)
}

func (m *Module) debugConsolidationStop(ctx context.Context) {
	if m == nil || m.logger == nil {
		return
	}

	m.logger.DebugContext(ctx, "naturalmemory consolidation loop stopped")
}

func (m *Module) debugConsolidationCycleStart(ctx context.Context, scopeCount int) {
	if m == nil || m.logger == nil {
		return
	}

	m.logger.DebugContext(ctx, "naturalmemory consolidation cycle",
		"scope_count", scopeCount,
	)
}

func (m *Module) debugExtractionParseError(ctx context.Context, err error, response string) {
	if m == nil || m.logger == nil || err == nil {
		return
	}

	preview := strings.TrimSpace(response)
	if len([]rune(preview)) > 200 {
		preview = string([]rune(preview)[:200]) + "..."
	}

	m.logger.WarnContext(ctx, "naturalmemory extraction parse failed",
		"error", err,
		"response_runes", len([]rune(response)),
		"response_preview", preview,
	)
}

func (m *Module) debugCandidateError(ctx context.Context, candidate extractedMemory, err error) {
	if m == nil || m.logger == nil || err == nil {
		return
	}

	m.logger.WarnContext(ctx, "naturalmemory candidate processing failed",
		"error", err,
		"action", string(candidate.Action),
		"category", candidate.Category,
		"importance", candidate.Importance,
		"content_runes", len([]rune(candidate.Content)),
	)
}

func (m *Module) debugConsolidationScopeStart(
	ctx context.Context,
	scope ai.LLMMemoryScope,
	totalRecords int,
	expiredCount int,
	prunedCount int,
	keptCount int,
) {
	if m == nil || m.logger == nil {
		return
	}

	m.logger.DebugContext(ctx, "naturalmemory consolidation scope prune",
		"scope_platform", scope.Platform,
		"scope_conversation_id", scope.ConversationID,
		"total_records", totalRecords,
		"expired", expiredCount,
		"pruned", prunedCount,
		"kept", keptCount,
	)
}

func (m *Module) debugConsolidationCapOverflow(ctx context.Context, totalRecords int, maxAllowed int, prunedCount int) {
	if m == nil || m.logger == nil {
		return
	}

	m.logger.DebugContext(ctx, "naturalmemory cap overflow pruned",
		"total_records", totalRecords,
		"max_allowed", maxAllowed,
		"pruned", prunedCount,
	)
}
