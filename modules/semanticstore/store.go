package semanticstore

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"ex-otogi/pkg/otogi/ai"

	"github.com/google/uuid"
)

const (
	defaultSearchLimit   = 5
	defaultMinSimilarity = 0.3
)

// Store is a concurrency-safe in-memory semantic memory store.
type Store struct {
	mu      sync.RWMutex
	records map[string]*ai.SemanticRecord
	scopes  map[ai.SemanticScope][]string
	clock   func() time.Time
	newID   func() string
}

func newStore(clock func() time.Time, newID func() string) *Store {
	if clock == nil {
		clock = time.Now
	}
	if newID == nil {
		newID = uuid.NewString
	}

	return &Store{
		records: make(map[string]*ai.SemanticRecord),
		scopes:  make(map[ai.SemanticScope][]string),
		clock:   clock,
		newID:   newID,
	}
}

// Store persists one semantic memory entry.
func (s *Store) Store(ctx context.Context, entry ai.SemanticEntry) (ai.SemanticRecord, error) {
	if err := ctx.Err(); err != nil {
		return ai.SemanticRecord{}, fmt.Errorf("semanticstore store: %w", err)
	}
	if err := entry.Validate(); err != nil {
		return ai.SemanticRecord{}, fmt.Errorf("semanticstore store: %w", err)
	}

	now := s.now()
	record := ai.SemanticRecord{
		ID:        s.newID(),
		Scope:     entry.Scope,
		Content:   strings.TrimSpace(entry.Content),
		Category:  strings.TrimSpace(entry.Category),
		Embedding: cloneEmbedding(entry.Embedding),
		Profile:   cloneProfile(entry.Profile),
		Keywords:  cloneStrings(entry.Keywords),
		Tags:      cloneStrings(entry.Tags),
		Links:     cloneLinks(entry.Links),
		CreatedAt: now,
		UpdatedAt: now,
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.records[record.ID] = &record
	s.scopes[record.Scope] = append(s.scopes[record.Scope], record.ID)

	return cloneRecord(record), nil
}

// Search finds semantic memory matches within one scope.
func (s *Store) Search(ctx context.Context, query ai.SemanticQuery) ([]ai.SemanticMatch, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("semanticstore search: %w", err)
	}
	if err := query.Validate(); err != nil {
		return nil, fmt.Errorf("semanticstore search: %w", err)
	}

	effective := resolveQueryDefaults(query)

	now := s.now()

	s.mu.RLock()
	ids := append([]string(nil), s.scopes[effective.Scope]...)
	matches := make([]ai.SemanticMatch, 0, len(ids))
	for _, id := range ids {
		record := s.records[id]
		if record == nil {
			continue
		}
		if record.Profile.ValidUntil != nil && record.Profile.ValidUntil.Before(now) {
			continue
		}
		if len(record.Embedding) != len(effective.Embedding) {
			continue
		}
		similarity := dotProduct(effective.Embedding, record.Embedding)
		if similarity < effective.MinSimilarity {
			continue
		}
		matches = append(matches, ai.SemanticMatch{
			Record:     cloneRecord(*record),
			Similarity: similarity,
		})
	}
	s.mu.RUnlock()

	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Similarity == matches[j].Similarity {
			return matches[i].Record.CreatedAt.After(matches[j].Record.CreatedAt)
		}
		return matches[i].Similarity > matches[j].Similarity
	})

	if effective.Limit > 0 && len(matches) > effective.Limit {
		matches = matches[:effective.Limit]
	}

	return matches, nil
}

// Update replaces the mutable fields of one stored memory record.
func (s *Store) Update(ctx context.Context, update ai.SemanticUpdate) (ai.SemanticRecord, error) {
	if err := ctx.Err(); err != nil {
		return ai.SemanticRecord{}, fmt.Errorf("semanticstore update: %w", err)
	}
	if err := update.Validate(); err != nil {
		return ai.SemanticRecord{}, fmt.Errorf("semanticstore update: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	record, exists := s.records[strings.TrimSpace(update.ID)]
	if !exists {
		return ai.SemanticRecord{}, fmt.Errorf(
			"semanticstore update: record %s not found",
			strings.TrimSpace(update.ID),
		)
	}

	record.Content = strings.TrimSpace(update.Content)
	record.Category = strings.TrimSpace(update.Category)
	record.Embedding = cloneEmbedding(update.Embedding)
	record.Profile = cloneProfile(update.Profile)
	record.Keywords = cloneStrings(update.Keywords)
	record.Tags = cloneStrings(update.Tags)
	record.Links = cloneLinks(update.Links)
	record.UpdatedAt = s.now()

	return cloneRecord(*record), nil
}

// Delete removes one stored memory record by ID.
func (s *Store) Delete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("semanticstore delete: %w", err)
	}
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("semanticstore delete: missing id")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.records[strings.TrimSpace(id)]; !exists {
		return fmt.Errorf("semanticstore delete: record %s not found", strings.TrimSpace(id))
	}
	s.removeRecordLocked(strings.TrimSpace(id))

	return nil
}

