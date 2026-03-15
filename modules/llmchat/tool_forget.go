package llmchat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"ex-otogi/pkg/otogi/ai"
)

type forgetTool struct {
	memoryService ai.LLMMemoryService
}

type forgetToolArgs struct {
	MemoryID string `json:"memory_id"`
}

func newForgetTool(memoryService ai.LLMMemoryService) ToolHandler {
	return &forgetTool{memoryService: memoryService}
}

func (*forgetTool) Name() string {
	return "forget"
}

func (*forgetTool) Definition() ai.LLMToolDefinition {
	return forgetToolLLMDefinition()
}

func (t *forgetTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	if ctx == nil {
		return "", fmt.Errorf("forget tool: nil context")
	}
	if t.memoryService == nil {
		return "", fmt.Errorf("forget tool: memory service is nil")
	}

	var input forgetToolArgs
	if err := decodeToolArgsJSON(args, &input); err != nil {
		return "", fmt.Errorf("forget tool: %w", err)
	}

	memoryID := strings.TrimSpace(input.MemoryID)
	if memoryID == "" {
		return "", fmt.Errorf("forget tool: missing memory_id")
	}
	if err := t.memoryService.Delete(ctx, memoryID); err != nil {
		return "", fmt.Errorf("forget tool: delete memory %s: %w", memoryID, err)
	}

	return fmt.Sprintf("Forgotten memory %s.", memoryID), nil
}

func forgetToolLLMDefinition() ai.LLMToolDefinition {
	return ai.LLMToolDefinition{
		Name:        "forget",
		Description: "Delete one previously stored memory by ID.",
		Parameters: json.RawMessage(`{
			"type":"object",
			"properties":{
				"memory_id":{
					"type":"string",
					"description":"The ID of the memory to forget (from a previous recall result)"
				}
			},
			"required":["memory_id"],
			"additionalProperties":false
		}`),
	}
}
