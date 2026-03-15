package llmchat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"ex-otogi/pkg/otogi/ai"
)

const (
	defaultRecallToolLimit = 5
	maxRecallToolLimit     = 10
)

type recallTool struct {
	scope             ai.LLMMemoryScope
	embeddingProvider ai.EmbeddingProvider
	memoryService     ai.LLMMemoryService
}

type recallToolArgs struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

type recallToolResult struct {
	ID         string  `json:"id"`
	Content    string  `json:"content"`
	Category   string  `json:"category"`
	Similarity float32 `json:"similarity"`
	CreatedAt  string  `json:"created_at"`
}

func newRecallTool(
	scope ai.LLMMemoryScope,
	embeddingProvider ai.EmbeddingProvider,
	memoryService ai.LLMMemoryService,
) ToolHandler {
	return &recallTool{
		scope:             scope,
		embeddingProvider: embeddingProvider,
		memoryService:     memoryService,
	}
}

func (*recallTool) Name() string {
	return "recall"
}

func (*recallTool) Definition() ai.LLMToolDefinition {
	return recallToolLLMDefinition()
}

func (t *recallTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	if ctx == nil {
		return "", fmt.Errorf("recall tool: nil context")
	}
	if t.embeddingProvider == nil {
		return "", fmt.Errorf("recall tool: embedding provider is nil")
	}
	if t.memoryService == nil {
		return "", fmt.Errorf("recall tool: memory service is nil")
	}

	var input recallToolArgs
	if err := decodeToolArgsJSON(args, &input); err != nil {
		return "", fmt.Errorf("recall tool: %w", err)
	}

	query := strings.TrimSpace(input.Query)
	if query == "" {
		return "", fmt.Errorf("recall tool: missing query")
	}
	limit := resolveRecallToolLimit(input.Limit)

	embedding, err := embedSingleText(ctx, t.embeddingProvider, query, ai.EmbeddingTaskTypeQuery)
	if err != nil {
		return "", fmt.Errorf("recall tool: %w", err)
	}

	matches, err := t.memoryService.Search(ctx, ai.LLMMemoryQuery{
		Scope:         t.scope,
		Embedding:     embedding,
		Limit:         limit,
		MinSimilarity: 0.3,
	})
	if err != nil {
		return "", fmt.Errorf("recall tool: search memories: %w", err)
	}
	if len(matches) == 0 {
		return "No relevant memories found.", nil
	}

	results := make([]recallToolResult, 0, len(matches))
	for _, match := range matches {
		results = append(results, recallToolResult{
			ID:         match.Record.ID,
			Content:    match.Record.Content,
			Category:   match.Record.Category,
			Similarity: match.Similarity,
			CreatedAt:  match.Record.CreatedAt.UTC().Format(time.RFC3339),
		})
	}

	encoded, err := json.Marshal(results)
	if err != nil {
		return "", fmt.Errorf("recall tool: marshal results: %w", err)
	}

	return string(encoded), nil
}

func recallToolLLMDefinition() ai.LLMToolDefinition {
	return ai.LLMToolDefinition{
		Name:        "recall",
		Description: "Search previously stored memories relevant to the current conversation.",
		Parameters: json.RawMessage(`{
			"type":"object",
			"properties":{
				"query":{
					"type":"string",
					"description":"Natural language search query to find relevant memories"
				},
				"limit":{
					"type":"integer",
					"description":"Maximum number of memories to return (default 5, max 10)",
					"minimum":1,
					"maximum":10
				}
			},
			"required":["query"],
			"additionalProperties":false
		}`),
	}
}

func resolveRecallToolLimit(limit int) int {
	switch {
	case limit <= 0:
		return defaultRecallToolLimit
	case limit > maxRecallToolLimit:
		return maxRecallToolLimit
	default:
		return limit
	}
}
