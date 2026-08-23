package memory

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	"ex-otogi/pkg/otogi/ai"
)

func validConsolidationConfig() Config {
	return Config{
		Enabled:                  true,
		ExtractionTimeout:        time.Second,
		ExtractionMaxInputRunes:  4000,
		ConsolidationInterval:    time.Hour,
		BufferQuietPeriod:        2 * time.Minute,
		BufferMaxRunes:           3000,
		BufferMaxArticles:        30,
		BufferMaxAge:             10 * time.Minute,
		BufferCheckInterval:      15 * time.Second,
		RetrievalSearchLimit:     20,
		RetrievalPlanningEnabled: true,
		RetrievalPlanningTimeout: 10 * time.Second,
	}
}

func TestConsolidateScopePreservesDurableMemories(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 16, 12, 0, 0, 0, time.UTC)
	scope := ai.SemanticScope{Platform: "telegram", ConversationID: "chat-1"}
	store := newMemoryServiceStub([]ai.SemanticRecord{
		newScopedRecord(scope, "keep", 5, now.Add(-1*time.Hour), []float32{1, 0}),
		newScopedRecord(scope, "prune", 3, now.Add(-8*time.Hour), []float32{0, 1}),
	})
	cfg := validConsolidationConfig()
	module := New(withClock(func() time.Time { return now }), withConfig(cfg))
	module.semanticStore = store
	module.windowManager = newWindowManager(cfg, func() time.Time { return now })

	if err := module.consolidateScope(context.Background(), scope); err != nil {
		t.Fatalf("consolidateScope failed: %v", err)
	}

	records, err := store.ListByScope(context.Background(), scope, 0)
	if err != nil {
		t.Fatalf("ListByScope failed: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("records = %+v, want both durable memories", records)
	}
}

func TestConsolidateScopeDoesNotEnforceImplicitCap(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 16, 12, 0, 0, 0, time.UTC)
	scope := ai.SemanticScope{Platform: "telegram", ConversationID: "chat-1"}
	store := newMemoryServiceStub([]ai.SemanticRecord{
		newScopedRecord(scope, "a", 8, now.Add(-1*time.Hour), []float32{1, 0}),
		newScopedRecord(scope, "b", 7, now.Add(-2*time.Hour), []float32{0, 1}),
		newScopedRecord(scope, "c", 3, now.Add(-3*time.Hour), []float32{0, 0.5}),
	})
	cfg := validConsolidationConfig()
	module := New(withClock(func() time.Time { return now }), withConfig(cfg))
	module.semanticStore = store
	module.windowManager = newWindowManager(cfg, func() time.Time { return now })

	if err := module.consolidateScope(context.Background(), scope); err != nil {
		t.Fatalf("consolidateScope failed: %v", err)
	}

	records, err := store.ListByScope(context.Background(), scope, 0)
	if err != nil {
		t.Fatalf("ListByScope failed: %v", err)
	}
	if len(records) != 3 {
		t.Fatalf("record count = %d, want 3", len(records))
	}

	ids := []string{records[0].ID, records[1].ID, records[2].ID}
	sort.Strings(ids)
	if ids[0] != "a" || ids[1] != "b" || ids[2] != "c" {
		t.Fatalf("ids = %v, want [a b c]", ids)
	}
}

