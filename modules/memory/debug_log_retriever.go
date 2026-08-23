package memory

import (
	"context"

	"ex-otogi/pkg/otogi/ai"
)

func (m *Module) debugSemanticMemoryRetrieve(ctx context.Context, scope ai.SemanticScope, prompt string) {
	if m == nil || m.logger == nil {
		return
	}

	m.logger.DebugContext(ctx, "memory semantic retrieve start",
		"scope_platform", scope.Platform,
		"scope_conversation_id", scope.ConversationID,
		"prompt_runes", len([]rune(prompt)),
	)
}

func (m *Module) debugSemanticMemoryPlan(ctx context.Context, plan retrievalPlan, plannerUsed bool) {
	if m == nil || m.logger == nil {
		return
	}

	m.logger.DebugContext(ctx, "memory semantic retrieve plan",
		"query_count", len(plan.Queries),
		"queries", plan.Queries,
		"time_filter", plan.TimeFilter,
		"depth", plan.Depth,
		"planner_used", plannerUsed,
	)
}

func (m *Module) debugSemanticMemorySearch(ctx context.Context, rawMatchCount int, searchLimit int, depth string) {
	if m == nil || m.logger == nil {
		return
	}

	m.logger.DebugContext(ctx, "memory semantic retrieve search",
		"raw_match_count", rawMatchCount,
		"search_limit", searchLimit,
		"depth", depth,
	)
}

func (m *Module) debugSemanticMemoryTimeFilter(ctx context.Context, beforeCount int, afterCount int, filter string) {
	if m == nil || m.logger == nil {
		return
	}

	m.logger.DebugContext(ctx, "memory semantic retrieve time filter",
		"before_count", beforeCount,
		"after_count", afterCount,
		"filter", filter,
	)
}

func (m *Module) debugSemanticMemoryRank(ctx context.Context, rankedCount int, selectedCount int, queryTermCount int) {
	if m == nil || m.logger == nil {
		return
	}

	m.logger.DebugContext(ctx, "memory semantic retrieve rank",
		"ranked_count", rankedCount,
		"selected_count", selectedCount,
		"query_term_count", queryTermCount,
	)
}

func (m *Module) debugSemanticMemoryRetrieveResult(
	ctx context.Context,
	scope ai.SemanticScope,
	matchCount int,
	serializedLength int,
	backgroundCount int,
	recalledCount int,
) {
	if m == nil || m.logger == nil {
		return
	}

	m.logger.DebugContext(ctx, "memory semantic retrieve result",
		"scope_platform", scope.Platform,
		"scope_conversation_id", scope.ConversationID,
		"match_count", matchCount,
		"serialized_length", serializedLength,
		"background_count", backgroundCount,
		"recalled_count", recalledCount,
	)
}
