package ai

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"
)

// ServiceSemanticStore is the canonical service registry key for semantic memory
// lookups and mutations.
const ServiceSemanticStore = "otogi.semantic_store"

// SemanticStore provides semantic knowledge storage and retrieval using
// vector embeddings.
//
// Implementations must be concurrency-safe because modules can resolve and
// mutate memory from multiple workers at the same time.
type SemanticStore interface {
	// Store persists one knowledge entry with its embedding vector.
	Store(ctx context.Context, entry SemanticEntry) (SemanticRecord, error)
	// Search finds entries whose embeddings are semantically similar to the
	// query embedding within one conversation scope.
	Search(ctx context.Context, query SemanticQuery) ([]SemanticMatch, error)
	// Update replaces the mutable fields of one existing entry and returns the
	// refreshed stored record.
	Update(ctx context.Context, update SemanticUpdate) (SemanticRecord, error)
	// Delete removes one entry by ID.
	Delete(ctx context.Context, id string) error
	// ListByScope returns stored entries for one scope, ordered by creation time
	// descending, up to limit when limit is greater than zero.
	ListByScope(ctx context.Context, scope SemanticScope, limit int) ([]SemanticRecord, error)
}

// SemanticKind classifies the storage lifecycle of one memory record.
type SemanticKind string

const (
	// SemanticKindUnit identifies one directly extracted memory unit.
	SemanticKindUnit SemanticKind = "unit"
	// SemanticKindSynthesized identifies one rewritten or consolidated memory.
	SemanticKindSynthesized SemanticKind = "synthesized"
)

// Validate checks whether the memory kind is supported.
func (k SemanticKind) Validate() error {
	switch strings.TrimSpace(string(k)) {
	case "", string(SemanticKindUnit), string(SemanticKindSynthesized):
		return nil
	default:
		return fmt.Errorf("validate semantic kind: unsupported kind %q", k)
	}
}

// SemanticLink describes one directional relationship from the owning
// record to another record in the same scope.
type SemanticLink struct {
	// TargetID identifies the linked record.
	TargetID string `json:"target_id"`
	// Relation classifies the link type: "related", "refines", or
	// "supersedes".
	Relation string `json:"relation,omitempty"`
}

// Validate checks whether the link reference is valid.
func (l SemanticLink) Validate() error {
	if strings.TrimSpace(l.TargetID) == "" {
		return fmt.Errorf("validate semantic link: missing target_id")
	}
	switch strings.TrimSpace(l.Relation) {
	case "", "related", "refines", "supersedes":
		return nil
	default:
		return fmt.Errorf("validate semantic link: unsupported relation %q", l.Relation)
	}
}

// SemanticActorRef identifies one actor referenced by a memory record.
type SemanticActorRef struct {
	// ID is the stable upstream actor identifier when known.
	ID string `json:"id,omitempty"`
	// Name is the human-readable actor display name when known.
	Name string `json:"name,omitempty"`
	// IsBot reports whether the referenced actor is a bot.
	IsBot bool `json:"is_bot,omitempty"`
}

// Validate checks whether the actor reference is meaningful when present.
func (a SemanticActorRef) Validate() error {
	if strings.TrimSpace(a.ID) == "" && strings.TrimSpace(a.Name) == "" {
		return fmt.Errorf("validate semantic actor ref: missing id and name")
	}

	return nil
}

// SemanticProfile carries typed lifecycle and provenance details for one
// memory record.
type SemanticProfile struct {
	// Kind records whether this memory is a directly extracted unit or a
	// synthesized canonical record.
	Kind SemanticKind `json:"kind,omitempty"`
	// Importance stores the 1-10 salience score assigned to the memory.
	Importance int `json:"importance,omitempty"`
	// Source identifies the module or process that created the memory.
	Source string `json:"source,omitempty"`
	// SourceArticleID stores the originating article ID when known.
	SourceArticleID string `json:"source_article_id,omitempty"`
	// SourceActor identifies the actor who produced the originating article.
	SourceActor *SemanticActorRef `json:"source_actor,omitempty"`
	// SubjectActor identifies the actor the memory is primarily about.
	SubjectActor *SemanticActorRef `json:"subject_actor,omitempty"`
	// EvidenceRecordIDs stores absorbed supporting record IDs.
	EvidenceRecordIDs []string `json:"evidence_record_ids,omitempty"`
	// ValidUntil records the expiration time for time-bounded memories.
	// Nil means the memory has no expiration.
	ValidUntil *time.Time `json:"valid_until,omitempty"`
}

// Validate checks whether the typed profile is coherent.
func (p SemanticProfile) Validate() error {
	if err := p.Kind.Validate(); err != nil {
		return fmt.Errorf("validate semantic profile kind: %w", err)
	}
	if p.Importance < 0 || p.Importance > 10 {
		return fmt.Errorf("validate semantic profile: importance must be between 0 and 10")
	}
	if p.SourceActor != nil {
		if err := p.SourceActor.Validate(); err != nil {
			return fmt.Errorf("validate semantic profile source_actor: %w", err)
		}
	}
	if p.SubjectActor != nil {
		if err := p.SubjectActor.Validate(); err != nil {
			return fmt.Errorf("validate semantic profile subject_actor: %w", err)
		}
	}
	for index, recordID := range p.EvidenceRecordIDs {
		if strings.TrimSpace(recordID) == "" {
			return fmt.Errorf("validate semantic profile evidence_record_ids[%d]: missing id", index)
		}
	}

	return nil
}

