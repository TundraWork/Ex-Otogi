package naturalmemory

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	"ex-otogi/pkg/otogi/ai"
)

func TestConsolidateScopePrunesDecayedMemories(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 16, 12, 0, 0, 0, time.UTC)
	scope := ai.LLMMemoryScope{Platform: "telegram", ConversationID: "chat-1"}
	store := newMemoryServiceStub([]ai.LLMMemoryRecord{
		newScopedRecord(scope, "keep", 5, now.Add(-1*time.Hour), []float32{1, 0}),
		newScopedRecord(scope, "prune", 3, now.Add(-8*time.Hour), []float32{0, 1}),
	})
	module := New(withClock(func() time.Time { return now }), withConfig(Config{
		Enabled:                      true,
		DecayFactor:                  0.7,
		MinImportance:                3,
		MaxMemoriesPerScope:          10,
		DuplicateSimilarityThreshold: 0.85,
		ExtractionTimeout:            time.Second,
		ExtractionMaxInputRunes:      1000,
		ConsolidationInterval:        time.Hour,
		ConsolidationTimeout:         time.Second,
		ContextWindowSize:            5,
	}))
	module.llmMemory = store

	if err := module.consolidateScope(context.Background(), scope); err != nil {
		t.Fatalf("consolidateScope failed: %v", err)
	}

	records, err := store.ListByScope(context.Background(), scope, 0)
	if err != nil {
		t.Fatalf("ListByScope failed: %v", err)
	}
	if len(records) != 1 || records[0].ID != "keep" {
		t.Fatalf("records = %+v, want only keep", records)
	}
}

func TestConsolidateScopeMergesNearDuplicates(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 16, 12, 0, 0, 0, time.UTC)
	scope := ai.LLMMemoryScope{Platform: "telegram", ConversationID: "chat-1"}
	store := newMemoryServiceStub([]ai.LLMMemoryRecord{
		newScopedRecord(scope, "high", 8, now.Add(-1*time.Hour), []float32{1, 0}),
		newScopedRecord(scope, "low", 4, now.Add(-30*time.Minute), []float32{1, 0}),
	})
	module := New(withClock(func() time.Time { return now }), withConfig(Config{
		Enabled:                      true,
		DecayFactor:                  0.99,
		MinImportance:                1,
		MaxMemoriesPerScope:          10,
		DuplicateSimilarityThreshold: 0.85,
		ExtractionTimeout:            time.Second,
		ExtractionMaxInputRunes:      1000,
		ConsolidationInterval:        time.Hour,
		ConsolidationTimeout:         time.Second,
		ContextWindowSize:            5,
	}))
	module.llmMemory = store

	if err := module.consolidateScope(context.Background(), scope); err != nil {
		t.Fatalf("consolidateScope failed: %v", err)
	}

	records, err := store.ListByScope(context.Background(), scope, 0)
	if err != nil {
		t.Fatalf("ListByScope failed: %v", err)
	}
	if len(records) != 1 || records[0].ID != "high" {
		t.Fatalf("records = %+v, want only high", records)
	}
}

func TestConsolidateScopeCapsLowestScores(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 16, 12, 0, 0, 0, time.UTC)
	scope := ai.LLMMemoryScope{Platform: "telegram", ConversationID: "chat-1"}
	store := newMemoryServiceStub([]ai.LLMMemoryRecord{
		newScopedRecord(scope, "a", 8, now.Add(-1*time.Hour), []float32{1, 0}),
		newScopedRecord(scope, "b", 7, now.Add(-2*time.Hour), []float32{0, 1}),
		newScopedRecord(scope, "c", 3, now.Add(-3*time.Hour), []float32{0, 0.5}),
	})
	module := New(withClock(func() time.Time { return now }), withConfig(Config{
		Enabled:                      true,
		DecayFactor:                  1,
		MinImportance:                1,
		MaxMemoriesPerScope:          2,
		DuplicateSimilarityThreshold: 0.85,
		ExtractionTimeout:            time.Second,
		ExtractionMaxInputRunes:      1000,
		ConsolidationInterval:        time.Hour,
		ConsolidationTimeout:         time.Second,
		ContextWindowSize:            5,
	}))
	module.llmMemory = store

	if err := module.consolidateScope(context.Background(), scope); err != nil {
		t.Fatalf("consolidateScope failed: %v", err)
	}

	records, err := store.ListByScope(context.Background(), scope, 0)
	if err != nil {
		t.Fatalf("ListByScope failed: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("record count = %d, want 2", len(records))
	}

	ids := []string{records[0].ID, records[1].ID}
	sort.Strings(ids)
	if ids[0] != "a" || ids[1] != "b" {
		t.Fatalf("ids = %v, want [a b]", ids)
	}
}

type memoryServiceStub struct {
	records map[string]ai.LLMMemoryRecord
}

func newMemoryServiceStub(records []ai.LLMMemoryRecord) *memoryServiceStub {
	store := &memoryServiceStub{records: make(map[string]ai.LLMMemoryRecord, len(records))}
	for _, record := range records {
		store.records[record.ID] = record
	}

	return store
}

func (s *memoryServiceStub) Store(context.Context, ai.LLMMemoryEntry) (ai.LLMMemoryRecord, error) {
	return ai.LLMMemoryRecord{}, fmt.Errorf("not implemented")
}

func (s *memoryServiceStub) Search(context.Context, ai.LLMMemoryQuery) ([]ai.LLMMemoryMatch, error) {
	return nil, fmt.Errorf("not implemented")
}

func (s *memoryServiceStub) Update(context.Context, ai.LLMMemoryUpdate) (ai.LLMMemoryRecord, error) {
	return ai.LLMMemoryRecord{}, fmt.Errorf("not implemented")
}

func (s *memoryServiceStub) Delete(_ context.Context, id string) error {
	delete(s.records, id)
	return nil
}

func (s *memoryServiceStub) ListByScope(
	_ context.Context,
	scope ai.LLMMemoryScope,
	limit int,
) ([]ai.LLMMemoryRecord, error) {
	records := make([]ai.LLMMemoryRecord, 0, len(s.records))
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

func newScopedRecord(scope ai.LLMMemoryScope, id string, importance int, lastAccessed time.Time, embedding []float32) ai.LLMMemoryRecord {
	return ai.LLMMemoryRecord{
		ID:        id,
		Scope:     scope,
		Content:   id,
		Category:  "knowledge",
		Embedding: embedding,
		Metadata: map[string]string{
			"importance":    fmt.Sprintf("%d", importance),
			"last_accessed": lastAccessed.Format(time.RFC3339),
		},
		CreatedAt: lastAccessed,
		UpdatedAt: lastAccessed,
	}
}
