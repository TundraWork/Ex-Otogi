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
		"consolidation_provider", cfg.ConsolidationProvider,
		"consolidation_model", cfg.ConsolidationModel,
		"consolidation_interval", cfg.ConsolidationInterval,
		"context_window_size", cfg.ContextWindowSize,
		"extraction_max_input_runes", cfg.ExtractionMaxInputRunes,
		"synthesis_match_limit", cfg.SynthesisMatchLimit,
		"max_memories_per_scope", cfg.MaxMemoriesPerScope,
		"min_importance", cfg.MinImportance,
		"duplicate_similarity_threshold", cfg.DuplicateSimilarityThreshold,
		"cluster_min_size", cfg.ClusterMinSize,
		"cluster_similarity_threshold", cfg.ClusterSimilarityThreshold,
	)
}

func (m *Module) debugArticleReceived(
	ctx context.Context,
	scope ai.LLMMemoryScope,
	articleID string,
	articleTextRunes int,
	sourceActorName string,
) {
	if m == nil || m.logger == nil {
		return
	}

	m.logger.DebugContext(ctx, "naturalmemory article received",
		"scope_platform", scope.Platform,
		"scope_conversation_id", scope.ConversationID,
		"article_id", articleID,
		"article_text_runes", articleTextRunes,
		"source_actor", sourceActorName,
	)
}

func (m *Module) debugConsolidationStart(ctx context.Context) {
	if m == nil || m.logger == nil {
		return
	}

	m.logger.DebugContext(ctx, "naturalmemory consolidation loop started",
		"interval", m.cfg.ConsolidationInterval,
		"max_memories_per_scope", m.cfg.MaxMemoriesPerScope,
		"cluster_min_size", m.cfg.ClusterMinSize,
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

	// Show a prefix of the response to aid debugging malformed LLM output.
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
		"category", candidate.Category,
		"importance", candidate.Importance,
		"content_runes", len([]rune(candidate.Content)),
		"keywords", candidate.Keywords,
		"tags", candidate.Tags,
	)
}

func (m *Module) debugExtractionStart(
	ctx context.Context,
	scope ai.LLMMemoryScope,
	articleID string,
	conversationRunes int,
	existingCount int,
) {
	if m == nil || m.logger == nil {
		return
	}

	m.logger.DebugContext(ctx, "naturalmemory extraction start",
		"scope_platform", scope.Platform,
		"scope_conversation_id", scope.ConversationID,
		"article_id", articleID,
		"conversation_runes", conversationRunes,
		"existing_memories", existingCount,
		"model", m.cfg.ExtractionModel,
	)
}

func (m *Module) debugExtractionResult(ctx context.Context, candidateCount int, categories []string) {
	if m == nil || m.logger == nil {
		return
	}

	m.logger.DebugContext(ctx, "naturalmemory extraction produced candidates",
		"candidate_count", candidateCount,
		"categories", categories,
	)
}

func (m *Module) debugCandidateUpsert(
	ctx context.Context,
	action synthesisAction,
	targetID string,
	category string,
	importance int,
	contentRunes int,
) {
	if m == nil || m.logger == nil {
		return
	}

	m.logger.DebugContext(ctx, "naturalmemory candidate upsert",
		"action", string(action),
		"target_id", targetID,
		"category", category,
		"importance", importance,
		"content_runes", contentRunes,
	)
}

func (m *Module) debugSynthesisDecision(
	ctx context.Context,
	action synthesisAction,
	targetID string,
	absorbedCount int,
	usedFallback bool,
) {
	if m == nil || m.logger == nil {
		return
	}

	m.logger.DebugContext(ctx, "naturalmemory synthesis decision",
		"action", string(action),
		"target_id", targetID,
		"absorbed_count", absorbedCount,
		"used_fallback", usedFallback,
	)
}

func (m *Module) debugSynthesisQuery(
	ctx context.Context,
	candidateContentRunes int,
	matchCount int,
	topSimilarity float32,
) {
	if m == nil || m.logger == nil {
		return
	}

	m.logger.DebugContext(ctx, "naturalmemory synthesis query",
		"candidate_content_runes", candidateContentRunes,
		"match_count", matchCount,
		"top_similarity", topSimilarity,
	)
}

func (m *Module) debugLinkGeneration(
	ctx context.Context,
	recordID string,
	linkCount int,
	reverseLinksApplied int,
) {
	if m == nil || m.logger == nil {
		return
	}

	m.logger.DebugContext(ctx, "naturalmemory links applied",
		"record_id", recordID,
		"forward_links", linkCount,
		"reverse_links", reverseLinksApplied,
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

func (m *Module) debugConsolidationClustering(
	ctx context.Context,
	clusterCount int,
	themeEligible int,
	mergeEligible int,
	singletons int,
) {
	if m == nil || m.logger == nil {
		return
	}

	m.logger.DebugContext(ctx, "naturalmemory consolidation clustered",
		"cluster_count", clusterCount,
		"theme_eligible", themeEligible,
		"merge_eligible", mergeEligible,
		"singletons", singletons,
	)
}

func (m *Module) debugConsolidationThemeGenerated(ctx context.Context, clusterSize int, contentRunes int) {
	if m == nil || m.logger == nil {
		return
	}

	m.logger.DebugContext(ctx, "naturalmemory theme generated from cluster",
		"source_records", clusterSize,
		"theme_content_runes", contentRunes,
	)
}

func (m *Module) debugConsolidationMerge(ctx context.Context, clusterSize int) {
	if m == nil || m.logger == nil {
		return
	}

	m.logger.DebugContext(ctx, "naturalmemory pairwise merge started",
		"cluster_size", clusterSize,
		"similarity_threshold", consolidationMergeSimilarityThreshold,
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

func (m *Module) debugConsolidationReflection(ctx context.Context, recordCount int, reflectionMinSource int) {
	if m == nil || m.logger == nil {
		return
	}

	eligible := recordCount >= reflectionMinSource
	m.logger.DebugContext(ctx, "naturalmemory reflection check",
		"record_count", recordCount,
		"min_source_required", reflectionMinSource,
		"eligible", eligible,
	)
}