// SemanticScope identifies the conversation-bound namespace for semantic
// memories.
type SemanticScope struct {
	// TenantID scopes memories for multi-tenant deployments.
	TenantID string
	// Platform identifies which upstream platform owns this conversation scope.
	Platform string
	// ConversationID identifies the shared conversation namespace.
	ConversationID string
}

// Validate checks that mandatory scope fields are present.
func (s SemanticScope) Validate() error {
	if strings.TrimSpace(s.Platform) == "" {
		return fmt.Errorf("validate semantic scope: missing platform")
	}
	if strings.TrimSpace(s.ConversationID) == "" {
		return fmt.Errorf("validate semantic scope: missing conversation id")
	}

	return nil
}

// SemanticEntry describes one memory entry to be stored.
type SemanticEntry struct {
	// Scope identifies which conversation namespace owns the memory.
	Scope SemanticScope
	// Content stores the fact or knowledge text.
	Content string
	// Category groups memories by semantic purpose.
	Category string
	// Embedding stores one L2-normalized vector representation of Content.
	Embedding []float32
	// Profile carries typed lifecycle and provenance details.
	Profile SemanticProfile
	// Keywords carries key terms extracted alongside the memory content.
	Keywords []string
	// Tags carries categorical labels extracted alongside the memory content.
	Tags []string
	// Links stores directional relationships to other records in the same
	// scope.
	Links []SemanticLink
}

// Validate checks that a memory entry is complete enough to store.
func (e SemanticEntry) Validate() error {
	if err := e.Scope.Validate(); err != nil {
		return fmt.Errorf("validate semantic entry: %w", err)
	}
	if strings.TrimSpace(e.Content) == "" {
		return fmt.Errorf("validate semantic entry: missing content")
	}
	if strings.TrimSpace(e.Category) == "" {
		return fmt.Errorf("validate semantic entry: missing category")
	}
	if err := validateSemanticEmbedding(e.Embedding); err != nil {
		return fmt.Errorf("validate semantic entry embedding: %w", err)
	}
	if err := e.Profile.Validate(); err != nil {
		return fmt.Errorf("validate semantic entry profile: %w", err)
	}

	return nil
}

// SemanticUpdate describes one full mutable update to an existing memory
// entry.
type SemanticUpdate struct {
	// ID identifies which stored record to replace.
	ID string
	// Content stores the canonical fact or knowledge text.
	Content string
	// Category groups memories by semantic purpose.
	Category string
	// Embedding stores the updated L2-normalized vector representation.
	Embedding []float32
	// Profile carries typed lifecycle and provenance details.
	Profile SemanticProfile
	// Keywords carries key terms extracted alongside the memory content.
	Keywords []string
	// Tags carries categorical labels extracted alongside the memory content.
	Tags []string
	// Links stores directional relationships to other records in the same
	// scope.
	Links []SemanticLink
}

// Validate checks that an update payload is complete enough to apply.
func (u SemanticUpdate) Validate() error {
	if strings.TrimSpace(u.ID) == "" {
		return fmt.Errorf("validate semantic update: missing id")
	}
	if strings.TrimSpace(u.Content) == "" {
		return fmt.Errorf("validate semantic update: missing content")
	}
	if strings.TrimSpace(u.Category) == "" {
		return fmt.Errorf("validate semantic update: missing category")
	}
	if err := validateSemanticEmbedding(u.Embedding); err != nil {
		return fmt.Errorf("validate semantic update embedding: %w", err)
	}
	if err := u.Profile.Validate(); err != nil {
		return fmt.Errorf("validate semantic update profile: %w", err)
	}

	return nil
}

// SemanticRecord is one persisted memory entry with stable identity and
// timestamps.
type SemanticRecord struct {
	// ID uniquely identifies the stored memory record.
	ID string
	// Scope identifies which conversation namespace owns the memory.
	Scope SemanticScope
	// Content stores the fact or knowledge text.
	Content string
	// Category groups memories by semantic purpose.
	Category string
	// Embedding stores one L2-normalized vector representation of Content.
	Embedding []float32
	// Profile carries typed lifecycle and provenance details.
	Profile SemanticProfile
	// Keywords carries key terms extracted alongside the memory content.
	Keywords []string
	// Tags carries categorical labels extracted alongside the memory content.
	Tags []string
	// Links stores directional relationships to other records in the same
	// scope.
	Links []SemanticLink
	// CreatedAt records when this memory was first stored.
	CreatedAt time.Time
	// UpdatedAt records when this memory was last modified.
	UpdatedAt time.Time
}

// SemanticQuery describes one semantic memory search.
type SemanticQuery struct {
	// Scope identifies which conversation namespace to search.
	Scope SemanticScope
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
func (q SemanticQuery) Validate() error {
	if err := q.Scope.Validate(); err != nil {
		return fmt.Errorf("validate semantic query: %w", err)
	}
	if err := validateSemanticEmbedding(q.Embedding); err != nil {
		return fmt.Errorf("validate semantic query embedding: %w", err)
	}
	if q.Limit < 0 {
		return fmt.Errorf("validate semantic query: limit must be >= 0")
	}
	if q.MinSimilarity < 0 || q.MinSimilarity > 1 {
		return fmt.Errorf("validate semantic query: min_similarity must be between 0 and 1")
	}

	return nil
}

// SemanticMatch is one semantic search hit.
type SemanticMatch struct {
	// Record is the matched memory record.
	Record SemanticRecord
	// Similarity is the dot-product similarity score between the query vector
	// and the record embedding.
	Similarity float32
}

func validateSemanticEmbedding(embedding []float32) error {
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
