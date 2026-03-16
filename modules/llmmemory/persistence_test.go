package llmmemory

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ex-otogi/pkg/otogi/ai"
)

func TestStoreSaveAndLoadRoundTrip(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "llm-memory.json")
	store := newStore(10, sequenceClock([]time.Time{
		time.Unix(100, 0).UTC(),
		time.Unix(200, 0).UTC(),
	}), sequenceIDs("mem-1", "mem-2"))
	scope := testMemoryScope("chat-1")
	for _, entry := range []ai.LLMMemoryEntry{
		{
			Scope:     scope,
			Content:   "Alice likes tea",
			Category:  "preference",
			Embedding: []float32{1, 0},
			Profile: ai.LLMMemoryProfile{
				Kind:           ai.LLMMemoryKindUnit,
				Importance:     7,
				LastAccessedAt: time.Unix(150, 0).UTC(),
				AccessCount:    2,
				Source:         "user",
			},
			Metadata: map[string]string{"source": "user"},
		},
		{
			Scope:     scope,
			Content:   "Alice works on Go",
			Category:  "knowledge",
			Embedding: []float32{0.8, 0.6},
		},
	} {
		if _, err := store.Store(context.Background(), entry); err != nil {
			t.Fatalf("Store failed: %v", err)
		}
	}

	if err := store.SaveToFile(path); err != nil {
		t.Fatalf("SaveToFile failed: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected persisted file: %v", err)
	}

	loaded := newStore(10, time.Now, sequenceIDs("unused"))
	if err := loaded.LoadFromFile(path); err != nil {
		t.Fatalf("LoadFromFile failed: %v", err)
	}

	records, err := loaded.ListByScope(context.Background(), scope, 10)
	if err != nil {
		t.Fatalf("ListByScope failed: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("records len = %d, want 2", len(records))
	}
	if records[1].Metadata["source"] != "user" {
		t.Fatalf("metadata[source] = %q, want user", records[1].Metadata["source"])
	}
	if records[1].Profile.Kind != ai.LLMMemoryKindUnit {
		t.Fatalf("profile.kind = %q, want %q", records[1].Profile.Kind, ai.LLMMemoryKindUnit)
	}
	if records[1].Profile.AccessCount != 2 {
		t.Fatalf("profile.access_count = %d, want 2", records[1].Profile.AccessCount)
	}

	matches, err := loaded.Search(context.Background(), ai.LLMMemoryQuery{
		Scope:         scope,
		Embedding:     []float32{1, 0},
		Limit:         5,
		MinSimilarity: 0.1,
	})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(matches) != 2 {
		t.Fatalf("matches len = %d, want 2", len(matches))
	}
}

func TestStoreLoadFromFileMissingFile(t *testing.T) {
	t.Parallel()

	store := newStore(10, time.Now, sequenceIDs("unused"))
	err := store.LoadFromFile(filepath.Join(t.TempDir(), "missing.json"))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestStoreLoadFromFileMigratesLegacyMetadata(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "legacy-llm-memory.json")
	payload := `{
  "records": [
    {
      "id": "mem-1",
      "tenant_id": "tenant-1",
      "platform": "telegram",
      "conversation_id": "chat-1",
      "content": "Alice likes tea",
      "category": "preference",
      "embedding": [1, 0],
      "metadata": {
        "importance": "8",
        "access_count": "4",
        "last_accessed": "2026-03-16T10:00:00Z",
        "source": "natural",
        "source_actor_name": "Alice",
        "source_record_ids": "mem-a, mem-b"
      },
      "created_at": "2026-03-16T09:00:00Z",
      "updated_at": "2026-03-16T09:30:00Z"
    }
  ]
}`
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	store := newStore(10, time.Now, sequenceIDs("unused"))
	if err := store.LoadFromFile(path); err != nil {
		t.Fatalf("LoadFromFile failed: %v", err)
	}

	records, err := store.ListByScope(context.Background(), testMemoryScope("chat-1"), 10)
	if err != nil {
		t.Fatalf("ListByScope failed: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("records len = %d, want 1", len(records))
	}
	if records[0].Profile.Importance != 8 {
		t.Fatalf("profile.importance = %d, want 8", records[0].Profile.Importance)
	}
	if records[0].Profile.Kind != ai.LLMMemoryKindUnit {
		t.Fatalf("profile.kind = %q, want %q", records[0].Profile.Kind, ai.LLMMemoryKindUnit)
	}
	if records[0].Profile.SourceActor == nil || records[0].Profile.SourceActor.Name != "Alice" {
		t.Fatalf("profile.source_actor = %+v, want Alice", records[0].Profile.SourceActor)
	}
}
