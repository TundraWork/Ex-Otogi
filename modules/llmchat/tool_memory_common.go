package llmchat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"ex-otogi/pkg/otogi/ai"
)

const (
	memoryToolCategoryUserFact   = "user_fact"
	memoryToolCategoryPreference = "preference"
	memoryToolCategoryKnowledge  = "knowledge"
	memoryToolCategoryExperience = "experience"
)

func buildSemanticMemoryToolRegistry(
	scope ai.LLMMemoryScope,
	embeddingProvider ai.EmbeddingProvider,
	memoryService ai.LLMMemoryService,
) *ToolRegistry {
	if embeddingProvider == nil || memoryService == nil {
		return nil
	}

	return NewToolRegistry([]ToolHandler{
		newRememberTool(scope, embeddingProvider, memoryService),
		newRecallTool(scope, embeddingProvider, memoryService),
		newForgetTool(memoryService),
	})
}

func decodeToolArgsJSON(raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode tool arguments: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("decode tool arguments: unexpected trailing content")
		}
		return fmt.Errorf("decode tool arguments trailing json: %w", err)
	}

	return nil
}

func embedSingleText(
	ctx context.Context,
	embeddingProvider ai.EmbeddingProvider,
	text string,
	taskType ai.EmbeddingTaskType,
) ([]float32, error) {
	if ctx == nil {
		return nil, fmt.Errorf("embed single text: nil context")
	}
	if embeddingProvider == nil {
		return nil, fmt.Errorf("embed single text: embedding provider is nil")
	}

	response, err := embeddingProvider.Embed(ctx, ai.EmbeddingRequest{
		Texts:    []string{strings.TrimSpace(text)},
		TaskType: taskType,
	})
	if err != nil {
		return nil, fmt.Errorf("embed single text: %w", err)
	}
	if len(response.Vectors) != 1 {
		return nil, fmt.Errorf("embed single text: expected 1 vector, got %d", len(response.Vectors))
	}

	return append([]float32(nil), response.Vectors[0]...), nil
}
