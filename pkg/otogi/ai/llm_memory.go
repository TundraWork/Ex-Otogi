package ai

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"
)

// ServiceLLMMemory is the canonical service registry key for semantic memory
// lookups and mutations.
const ServiceLLMMemory = "otogi.llm_memory"

// LLMMemoryService provides semantic knowledge storage and retrieval using
// vector embeddings.
//
// Implementations must be concurrency-safe because modules can resolve and
// mutate memory from multiple workers at the same time.
type LLMMemoryService interface {
	// Store persists one knowledge entry with its embedding vector.
	Store(ctx context.Context, entry LLMMemoryEntry) (LLMMemoryRecord, error)
	// Search finds entries whose embeddings are semantically similar to the
	// query embedding within one conversation scope.
	Search(ctx context.Context, query LLMMemoryQuery) ([]LLMMemoryMatch, error)
	// Update replaces the content and embedding of one existing entry.
	Update(ctx context.Context, id string, content string, embedding []float32) error
	// Delete removes one entry by ID.
	Delete(ctx context.Context, id string) error
	// ListByScope returns stored entries for one scope, ordered by creation time
	// descending, up to limit when limit is greater than zero.
	ListByScope(ctx context.Context, scope LLMMemoryScope, limit int) ([]LLMMemoryRecord, error)
}

// LLMMemoryScope identifies the conversation-bound namespace for semantic
// memories.
type LLMMemoryScope struct {
	// TenantID scopes memories for multi-tenant deployments.
	TenantID string
	// Platform identifies which upstream platform owns this conversation scope.
	Platform string
	// ConversationID identifies the shared conversation namespace.
	ConversationID string
}

// Validate checks that mandatory scope fields are present.
func (s LLMMemoryScope) Validate() error {
	if strings.TrimSpace(s.Platform) == "" {
		return fmt.Errorf("validate llm memory scope: missing platform")
	}
	if strings.TrimSpace(s.ConversationID) == "" {
		return fmt.Errorf("validate llm memory scope: missing conversation id")
	}

	return nil
}

// LLMMemoryEntry describes one memory entry to be stored.
type LLMMemoryEntry struct {
	// Scope identifies which conversation namespace owns the memory.
	Scope LLMMemoryScope
	// Content stores the fact or knowledge text.
	Content string
	// Category groups memories by semantic purpose.
	Category string
	// Embedding stores one L2-normalized vector representation of Content.
	Embedding []float32
	// Metadata carries optional provider-agnostic context.
	Metadata map[string]string
}

// Validate checks that a memory entry is complete enough to store.
func (e LLMMemoryEntry) Validate() error {
	if err := e.Scope.Validate(); err != nil {
		return fmt.Errorf("validate llm memory entry: %w", err)
	}
	if strings.TrimSpace(e.Content) == "" {
		return fmt.Errorf("validate llm memory entry: missing content")
	}
	if strings.TrimSpace(e.Category) == "" {
		return fmt.Errorf("validate llm memory entry: missing category")
	}
	if err := validateLLMMemoryEmbedding(e.Embedding); err != nil {
		return fmt.Errorf("validate llm memory entry embedding: %w", err)
	}

	return nil
}

// LLMMemoryRecord is one persisted memory entry with stable identity and
// timestamps.
type LLMMemoryRecord struct {
	// ID uniquely identifies the stored memory record.
	ID string
	// Scope identifies which conversation namespace owns the memory.
	Scope LLMMemoryScope
	// Content stores the fact or knowledge text.
	Content string
	// Category groups memories by semantic purpose.
	Category string
	// Embedding stores one L2-normalized vector representation of Content.
	Embedding []float32
	// Metadata carries optional provider-agnostic context.
	Metadata map[string]string
	// CreatedAt records when this memory was first stored.
	CreatedAt time.Time
	// UpdatedAt records when this memory was last modified.
	UpdatedAt time.Time
}

// LLMMemoryQuery describes one semantic memory search.
type LLMMemoryQuery struct {
	// Scope identifies which conversation namespace to search.
	Scope LLMMemoryScope
	// Embedding stores the L2-normalized query vector.
	Embedding []float32
	// Limit caps the number of returned matches.
	//
	// Zero lets implementations apply their default.
	Limit int
	// MinSimilarity sets the similarity threshold used to filter results.
	//
	// Zero lets implementations apply their default.
	MinSimilarity float32
}

// Validate checks that a memory search query is coherent.
func (q LLMMemoryQuery) Validate() error {
	if err := q.Scope.Validate(); err != nil {
		return fmt.Errorf("validate llm memory query: %w", err)
	}
	if err := validateLLMMemoryEmbedding(q.Embedding); err != nil {
		return fmt.Errorf("validate llm memory query embedding: %w", err)
	}
	if q.Limit < 0 {
		return fmt.Errorf("validate llm memory query: limit must be >= 0")
	}
	if q.MinSimilarity < 0 || q.MinSimilarity > 1 {
		return fmt.Errorf("validate llm memory query: min_similarity must be between 0 and 1")
	}

	return nil
}

// LLMMemoryMatch is one semantic search hit.
type LLMMemoryMatch struct {
	// Record is the matched memory record.
	Record LLMMemoryRecord
	// Similarity is the dot-product similarity score between the query vector
	// and the record embedding.
	Similarity float32
}

func validateLLMMemoryEmbedding(embedding []float32) error {
	if len(embedding) == 0 {
		return fmt.Errorf("missing embedding")
	}

	var sumSquares float64
	for index, value := range embedding {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return fmt.Errorf("embedding[%d] is not finite", index)
		}
		sumSquares += float64(value) * float64(value)
	}
	if sumSquares == 0 {
		return fmt.Errorf("embedding has zero magnitude")
	}

	return nil
}
