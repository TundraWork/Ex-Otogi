package llmchat

import (
	"context"
	"time"

	"ex-otogi/pkg/otogi/ai"
)

func (m *Module) debugToolIteration(ctx context.Context, iteration int, messageCount int) {
	if m == nil || m.logger == nil {
		return
	}

	m.logger.DebugContext(ctx, "llmchat tool iteration start",
		"iteration", iteration,
		"message_count", messageCount,
	)
}

func (m *Module) debugToolCallsDetected(ctx context.Context, iteration int, toolCalls []ai.LLMToolCall) {
	if m == nil || m.logger == nil {
		return
	}

	names := make([]string, 0, len(toolCalls))
	for _, tc := range toolCalls {
		names = append(names, tc.Name)
	}

	m.logger.DebugContext(ctx, "llmchat tool calls detected",
		"iteration", iteration,
		"tool_call_count", len(toolCalls),
		"tool_names", names,
	)
}

func (m *Module) debugToolExecuteStart(ctx context.Context, toolCall ai.LLMToolCall) {
	if m == nil || m.logger == nil {
		return
	}

	m.logger.DebugContext(ctx, "llmchat tool execute start",
		"tool_call_id", toolCall.ID,
		"tool_name", toolCall.Name,
		"arguments_length", len(toolCall.Arguments),
	)
}

func (m *Module) debugToolExecuteEnd(ctx context.Context, toolCall ai.LLMToolCall, elapsed time.Duration, resultLength int) {
	if m == nil || m.logger == nil {
		return
	}

	m.logger.DebugContext(ctx, "llmchat tool execute end",
		"tool_call_id", toolCall.ID,
		"tool_name", toolCall.Name,
		"elapsed", elapsed,
		"result_length", resultLength,
	)
}

func (m *Module) debugSemanticMemoryRetrieve(ctx context.Context, scope ai.LLMMemoryScope, prompt string) {
	if m == nil || m.logger == nil {
		return
	}

	m.logger.DebugContext(ctx, "llmchat semantic memory retrieve start",
		"scope_platform", scope.Platform,
		"scope_conversation_id", scope.ConversationID,
		"prompt_runes", len([]rune(prompt)),
	)
}

func (m *Module) debugSemanticMemoryPlan(ctx context.Context, plan retrievalPlan, plannerUsed bool) {
	if m == nil || m.logger == nil {
		return
	}

	m.logger.DebugContext(ctx, "llmchat semantic memory plan",
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

	m.logger.DebugContext(ctx, "llmchat semantic memory search",
		"raw_match_count", rawMatchCount,
		"search_limit", searchLimit,
		"depth", depth,
	)
}

func (m *Module) debugSemanticMemoryTimeFilter(ctx context.Context, beforeCount int, afterCount int, filter string) {
	if m == nil || m.logger == nil {
		return
	}

	m.logger.DebugContext(ctx, "llmchat semantic memory time filter",
		"before_count", beforeCount,
		"after_count", afterCount,
		"filter", filter,
	)
}

func (m *Module) debugSemanticMemoryRank(ctx context.Context, rankedCount int, selectedCount int, queryTermCount int) {
	if m == nil || m.logger == nil {
		return
	}

	m.logger.DebugContext(ctx, "llmchat semantic memory rank",
		"ranked_count", rankedCount,
		"selected_count", selectedCount,
		"query_term_count", queryTermCount,
	)
}

func (m *Module) debugSemanticMemoryRetrieveResult(
	ctx context.Context,
	scope ai.LLMMemoryScope,
	matchCount int,
	serializedLength int,
	backgroundCount int,
	recalledCount int,
) {
	if m == nil || m.logger == nil {
		return
	}

	m.logger.DebugContext(ctx, "llmchat semantic memory retrieve result",
		"scope_platform", scope.Platform,
		"scope_conversation_id", scope.ConversationID,
		"match_count", matchCount,
		"serialized_length", serializedLength,
		"background_count", backgroundCount,
		"recalled_count", recalledCount,
	)
}
