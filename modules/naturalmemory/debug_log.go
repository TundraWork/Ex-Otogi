package naturalmemory

import (
	"context"

	"ex-otogi/pkg/otogi/ai"
)

func (m *Module) debugConfigLoaded(ctx context.Context, cfg Config) {
	if m == nil || m.logger == nil {
		return
	}

	m.logger.DebugContext(ctx, "naturalmemory config loaded",
		"enabled", cfg.Enabled,
		"extraction_provider", cfg.ExtractionProvider,
		"embedding_provider", cfg.EmbeddingProvider,
		"consolidation_interval", cfg.ConsolidationInterval,
		"context_window_size", cfg.ContextWindowSize,
	)
}

func (m *Module) debugArticleObserved(ctx context.Context, scope ai.LLMMemoryScope, articleID string) {
	if m == nil || m.logger == nil {
		return
	}

	m.logger.DebugContext(ctx, "naturalmemory article observed",
		"scope_platform", scope.Platform,
		"scope_conversation_id", scope.ConversationID,
		"article_id", articleID,
	)
}

func (m *Module) debugConsolidationStart(ctx context.Context) {
	if m == nil || m.logger == nil {
		return
	}

	m.logger.DebugContext(ctx, "naturalmemory consolidation started",
		"interval", m.cfg.ConsolidationInterval,
	)
}

func (m *Module) debugConsolidationStop(ctx context.Context) {
	if m == nil || m.logger == nil {
		return
	}

	m.logger.DebugContext(ctx, "naturalmemory consolidation stopped")
}

func (m *Module) debugExtractionParseError(ctx context.Context, err error, response string) {
	if m == nil || m.logger == nil || err == nil {
		return
	}

	m.logger.WarnContext(ctx, "naturalmemory extraction parse error",
		"error", err,
		"response_runes", len([]rune(response)),
	)
}

func (m *Module) debugCandidateError(ctx context.Context, candidate extractedMemory, err error) {
	if m == nil || m.logger == nil || err == nil {
		return
	}

	m.logger.WarnContext(ctx, "naturalmemory candidate processing error",
		"error", err,
		"category", candidate.Category,
		"importance", candidate.Importance,
		"content_runes", len([]rune(candidate.Content)),
	)
}
