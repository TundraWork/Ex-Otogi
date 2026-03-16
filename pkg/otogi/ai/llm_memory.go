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

const (
	// LLMMemoryMetadataImportance stores the memory importance score in legacy
	// metadata-backed records.
	LLMMemoryMetadataImportance = "importance"
	// LLMMemoryMetadataAccessCount stores the retrieval count in legacy
	// metadata-backed records.
	LLMMemoryMetadataAccessCount = "access_count"
	// LLMMemoryMetadataLastAccessed stores the last retrieval timestamp in
	// legacy metadata-backed records.
	LLMMemoryMetadataLastAccessed = "last_accessed"
	// LLMMemoryMetadataSource stores the origin label for one memory.
	LLMMemoryMetadataSource = "source"
	// LLMMemoryMetadataSourceArticleID stores the source article ID for one
	// memory.
	LLMMemoryMetadataSourceArticleID = "source_article_id"
	// LLMMemoryMetadataSourceActorID stores the source actor ID for one memory.
	LLMMemoryMetadataSourceActorID = "source_actor_id"
	// LLMMemoryMetadataSourceActorName stores the source actor name for one
	// memory.
	LLMMemoryMetadataSourceActorName = "source_actor_name"
	// LLMMemoryMetadataSourceActorIsBot stores whether the source actor is a bot.
	LLMMemoryMetadataSourceActorIsBot = "source_actor_is_bot"
	// LLMMemoryMetadataSubjectActorID stores the subject actor ID for one memory.
	LLMMemoryMetadataSubjectActorID = "subject_actor_id"
	// LLMMemoryMetadataSubjectActorName stores the subject actor name for one
	// memory.
	LLMMemoryMetadataSubjectActorName = "subject_actor_name"
	// LLMMemoryMetadataSubjectActorIsBot stores whether the subject actor is a
	// bot.
	LLMMemoryMetadataSubjectActorIsBot = "subject_actor_is_bot"
	// LLMMemoryMetadataSourceRecordIDs stores absorbed evidence record IDs.
	LLMMemoryMetadataSourceRecordIDs = "source_record_ids"
)

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
	// Update replaces the mutable fields of one existing entry and returns the
	// refreshed stored record.
	Update(ctx context.Context, update LLMMemoryUpdate) (LLMMemoryRecord, error)
	// Delete removes one entry by ID.
	Delete(ctx context.Context, id string) error
	// ListByScope returns stored entries for one scope, ordered by creation time
	// descending, up to limit when limit is greater than zero.
	ListByScope(ctx context.Context, scope LLMMemoryScope, limit int) ([]LLMMemoryRecord, error)
}

// LLMMemoryKind classifies the storage lifecycle of one memory record.
type LLMMemoryKind string

const (
	// LLMMemoryKindUnit identifies one directly extracted memory unit.
	LLMMemoryKindUnit LLMMemoryKind = "unit"
	// LLMMemoryKindSynthesized identifies one rewritten or consolidated memory.
	LLMMemoryKindSynthesized LLMMemoryKind = "synthesized"
)

// Validate checks whether the memory kind is supported.
func (k LLMMemoryKind) Validate() error {
	switch strings.TrimSpace(string(k)) {
	case "", string(LLMMemoryKindUnit), string(LLMMemoryKindSynthesized):
		return nil
	default:
		return fmt.Errorf("validate llm memory kind: unsupported kind %q", k)
	}
}

// LLMMemoryActorRef identifies one actor referenced by a memory record.
type LLMMemoryActorRef struct {
	// ID is the stable upstream actor identifier when known.
	ID string `json:"id,omitempty"`
	// Name is the human-readable actor display name when known.
	Name string `json:"name,omitempty"`
	// IsBot reports whether the referenced actor is a bot.
	IsBot bool `json:"is_bot,omitempty"`
}

// Validate checks whether the actor reference is meaningful when present.
func (a LLMMemoryActorRef) Validate() error {
	if strings.TrimSpace(a.ID) == "" && strings.TrimSpace(a.Name) == "" {
		return fmt.Errorf("validate llm memory actor ref: missing id and name")
	}

	return nil
}

