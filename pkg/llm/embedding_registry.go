package llm

import (
	"fmt"
	"strings"

	"ex-otogi/pkg/otogi/ai"
)

// EmbeddingRegistry resolves configured embedding providers by stable profile
// key.
//
// The provider map is copied on construction and remains immutable afterward,
// so Resolve is concurrency-safe for parallel module workers.
type EmbeddingRegistry struct {
	providers map[string]ai.EmbeddingProvider
}

// NewEmbeddingRegistry constructs one immutable embedding provider registry.
func NewEmbeddingRegistry(providers map[string]ai.EmbeddingProvider) (*EmbeddingRegistry, error) {
	if len(providers) == 0 {
		return nil, fmt.Errorf("new embedding provider registry: empty providers")
	}

	cloned := make(map[string]ai.EmbeddingProvider, len(providers))
	for key, provider := range providers {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey == "" {
			return nil, fmt.Errorf("new embedding provider registry: empty provider key")
		}
		if provider == nil {
			return nil, fmt.Errorf("new embedding provider registry: provider %s is nil", trimmedKey)
		}
		if _, exists := cloned[trimmedKey]; exists {
			return nil, fmt.Errorf("new embedding provider registry: duplicate provider key %s", trimmedKey)
		}
		cloned[trimmedKey] = provider
	}

	return &EmbeddingRegistry{providers: cloned}, nil
}

// Resolve returns one configured embedding provider by key.
func (r *EmbeddingRegistry) Resolve(provider string) (ai.EmbeddingProvider, error) {
	if r == nil {
		return nil, fmt.Errorf("resolve embedding provider: nil registry")
	}

	trimmed := strings.TrimSpace(provider)
	if trimmed == "" {
		return nil, fmt.Errorf("resolve embedding provider: empty provider key")
	}

	resolved, exists := r.providers[trimmed]
	if !exists {
		return nil, fmt.Errorf("resolve embedding provider: provider %s is not configured", trimmed)
	}

	return resolved, nil
}

var _ ai.EmbeddingProviderRegistry = (*EmbeddingRegistry)(nil)
