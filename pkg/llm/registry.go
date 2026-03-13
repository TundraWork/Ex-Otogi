package llm

import (
	"fmt"
	"strings"

	"ex-otogi/pkg/otogi/ai"
)

// Registry resolves configured LLM providers by stable profile key.
//
// The provider map is copied on construction and remains immutable afterward,
// so Resolve is concurrency-safe for parallel module workers.
type Registry struct {
	providers map[string]ai.LLMProvider
}

// NewRegistry constructs one immutable LLM provider registry.
func NewRegistry(providers map[string]ai.LLMProvider) (*Registry, error) {
	if len(providers) == 0 {
		return nil, fmt.Errorf("new llm provider registry: empty providers")
	}

	cloned := make(map[string]ai.LLMProvider, len(providers))
	for key, provider := range providers {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey == "" {
			return nil, fmt.Errorf("new llm provider registry: empty provider key")
		}
		if provider == nil {
			return nil, fmt.Errorf("new llm provider registry: provider %s is nil", trimmedKey)
		}
		if _, exists := cloned[trimmedKey]; exists {
			return nil, fmt.Errorf("new llm provider registry: duplicate provider key %s", trimmedKey)
		}
		cloned[trimmedKey] = provider
	}

	return &Registry{providers: cloned}, nil
}

// Resolve returns one configured provider by key.
func (r *Registry) Resolve(provider string) (ai.LLMProvider, error) {
	if r == nil {
		return nil, fmt.Errorf("resolve llm provider: nil registry")
	}

	trimmed := strings.TrimSpace(provider)
	if trimmed == "" {
		return nil, fmt.Errorf("resolve llm provider: empty provider key")
	}

	resolved, exists := r.providers[trimmed]
	if !exists {
		return nil, fmt.Errorf("resolve llm provider: provider %s is not configured", trimmed)
	}

	return resolved, nil
}

var _ ai.LLMProviderRegistry = (*Registry)(nil)