// LLMMemoryProfile carries typed lifecycle and provenance details for one
// memory record.
type LLMMemoryProfile struct {
	// Kind records whether this memory is a directly extracted unit or a
	// synthesized canonical record.
	Kind LLMMemoryKind `json:"kind,omitempty"`
	// Importance stores the 1-10 salience score assigned to the memory.
	Importance int `json:"importance,omitempty"`
	// LastAccessedAt records when the memory was last retrieved for use.
	LastAccessedAt time.Time `json:"last_accessed_at,omitempty"`
	// AccessCount counts successful retrieval uses of this memory.
	AccessCount int `json:"access_count,omitempty"`
	// Source identifies the module or process that created the memory.
	Source string `json:"source,omitempty"`
	// SourceArticleID stores the originating article ID when known.
	SourceArticleID string `json:"source_article_id,omitempty"`
	// SourceActor identifies the actor who produced the originating article.
	SourceActor *LLMMemoryActorRef `json:"source_actor,omitempty"`
	// SubjectActor identifies the actor the memory is primarily about.
	SubjectActor *LLMMemoryActorRef `json:"subject_actor,omitempty"`
	// EvidenceRecordIDs stores absorbed supporting record IDs.
	EvidenceRecordIDs []string `json:"evidence_record_ids,omitempty"`
}

// Validate checks whether the typed profile is coherent.
func (p LLMMemoryProfile) Validate() error {
	if err := p.Kind.Validate(); err != nil {
		return fmt.Errorf("validate llm memory profile kind: %w", err)
	}
	if p.Importance < 0 || p.Importance > 10 {
		return fmt.Errorf("validate llm memory profile: importance must be between 0 and 10")
	}
	if p.AccessCount < 0 {
		return fmt.Errorf("validate llm memory profile: access_count must be >= 0")
	}
	if p.SourceActor != nil {
		if err := p.SourceActor.Validate(); err != nil {
			return fmt.Errorf("validate llm memory profile source_actor: %w", err)
		}
	}
	if p.SubjectActor != nil {
		if err := p.SubjectActor.Validate(); err != nil {
			return fmt.Errorf("validate llm memory profile subject_actor: %w", err)
		}
	}
	for index, recordID := range p.EvidenceRecordIDs {
		if strings.TrimSpace(recordID) == "" {
			return fmt.Errorf("validate llm memory profile evidence_record_ids[%d]: missing id", index)
		}
	}

	return nil
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
	// Profile carries typed lifecycle and provenance details.
	Profile LLMMemoryProfile
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
	if err := e.Profile.Validate(); err != nil {
		return fmt.Errorf("validate llm memory entry profile: %w", err)
	}

	return nil
}

// LLMMemoryUpdate describes one full mutable update to an existing memory
// entry.
type LLMMemoryUpdate struct {
	// ID identifies which stored record to replace.
	ID string
	// Content stores the canonical fact or knowledge text.
	Content string
	// Category groups memories by semantic purpose.
	Category string
	// Embedding stores the updated L2-normalized vector representation.
	Embedding []float32
	// Profile carries typed lifecycle and provenance details.
	Profile LLMMemoryProfile
	// Metadata carries optional provider-agnostic context.
	Metadata map[string]string
}

// Validate checks that an update payload is complete enough to apply.
func (u LLMMemoryUpdate) Validate() error {
	if strings.TrimSpace(u.ID) == "" {
		return fmt.Errorf("validate llm memory update: missing id")
	}
	if strings.TrimSpace(u.Content) == "" {
		return fmt.Errorf("validate llm memory update: missing content")
	}
	if strings.TrimSpace(u.Category) == "" {
		return fmt.Errorf("validate llm memory update: missing category")
	}
	if err := validateLLMMemoryEmbedding(u.Embedding); err != nil {
		return fmt.Errorf("validate llm memory update embedding: %w", err)
	}
	if err := u.Profile.Validate(); err != nil {
		return fmt.Errorf("validate llm memory update profile: %w", err)
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
	// Profile carries typed lifecycle and provenance details.
	Profile LLMMemoryProfile
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
