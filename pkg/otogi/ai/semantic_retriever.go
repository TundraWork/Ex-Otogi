package ai

import (
	"context"
	"fmt"
	"strings"
)

// ServiceSemanticRetriever is the canonical service registry key for semantic
// memory retrieval.
const ServiceSemanticRetriever = "otogi.semantic_retriever"

// SemanticRetriever produces serialized semantic memory context for one
// conversation-scoped query.
//
// Implementations plan, search, rank, filter, and render relevant memories
// against the underlying SemanticStore. Consumers supply a pre-computed actor
// and reply-root context at the boundary so the contract remains free of
// upstream platform types.
//
// Implementations must be concurrency-safe because modules can retrieve from
// multiple workers at the same time.
type SemanticRetriever interface {
	// Retrieve plans, searches, ranks, and renders relevant semantic memories
	// for one request.
	//
	// Implementations must return an empty result without error when the
	// retriever is not configured to serve the request — for example when
	// retrieval is disabled, the request omits the prompt or embedding
	// provider name, the backing store or embedding registry is not wired,
	// or planning and filtering legitimately produce zero matches — so
	// callers can continue without memory context.
	//
	// Errors are reserved for configured-but-failed operations, such as a
	// named embedding provider that fails to resolve, a planner call that
	// fails after its retry budget, or a backing store search that fails.
	Retrieve(ctx context.Context, req SemanticRetrievalRequest) (SemanticRetrievalResult, error)
	// Available reports whether the retriever can serve a request that would
	// use the named embedding provider.
	//
	// Callers use this to decide whether a request is viable before building
	// heavier context.
	Available(embeddingProvider string) bool
}

// SemanticRetrievalPolicy configures per-call retrieval bounds.
//
// Zero-valued fields let implementations apply their own defaults.
type SemanticRetrievalPolicy struct {
	// MaxRetrievedMemories caps how many memories are returned after ranking.
	MaxRetrievedMemories int
	// MinSimilarity filters search hits below this similarity score.
	MinSimilarity float32
	// MaxMemoryRunes caps the serialized size of the rendered result content.
	MaxMemoryRunes int
}

// Validate checks one retrieval policy contract.
func (p SemanticRetrievalPolicy) Validate() error {
	if p.MaxRetrievedMemories < 0 {
		return fmt.Errorf("validate semantic retrieval policy: max_retrieved_memories must be >= 0")
	}
	if p.MinSimilarity < 0 || p.MinSimilarity > 1 {
		return fmt.Errorf("validate semantic retrieval policy: min_similarity must be between 0 and 1")
	}
	if p.MaxMemoryRunes < 0 {
		return fmt.Errorf("validate semantic retrieval policy: max_memory_runes must be >= 0")
	}

	return nil
}

// SemanticRetrievalRequest describes one semantic memory retrieval call.
type SemanticRetrievalRequest struct {
	// Scope identifies the conversation namespace to search.
	Scope SemanticScope
	// Prompt is the current user message text used to derive search queries.
	Prompt string
	// EmbeddingProvider names which embedding provider profile the retriever
	// should use for this request.
	EmbeddingProvider string
	// Policy carries the per-call retrieval bounds.
	Policy SemanticRetrievalPolicy
	// CurrentActor identifies the active speaker. Used for actor-weighted
	// ranking.
	CurrentActor SemanticActorRef
	// RelatedActors lists other actors present in the reply chain. Used for
	// actor-weighted ranking.
	RelatedActors []SemanticActorRef
	// ReplyRootSummary carries a short normalized summary of the reply thread
	// root, used by the retrieval planner when the current prompt is a
	// follow-up.
	ReplyRootSummary string
}

// Validate checks one retrieval request contract.
func (r SemanticRetrievalRequest) Validate() error {
	if err := r.Scope.Validate(); err != nil {
		return fmt.Errorf("validate semantic retrieval request: %w", err)
	}
	if strings.TrimSpace(r.Prompt) == "" {
		return fmt.Errorf("validate semantic retrieval request: missing prompt")
	}
	if strings.TrimSpace(r.EmbeddingProvider) == "" {
		return fmt.Errorf("validate semantic retrieval request: missing embedding_provider")
	}
	if err := r.Policy.Validate(); err != nil {
		return fmt.Errorf("validate semantic retrieval request: %w", err)
	}
	for index, actor := range r.RelatedActors {
		if err := actor.Validate(); err != nil {
			return fmt.Errorf("validate semantic retrieval request related_actors[%d]: %w", index, err)
		}
	}

	return nil
}

// SemanticRetrievalResult carries the rendered context produced by a retriever.
type SemanticRetrievalResult struct {
	// Content is the serialized memory context ready for inclusion in an LLM
	// request. Empty when no matches pass the filters.
	Content string
	// MatchCount reports how many memory records contributed to Content.
	MatchCount int
}