// ListByScope returns stored memory records for one scope ordered by creation
// time descending.
func (s *Store) ListByScope(ctx context.Context, scope ai.SemanticScope, limit int) ([]ai.SemanticRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("semanticstore list by scope: %w", err)
	}
	if err := scope.Validate(); err != nil {
		return nil, fmt.Errorf("semanticstore list by scope: %w", err)
	}
	if limit < 0 {
		return nil, fmt.Errorf("semanticstore list by scope: limit must be >= 0")
	}

	s.mu.RLock()
	ids := append([]string(nil), s.scopes[scope]...)
	records := make([]ai.SemanticRecord, 0, len(ids))
	for _, id := range ids {
		record := s.records[id]
		if record == nil {
			continue
		}
		records = append(records, cloneRecord(*record))
	}
	s.mu.RUnlock()

	sort.Slice(records, func(i, j int) bool {
		if records[i].CreatedAt.Equal(records[j].CreatedAt) {
			return records[i].ID > records[j].ID
		}
		return records[i].CreatedAt.After(records[j].CreatedAt)
	})

	if limit > 0 && len(records) > limit {
		records = records[:limit]
	}

	return records, nil
}

func (s *Store) removeRecordLocked(id string) {
	record := s.records[id]
	if record == nil {
		return
	}

	scope := record.Scope
	ids := s.scopes[scope]
	for index := range ids {
		if ids[index] != id {
			continue
		}
		ids = append(ids[:index], ids[index+1:]...)
		break
	}
	if len(ids) == 0 {
		delete(s.scopes, scope)
	} else {
		s.scopes[scope] = ids
	}
	delete(s.records, id)
}

func (s *Store) now() time.Time {
	return s.clock().UTC()
}

func resolveQueryDefaults(query ai.SemanticQuery) ai.SemanticQuery {
	effective := query
	if effective.Limit == 0 {
		effective.Limit = defaultSearchLimit
	}
	if effective.MinSimilarity == 0 {
		effective.MinSimilarity = defaultMinSimilarity
	}

	return effective
}

func dotProduct(a, b []float32) float32 {
	if len(a) != len(b) {
		return 0
	}

	var sum float32
	for index := range a {
		sum += a[index] * b[index]
	}

	return sum
}

func cloneRecord(record ai.SemanticRecord) ai.SemanticRecord {
	record.Embedding = cloneEmbedding(record.Embedding)
	record.Profile = cloneProfile(record.Profile)
	record.Keywords = cloneStrings(record.Keywords)
	record.Tags = cloneStrings(record.Tags)
	record.Links = cloneLinks(record.Links)
	return record
}

func cloneProfile(profile ai.SemanticProfile) ai.SemanticProfile {
	profile.SourceActor = cloneActorRef(profile.SourceActor)
	profile.SubjectActor = cloneActorRef(profile.SubjectActor)
	if len(profile.EvidenceRecordIDs) > 0 {
		profile.EvidenceRecordIDs = append([]string(nil), profile.EvidenceRecordIDs...)
	}
	if profile.ValidUntil != nil {
		cloned := profile.ValidUntil.UTC()
		profile.ValidUntil = &cloned
	}
	return profile
}

func cloneActorRef(actor *ai.SemanticActorRef) *ai.SemanticActorRef {
	if actor == nil {
		return nil
	}

	cloned := *actor
	cloned.ID = strings.TrimSpace(cloned.ID)
	cloned.Name = strings.TrimSpace(cloned.Name)

	return &cloned
}

func cloneEmbedding(embedding []float32) []float32 {
	if len(embedding) == 0 {
		return nil
	}

	return append([]float32(nil), embedding...)
}

func cloneStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}

	return append([]string(nil), values...)
}

func cloneLinks(links []ai.SemanticLink) []ai.SemanticLink {
	if len(links) == 0 {
		return nil
	}

	return append([]ai.SemanticLink(nil), links...)
}

var _ ai.SemanticStore = (*Store)(nil)
