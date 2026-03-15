package ai

import (
	"context"
	"fmt"
	"strings"
)

// ServiceEmbeddingProviderRegistry is the canonical service registry key for
// embedding providers.
const ServiceEmbeddingProviderRegistry = "otogi.embedding_provider_registry"

// EmbeddingProviderRegistry resolves embedding providers by stable provider
// name.
//
// Implementations must be concurrency-safe because modules can resolve
// providers from multiple workers at the same time.
type EmbeddingProviderRegistry interface {
	// Resolve returns one configured embedding provider by name.
	Resolve(provider string) (EmbeddingProvider, error)
}

// EmbeddingProvider generates embedding vectors from text.
//
// Implementations should keep provider-specific transport details hidden
// behind this interface.
type EmbeddingProvider interface {
	// Embed generates one embedding vector per input text.
	//
	// Returned vectors must be L2-normalized so dot product can be used as cosine
	// similarity.
	Embed(ctx context.Context, req EmbeddingRequest) (EmbeddingResponse, error)
}

// EmbeddingTaskType identifies the intended use of an embedding request.
type EmbeddingTaskType string

const (
	// EmbeddingTaskTypeDocument marks text being stored for later retrieval.
	EmbeddingTaskTypeDocument EmbeddingTaskType = "document"
	// EmbeddingTaskTypeQuery marks text being embedded as a retrieval query.
	EmbeddingTaskTypeQuery EmbeddingTaskType = "query"
)

// Validate checks whether the task type value is supported.
func (t EmbeddingTaskType) Validate() error {
	switch t {
	case "", EmbeddingTaskTypeDocument, EmbeddingTaskTypeQuery:
		return nil
	default:
		return fmt.Errorf("validate embedding task type: unsupported value %q", t)
	}
}

// EmbeddingRequest describes one embedding generation call.
type EmbeddingRequest struct {
	// Model identifies which embedding model should be used.
	Model string
	// Texts is the ordered list of input texts to embed.
	Texts []string
	// Dimensions optionally truncates the returned vectors to this size.
	//
	// Zero uses the provider default.
	Dimensions int
	// TaskType optionally hints how the embeddings will be used.
	TaskType EmbeddingTaskType
}

// Validate checks one embedding request contract.
func (r EmbeddingRequest) Validate() error {
	if strings.TrimSpace(r.Model) == "" {
		return fmt.Errorf("validate embedding request: missing model")
	}
	if len(r.Texts) == 0 {
		return fmt.Errorf("validate embedding request: missing texts")
	}
	for index, text := range r.Texts {
		if strings.TrimSpace(text) == "" {
			return fmt.Errorf("validate embedding request texts[%d]: missing text", index)
		}
	}
	if r.Dimensions < 0 {
		return fmt.Errorf("validate embedding request: dimensions must be >= 0")
	}
	if err := r.TaskType.Validate(); err != nil {
		return fmt.Errorf("validate embedding request: %w", err)
	}

	return nil
}

// EmbeddingResponse contains the generated embedding vectors.
type EmbeddingResponse struct {
	// Vectors contains one L2-normalized vector per input text, in the same
	// order as the request.
	Vectors [][]float32
}
