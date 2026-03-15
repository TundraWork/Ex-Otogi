package llmchat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"ex-otogi/pkg/otogi/ai"
)

type rememberTool struct {
	scope             ai.LLMMemoryScope
	embeddingProvider ai.EmbeddingProvider
	memoryService     ai.LLMMemoryService
}

type rememberToolArgs struct {
	Content  string `json:"content"`
	Category string `json:"category"`
}

func newRememberTool(
	scope ai.LLMMemoryScope,
	embeddingProvider ai.EmbeddingProvider,
	memoryService ai.LLMMemoryService,
) ToolHandler {
	return &rememberTool{
		scope:             scope,
		embeddingProvider: embeddingProvider,
		memoryService:     memoryService,
	}
}

func (*rememberTool) Name() string {
	return "remember"
}

func (*rememberTool) Definition() ai.LLMToolDefinition {
	return rememberToolLLMDefinition()
}

func (t *rememberTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	if ctx == nil {
		return "", fmt.Errorf("remember tool: nil context")
	}
	if t.embeddingProvider == nil {
		return "", fmt.Errorf("remember tool: embedding provider is nil")
	}
	if t.memoryService == nil {
		return "", fmt.Errorf("remember tool: memory service is nil")
	}

	var input rememberToolArgs
	if err := decodeToolArgsJSON(args, &input); err != nil {
		return "", fmt.Errorf("remember tool: %w", err)
	}

	content := strings.TrimSpace(input.Content)
	category := strings.TrimSpace(input.Category)
	if content == "" {
		return "", fmt.Errorf("remember tool: missing content")
	}
	if !isValidMemoryCategory(category) {
		return "", fmt.Errorf("remember tool: unsupported category %q", input.Category)
	}

	embedding, err := embedSingleText(ctx, t.embeddingProvider, content, ai.EmbeddingTaskTypeDocument)
	if err != nil {
		return "", fmt.Errorf("remember tool: %w", err)
	}

	record, err := t.memoryService.Store(ctx, ai.LLMMemoryEntry{
		Scope:     t.scope,
		Content:   content,
		Category:  category,
		Embedding: embedding,
	})
	if err != nil {
		return "", fmt.Errorf("remember tool: store memory: %w", err)
	}

	return fmt.Sprintf("Remembered: %s (id: %s)", content, record.ID), nil
}

func rememberToolLLMDefinition() ai.LLMToolDefinition {
	return ai.LLMToolDefinition{
		Name:        "remember",
		Description: "Store a useful fact or preference for future use in this conversation.",
		Parameters: json.RawMessage(`{
			"type":"object",
			"properties":{
				"content":{
					"type":"string",
					"description":"The fact or knowledge to remember for future reference"
				},
				"category":{
					"type":"string",
					"description":"Category of the memory: user_fact, preference, knowledge, or experience",
					"enum":["user_fact","preference","knowledge","experience"]
				}
			},
			"required":["content","category"],
			"additionalProperties":false
		}`),
	}
}

func isValidMemoryCategory(category string) bool {
	switch category {
	case memoryToolCategoryUserFact,
		memoryToolCategoryPreference,
		memoryToolCategoryKnowledge,
		memoryToolCategoryExperience:
		return true
	default:
		return false
	}
}