func TestConsolidateScopeDeletesExpiredMemories(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 16, 12, 0, 0, 0, time.UTC)
	past := now.Add(-1 * time.Hour)
	future := now.Add(24 * time.Hour)
	scope := ai.SemanticScope{Platform: "telegram", ConversationID: "chat-1"}

	expiredRecord := newScopedRecord(scope, "expired", 8, now.Add(-1*time.Hour), []float32{1, 0})
	expiredRecord.Profile.ValidUntil = &past

	validRecord := newScopedRecord(scope, "valid", 8, now.Add(-1*time.Hour), []float32{0, 1})
	validRecord.Profile.ValidUntil = &future

	noExpiryRecord := newScopedRecord(scope, "no-expiry", 8, now.Add(-1*time.Hour), []float32{0.5, 0.5})

	store := newMemoryServiceStub([]ai.SemanticRecord{expiredRecord, validRecord, noExpiryRecord})
	cfg := validConsolidationConfig()
	module := New(withClock(func() time.Time { return now }), withConfig(cfg))
	module.semanticStore = store
	module.windowManager = newWindowManager(cfg, func() time.Time { return now })

	if err := module.consolidateScope(context.Background(), scope); err != nil {
		t.Fatalf("consolidateScope failed: %v", err)
	}

	records, err := store.ListByScope(context.Background(), scope, 0)
	if err != nil {
		t.Fatalf("ListByScope failed: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("record count = %d, want 2 (expired should be deleted)", len(records))
	}
	for _, record := range records {
		if record.ID == "expired" {
			t.Fatal("expired memory should have been deleted")
		}
	}
}

type memoryServiceStub struct {
	records    map[string]ai.SemanticRecord
	nextID     int
	storeCalls []ai.SemanticEntry
}

func newMemoryServiceStub(records []ai.SemanticRecord) *memoryServiceStub {
	store := &memoryServiceStub{records: make(map[string]ai.SemanticRecord, len(records))}
	for _, record := range records {
		store.records[record.ID] = record
	}

	return store
}

func (s *memoryServiceStub) Store(_ context.Context, entry ai.SemanticEntry) (ai.SemanticRecord, error) {
	s.storeCalls = append(s.storeCalls, entry)
	s.nextID++
	id := fmt.Sprintf("gen-%d", s.nextID)
	record := ai.SemanticRecord{
		ID:        id,
		Scope:     entry.Scope,
		Content:   entry.Content,
		Category:  entry.Category,
		Embedding: append([]float32(nil), entry.Embedding...),
		Profile:   entry.Profile,
		Keywords:  entry.Keywords,
		Tags:      entry.Tags,
		Links:     entry.Links,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	s.records[id] = record

	return record, nil
}

func (s *memoryServiceStub) Search(context.Context, ai.SemanticQuery) ([]ai.SemanticMatch, error) {
	return nil, fmt.Errorf("not implemented")
}

func (s *memoryServiceStub) Update(_ context.Context, update ai.SemanticUpdate) (ai.SemanticRecord, error) {
	record, exists := s.records[update.ID]
	if !exists {
		return ai.SemanticRecord{}, fmt.Errorf("record %s not found", update.ID)
	}
	record.Content = update.Content
	record.Category = update.Category
	record.Embedding = append([]float32(nil), update.Embedding...)
	record.Profile = update.Profile
	record.Keywords = update.Keywords
	record.Tags = update.Tags
	record.Links = update.Links
	s.records[update.ID] = record

	return record, nil
}

func (s *memoryServiceStub) Delete(_ context.Context, id string) error {
	delete(s.records, id)
	return nil
}

func (s *memoryServiceStub) ListByScope(
	_ context.Context,
	scope ai.SemanticScope,
	limit int,
) ([]ai.SemanticRecord, error) {
	records := make([]ai.SemanticRecord, 0, len(s.records))
	for _, record := range s.records {
		if record.Scope != scope {
			continue
		}
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].ID < records[j].ID
	})
	if limit > 0 && len(records) > limit {
		records = records[:limit]
	}

	return records, nil
}

func newScopedRecord(scope ai.SemanticScope, id string, importance int, lastAccessed time.Time, embedding []float32) ai.SemanticRecord {
	return ai.SemanticRecord{
		ID:        id,
		Scope:     scope,
		Content:   id,
		Category:  "knowledge",
		Embedding: embedding,
		Profile: ai.SemanticProfile{
			Importance: importance,
		},
		CreatedAt: lastAccessed,
		UpdatedAt: lastAccessed,
	}
}
