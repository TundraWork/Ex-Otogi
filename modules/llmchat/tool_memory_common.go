package llmchat

import (
	"context"
	"fmt"
	"strings"

	"ex-otogi/pkg/otogi/ai"
)

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
