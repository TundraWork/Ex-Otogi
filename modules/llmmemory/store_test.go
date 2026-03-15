package llmmemory

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ex-otogi/pkg/otogi/ai"
)

func TestStoreStoreAndListByScope(t *testing.T) {
	t.Parallel()

	store := newStore(10, fixedClock(time.Unix(100, 0).UTC()), sequenceIDs("mem-1"))
	record, err := store.Store(context.Background(), ai.LLMMemoryEntry{
		Scope:     testMemoryScope("chat-1"),
		Content:   "User likes tea",
		Category:  "preference",
		Embedding: []float32{1, 0},
		Metadata:  map[string]string{"source": "user"},
	})
	if err != nil {
		t.Fatalf("Store failed: %v", err)
	}

	records, err := store.ListByScope(context.Background(), testMemoryScope("chat-1"), 10)
	if err != nil {
		t.Fatalf("ListByScope failed: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("records len = %d, want 1", len(records))
	}
	if records[0].ID != record.ID {
		t.Fatalf("record id = %q, want %q", records[0].ID, record.ID)
	}
	if records[0].Metadata["source"] != "user" {
		t.Fatalf("metadata[source] = %q, want user", records[0].Metadata["source"])
	}
}

func TestStoreSearchSimilarityScoring(t *testing.T) {
	t.Parallel()

	store := newStore(10, fixedClock(time.Unix(100, 0).UTC()), sequenceIDs("mem-1", "mem-2", "mem-3"))
	entries := []ai.LLMMemoryEntry{
		{
			Scope:     testMemoryScope("chat-1"),
			Content:   "Alice likes tea",
			Category:  "preference",
			Embedding: []float32{1, 0, 0},
		},
		{
			Scope:     testMemoryScope("chat-1"),
			Content:   "Alice likes green tea",
			Category:  "preference",
			Embedding: []float32{0.8, 0.6, 0},
		},
		{
			Scope:     testMemoryScope("chat-1"),
			Content:   "Alice likes coffee",
			Category:  "preference",
			Embedding: []float32{0, 1, 0},
		},
	}
	for _, entry := range entries {
		if _, err := store.Store(context.Background(), entry); err != nil {
			t.Fatalf("Store failed: %v", err)
		}
	}

	matches, err := store.Search(context.Background(), ai.LLMMemoryQuery{
		Scope:         testMemoryScope("chat-1"),
		Embedding:     []float32{1, 0, 0},
		Limit:         3,
		MinSimilarity: 0.1,
	})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(matches) != 2 {
		t.Fatalf("matches len = %d, want 2", len(matches))
	}
	if matches[0].Record.Content != "Alice likes tea" {
		t.Fatalf("matches[0] = %q, want Alice likes tea", matches[0].Record.Content)
	}
	if matches[1].Record.Content != "Alice likes green tea" {
		t.Fatalf("matches[1] = %q, want Alice likes green tea", matches[1].Record.Content)
	}
	if matches[0].Similarity <= matches[1].Similarity {
		t.Fatalf("similarities = %f, %f, want descending order", matches[0].Similarity, matches[1].Similarity)
	}
}

func TestStoreSearchMinSimilarityFilteringAndLimit(t *testing.T) {
	t.Parallel()

	store := newStore(10, fixedClock(time.Unix(100, 0).UTC()), sequenceIDs("mem-1", "mem-2", "mem-3"))
	for _, embedding := range [][]float32{
		{1, 0},
		{0.8, 0.6},
		{0.6, 0.8},
	} {
		if _, err := store.Store(context.Background(), ai.LLMMemoryEntry{
			Scope:     testMemoryScope("chat-1"),
			Content:   fmt.Sprintf("memory-%v", embedding),
			Category:  "knowledge",
			Embedding: embedding,
		}); err != nil {
			t.Fatalf("Store failed: %v", err)
		}
	}

	filtered, err := store.Search(context.Background(), ai.LLMMemoryQuery{
		Scope:         testMemoryScope("chat-1"),
		Embedding:     []float32{1, 0},
		Limit:         10,
		MinSimilarity: 0.75,
	})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(filtered) != 2 {
		t.Fatalf("filtered len = %d, want 2", len(filtered))
	}

	limited, err := store.Search(context.Background(), ai.LLMMemoryQuery{
		Scope:         testMemoryScope("chat-1"),
		Embedding:     []float32{1, 0},
		Limit:         1,
		MinSimilarity: 0.1,
	})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(limited) != 1 {
		t.Fatalf("limited len = %d, want 1", len(limited))
	}
}

func TestStoreUpdateAndDelete(t *testing.T) {
	t.Parallel()

	store := newStore(10, sequenceClock([]time.Time{
		time.Unix(100, 0).UTC(),
		time.Unix(200, 0).UTC(),
	}), sequenceIDs("mem-1"))
	record, err := store.Store(context.Background(), ai.LLMMemoryEntry{
		Scope:     testMemoryScope("chat-1"),
		Content:   "Old memory",
		Category:  "knowledge",
		Embedding: []float32{1, 0},
	})
	if err != nil {
		t.Fatalf("Store failed: %v", err)
	}

	if err := store.Update(context.Background(), record.ID, "New memory", []float32{0, 1}); err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	records, err := store.ListByScope(context.Background(), testMemoryScope("chat-1"), 10)
	if err != nil {
		t.Fatalf("ListByScope failed: %v", err)
	}
	if records[0].Content != "New memory" {
		t.Fatalf("content = %q, want New memory", records[0].Content)
	}
	if !records[0].UpdatedAt.After(records[0].CreatedAt) {
		t.Fatalf("updated_at = %s, want after created_at %s", records[0].UpdatedAt, records[0].CreatedAt)
	}

	if err := store.Delete(context.Background(), record.ID); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	records, err = store.ListByScope(context.Background(), testMemoryScope("chat-1"), 10)
	if err != nil {
		t.Fatalf("ListByScope failed: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("records len = %d, want 0", len(records))
	}
}

func TestStoreListByScopeReturnsNewestFirst(t *testing.T) {
	t.Parallel()

	store := newStore(10, sequenceClock([]time.Time{
		time.Unix(100, 0).UTC(),
		time.Unix(200, 0).UTC(),
		time.Unix(300, 0).UTC(),
	}), sequenceIDs("mem-1", "mem-2", "mem-3"))
	for _, content := range []string{"first", "second", "third"} {
		if _, err := store.Store(context.Background(), ai.LLMMemoryEntry{
			Scope:     testMemoryScope("chat-1"),
			Content:   content,
			Category:  "knowledge",
			Embedding: []float32{1, 0},
		}); err != nil {
			t.Fatalf("Store failed: %v", err)
		}
	}

	records, err := store.ListByScope(context.Background(), testMemoryScope("chat-1"), 10)
	if err != nil {
		t.Fatalf("ListByScope failed: %v", err)
	}
	if len(records) != 3 {
		t.Fatalf("records len = %d, want 3", len(records))
	}
	wantOrder := []string{"third", "second", "first"}
	for index, want := range wantOrder {
		if records[index].Content != want {
			t.Fatalf("records[%d] = %q, want %q", index, records[index].Content, want)
		}
	}
}

func TestStoreConcurrentAccess(t *testing.T) {
	t.Parallel()

	var counter atomic.Int64
	store := newStore(1000, time.Now, func() string {
		return fmt.Sprintf("mem-%d", counter.Add(1))
	})

	var group sync.WaitGroup
	for worker := 0; worker < 10; worker++ {
		group.Add(1)
		go func(worker int) {
			defer group.Done()
			for index := 0; index < 20; index++ {
				if _, err := store.Store(context.Background(), ai.LLMMemoryEntry{
					Scope:     testMemoryScope("chat-1"),
					Content:   fmt.Sprintf("worker-%d-%d", worker, index),
					Category:  "knowledge",
					Embedding: []float32{1, 0},
				}); err != nil {
					t.Errorf("Store failed: %v", err)
					return
				}
				if _, err := store.Search(context.Background(), ai.LLMMemoryQuery{
					Scope:         testMemoryScope("chat-1"),
					Embedding:     []float32{1, 0},
					Limit:         5,
					MinSimilarity: 0.1,
				}); err != nil {
					t.Errorf("Search failed: %v", err)
					return
				}
			}
		}(worker)
	}
	group.Wait()

	records, err := store.ListByScope(context.Background(), testMemoryScope("chat-1"), 0)
	if err != nil {
		t.Fatalf("ListByScope failed: %v", err)
	}
	if len(records) == 0 {
		t.Fatal("expected stored records after concurrent operations")
	}
}

func TestStorePerScopeCapacityEviction(t *testing.T) {
	t.Parallel()

	store := newStore(2, sequenceClock([]time.Time{
		time.Unix(100, 0).UTC(),
		time.Unix(200, 0).UTC(),
		time.Unix(300, 0).UTC(),
		time.Unix(400, 0).UTC(),
	}), sequenceIDs("mem-1", "mem-2", "mem-3", "mem-4"))

	for _, content := range []string{"one", "two", "three"} {
		if _, err := store.Store(context.Background(), ai.LLMMemoryEntry{
			Scope:     testMemoryScope("chat-1"),
			Content:   content,
			Category:  "knowledge",
			Embedding: []float32{1, 0},
		}); err != nil {
			t.Fatalf("Store failed: %v", err)
		}
	}
	if _, err := store.Store(context.Background(), ai.LLMMemoryEntry{
		Scope:     testMemoryScope("chat-2"),
		Content:   "other-scope",
		Category:  "knowledge",
		Embedding: []float32{1, 0},
	}); err != nil {
		t.Fatalf("Store failed: %v", err)
	}

	records, err := store.ListByScope(context.Background(), testMemoryScope("chat-1"), 10)
	if err != nil {
		t.Fatalf("ListByScope failed: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("records len = %d, want 2", len(records))
	}
	if records[0].Content != "three" || records[1].Content != "two" {
		t.Fatalf("records = %+v, want newest two records", records)
	}

	otherScopeRecords, err := store.ListByScope(context.Background(), testMemoryScope("chat-2"), 10)
	if err != nil {
		t.Fatalf("ListByScope failed: %v", err)
	}
	if len(otherScopeRecords) != 1 {
		t.Fatalf("other scope len = %d, want 1", len(otherScopeRecords))
	}
}

func testMemoryScope(conversationID string) ai.LLMMemoryScope {
	return ai.LLMMemoryScope{
		TenantID:       "tenant-1",
		Platform:       "telegram",
		ConversationID: conversationID,
	}
}

func fixedClock(now time.Time) func() time.Time {
	return func() time.Time { return now }
}

func sequenceClock(times []time.Time) func() time.Time {
	index := 0
	var mu sync.Mutex
	return func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		if len(times) == 0 {
			return time.Unix(0, 0).UTC()
		}
		if index >= len(times) {
			return times[len(times)-1]
		}
		value := times[index]
		index++
		return value
	}
}

func sequenceIDs(ids ...string) func() string {
	index := 0
	var mu sync.Mutex
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		if len(ids) == 0 {
			return "mem-default"
		}
		if index >= len(ids) {
			return ids[len(ids)-1]
		}
		value := ids[index]
		index++
		return value
	}
}
