package llmmemory

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
	mu         sync.RWMutex
	records    map[string]*ai.LLMMemoryRecord
	scopes     map[ai.LLMMemoryScope][]string
	clock      func() time.Time
	newID      func() string
	maxEntries int
}

func newStore(maxEntries int, clock func() time.Time, newID func() string) *Store {
	if maxEntries <= 0 {
		maxEntries = defaultMaxEntries
	}
	if clock == nil {
		clock = time.Now
	}
	if newID == nil {
		newID = uuid.NewString
	}

	return &Store{
		records:    make(map[string]*ai.LLMMemoryRecord),
		scopes:     make(map[ai.LLMMemoryScope][]string),
		clock:      clock,
		newID:      newID,
		maxEntries: maxEntries,
	}
}

// Store persists one semantic memory entry.
func (s *Store) Store(ctx context.Context, entry ai.LLMMemoryEntry) (ai.LLMMemoryRecord, error) {
	if err := ctx.Err(); err != nil {
		return ai.LLMMemoryRecord{}, fmt.Errorf("llmmemory store: %w", err)
	}
	if err := entry.Validate(); err != nil {
		return ai.LLMMemoryRecord{}, fmt.Errorf("llmmemory store: %w", err)
	}

	now := s.now()
	record := ai.LLMMemoryRecord{
		ID:        s.newID(),
		Scope:     entry.Scope,
		Content:   strings.TrimSpace(entry.Content),
		Category:  strings.TrimSpace(entry.Category),
		Embedding: cloneEmbedding(entry.Embedding),
		Metadata:  cloneMetadata(entry.Metadata),
		CreatedAt: now,
		UpdatedAt: now,
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.records[record.ID] = &record
	s.scopes[record.Scope] = append(s.scopes[record.Scope], record.ID)
	if s.maxEntries > 0 && len(s.scopes[record.Scope]) > s.maxEntries {
		oldestID := s.scopes[record.Scope][0]
		s.removeRecordLocked(oldestID)
	}

	return cloneRecord(record), nil
}

// Search finds semantic memory matches within one scope.
func (s *Store) Search(ctx context.Context, query ai.LLMMemoryQuery) ([]ai.LLMMemoryMatch, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("llmmemory search: %w", err)
	}
	if err := query.Validate(); err != nil {
		return nil, fmt.Errorf("llmmemory search: %w", err)
	}

	effective := resolveQueryDefaults(query)

	s.mu.RLock()
	ids := append([]string(nil), s.scopes[effective.Scope]...)
	matches := make([]ai.LLMMemoryMatch, 0, len(ids))
	for _, id := range ids {
		record := s.records[id]
		if record == nil {
			continue
		}
		if len(record.Embedding) != len(effective.Embedding) {
			continue
		}
		similarity := dotProduct(effective.Embedding, record.Embedding)
		if similarity < effective.MinSimilarity {
			continue
		}
		matches = append(matches, ai.LLMMemoryMatch{
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

// Update replaces the content and embedding of one stored memory record.
func (s *Store) Update(ctx context.Context, id string, content string, embedding []float32) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("llmmemory update: %w", err)
	}
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("llmmemory update: missing id")
	}
	if strings.TrimSpace(content) == "" {
		return fmt.Errorf("llmmemory update: missing content")
	}
	if err := validateMemoryEmbedding(embedding); err != nil {
		return fmt.Errorf("llmmemory update embedding: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	record, exists := s.records[strings.TrimSpace(id)]
	if !exists {
		return fmt.Errorf("llmmemory update: record %s not found", strings.TrimSpace(id))
	}

	record.Content = strings.TrimSpace(content)
	record.Embedding = cloneEmbedding(embedding)
	record.UpdatedAt = s.now()

	return nil
}

// Delete removes one stored memory record by ID.
func (s *Store) Delete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("llmmemory delete: %w", err)
	}
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("llmmemory delete: missing id")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.records[strings.TrimSpace(id)]; !exists {
		return fmt.Errorf("llmmemory delete: record %s not found", strings.TrimSpace(id))
	}
	s.removeRecordLocked(strings.TrimSpace(id))

	return nil
}

// ListByScope returns stored memory records for one scope ordered by creation
// time descending.
func (s *Store) ListByScope(ctx context.Context, scope ai.LLMMemoryScope, limit int) ([]ai.LLMMemoryRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("llmmemory list by scope: %w", err)
	}
	if err := scope.Validate(); err != nil {
		return nil, fmt.Errorf("llmmemory list by scope: %w", err)
	}
	if limit < 0 {
		return nil, fmt.Errorf("llmmemory list by scope: limit must be >= 0")
	}

	s.mu.RLock()
	ids := append([]string(nil), s.scopes[scope]...)
	records := make([]ai.LLMMemoryRecord, 0, len(ids))
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

func (s *Store) recordCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return len(s.records)
}

func (s *Store) scopeCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return len(s.scopes)
}

func (s *Store) now() time.Time {
	return s.clock().UTC()
}

func resolveQueryDefaults(query ai.LLMMemoryQuery) ai.LLMMemoryQuery {
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

func cloneRecord(record ai.LLMMemoryRecord) ai.LLMMemoryRecord {
	record.Embedding = cloneEmbedding(record.Embedding)
	record.Metadata = cloneMetadata(record.Metadata)
	return record
}

func cloneEmbedding(embedding []float32) []float32 {
	if len(embedding) == 0 {
		return nil
	}

	return append([]float32(nil), embedding...)
}

func cloneMetadata(metadata map[string]string) map[string]string {
	if len(metadata) == 0 {
		return nil
	}

	cloned := make(map[string]string, len(metadata))
	for key, value := range metadata {
		cloned[key] = value
	}

	return cloned
}

func validateMemoryEmbedding(embedding []float32) error {
	if err := (ai.LLMMemoryEntry{
		Scope: ai.LLMMemoryScope{
			Platform:       "validation",
			ConversationID: "validation",
		},
		Content:   "validation",
		Category:  "validation",
		Embedding: embedding,
	}).Validate(); err != nil {
		return fmt.Errorf("validate memory embedding: %w", err)
	}

	return nil
}

var _ ai.LLMMemoryService = (*Store)(nil)
