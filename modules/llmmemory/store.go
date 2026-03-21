package llmmemory

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"ex-otogi/pkg/otogi/ai"

	"github.com/google/uuid"
)

const (
	defaultSearchLimit       = 5
	defaultMinSimilarity     = 0.3
	defaultProfileImportance = 5
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
		Profile:   normalizeProfile(entry.Profile, entry.Metadata, now),
		Metadata:  cloneMetadata(entry.Metadata),
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

	now := s.now()

	s.mu.RLock()
	ids := append([]string(nil), s.scopes[effective.Scope]...)
	matches := make([]ai.LLMMemoryMatch, 0, len(ids))
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

// Update replaces the mutable fields of one stored memory record.
func (s *Store) Update(ctx context.Context, update ai.LLMMemoryUpdate) (ai.LLMMemoryRecord, error) {
	if err := ctx.Err(); err != nil {
		return ai.LLMMemoryRecord{}, fmt.Errorf("llmmemory update: %w", err)
	}
	if err := update.Validate(); err != nil {
		return ai.LLMMemoryRecord{}, fmt.Errorf("llmmemory update: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	record, exists := s.records[strings.TrimSpace(update.ID)]
	if !exists {
		return ai.LLMMemoryRecord{}, fmt.Errorf(
			"llmmemory update: record %s not found",
			strings.TrimSpace(update.ID),
		)
	}

	record.Content = strings.TrimSpace(update.Content)
	record.Category = strings.TrimSpace(update.Category)
	record.Embedding = cloneEmbedding(update.Embedding)
	record.Profile = normalizeProfile(update.Profile, update.Metadata, s.now())
	record.Metadata = cloneMetadata(update.Metadata)
	record.Keywords = cloneStrings(update.Keywords)
	record.Tags = cloneStrings(update.Tags)
	record.Links = cloneLinks(update.Links)
	record.UpdatedAt = s.now()

	return cloneRecord(*record), nil
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
	record.Profile = cloneProfile(record.Profile)
	record.Metadata = cloneMetadata(record.Metadata)
	record.Keywords = cloneStrings(record.Keywords)
	record.Tags = cloneStrings(record.Tags)
	record.Links = cloneLinks(record.Links)
	return record
}

func cloneProfile(profile ai.LLMMemoryProfile) ai.LLMMemoryProfile {
	profile.LastAccessedAt = profile.LastAccessedAt.UTC()
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

func cloneActorRef(actor *ai.LLMMemoryActorRef) *ai.LLMMemoryActorRef {
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

func cloneStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}

	return append([]string(nil), values...)
}

func cloneLinks(links []ai.LLMMemoryLink) []ai.LLMMemoryLink {
	if len(links) == 0 {
		return nil
	}

	return append([]ai.LLMMemoryLink(nil), links...)
}

func normalizeProfile(
	profile ai.LLMMemoryProfile,
	metadata map[string]string,
	fallbackAccess time.Time,
) ai.LLMMemoryProfile {
	normalized := cloneProfile(profile)
	if normalized.Kind == "" {
		normalized.Kind = ai.LLMMemoryKindUnit
	}
	if normalized.Importance == 0 {
		normalized.Importance = parseMetadataInt(metadata, ai.LLMMemoryMetadataImportance, defaultProfileImportance)
	}
	if normalized.LastAccessedAt.IsZero() {
		normalized.LastAccessedAt = parseMetadataTime(metadata, ai.LLMMemoryMetadataLastAccessed, fallbackAccess)
	}
	normalized.LastAccessedAt = normalized.LastAccessedAt.UTC()
	if normalized.AccessCount == 0 {
		normalized.AccessCount = parseMetadataInt(metadata, ai.LLMMemoryMetadataAccessCount, 0)
	}
	if strings.TrimSpace(normalized.Source) == "" {
		normalized.Source = strings.TrimSpace(metadata[ai.LLMMemoryMetadataSource])
	}
	if strings.TrimSpace(normalized.SourceArticleID) == "" {
		normalized.SourceArticleID = strings.TrimSpace(metadata[ai.LLMMemoryMetadataSourceArticleID])
	}
	if normalized.SourceActor == nil {
		normalized.SourceActor = parseActorRef(
			metadata,
			ai.LLMMemoryMetadataSourceActorID,
			ai.LLMMemoryMetadataSourceActorName,
			ai.LLMMemoryMetadataSourceActorIsBot,
		)
	}
	if normalized.SubjectActor == nil {
		normalized.SubjectActor = parseActorRef(
			metadata,
			ai.LLMMemoryMetadataSubjectActorID,
			ai.LLMMemoryMetadataSubjectActorName,
			ai.LLMMemoryMetadataSubjectActorIsBot,
		)
	}
	if len(normalized.EvidenceRecordIDs) == 0 {
		normalized.EvidenceRecordIDs = parseMetadataCSV(metadata, ai.LLMMemoryMetadataSourceRecordIDs)
	}

	return normalized
}

func parseMetadataInt(metadata map[string]string, key string, defaultValue int) int {
	if len(metadata) == 0 {
		return defaultValue
	}

	raw := strings.TrimSpace(metadata[key])
	if raw == "" {
		return defaultValue
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return defaultValue
	}

	return value
}

func parseMetadataTime(metadata map[string]string, key string, defaultValue time.Time) time.Time {
	if len(metadata) == 0 {
		return defaultValue.UTC()
	}

	raw := strings.TrimSpace(metadata[key])
	if raw == "" {
		return defaultValue.UTC()
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return defaultValue.UTC()
	}

	return parsed.UTC()
}

func parseMetadataCSV(metadata map[string]string, key string) []string {
	if len(metadata) == 0 {
		return nil
	}

	raw := strings.TrimSpace(metadata[key])
	if raw == "" {
		return nil
	}

	parts := strings.Split(raw, ",")
	ids := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		ids = append(ids, trimmed)
	}
	if len(ids) == 0 {
		return nil
	}

	return ids
}

func parseActorRef(metadata map[string]string, idKey string, nameKey string, isBotKey string) *ai.LLMMemoryActorRef {
	if len(metadata) == 0 {
		return nil
	}

	actor := &ai.LLMMemoryActorRef{
		ID:   strings.TrimSpace(metadata[idKey]),
		Name: strings.TrimSpace(metadata[nameKey]),
	}
	if actor.ID == "" && actor.Name == "" {
		return nil
	}
	if raw := strings.TrimSpace(metadata[isBotKey]); raw != "" {
		if parsed, err := strconv.ParseBool(raw); err == nil {
			actor.IsBot = parsed
		}
	}

	return actor
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
