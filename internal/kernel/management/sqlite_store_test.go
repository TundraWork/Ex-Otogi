package management

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	panel "ex-otogi/pkg/otogi/management"
)

func newTestSQLiteStore(t *testing.T) *SQLiteStore {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "management.db")
	store, err := NewSQLiteStore(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestSQLiteRecordEventAssignsMonotonicIDs(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)

	const workers = 24
	var (
		wg      sync.WaitGroup
		results = make(chan int64, workers)
	)
	for i := range workers {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()

			recorded, err := store.RecordEvent(context.Background(), panel.Event{
				Category: panel.EventCategoryRuntime,
				Kind:     fmt.Sprintf("runtime.test.%d", index),
				Level:    panel.EventLevelInfo,
			})
			if err != nil {
				t.Errorf("RecordEvent(%d) failed: %v", index, err)
				return
			}
			results <- recorded.ID
		}(i)
	}

	wg.Wait()
	close(results)

	got := make([]int64, 0, workers)
	for id := range results {
		got = append(got, id)
	}
	slices.Sort(got)

	if len(got) != workers {
		t.Fatalf("recorded %d IDs, want %d", len(got), workers)
	}
	for index, id := range got {
		want := int64(index + 1)
		if id != want {
			t.Fatalf("sorted ID[%d] = %d, want %d", index, id, want)
		}
	}
}

func TestSQLiteListEventsOrderedAndFiltered(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	ctx := context.Background()

	recorded := make([]panel.Event, 0, 4)
	for _, event := range []panel.Event{
		{Category: panel.EventCategoryRuntime, Kind: "runtime.a", Level: panel.EventLevelInfo, Module: "kernel"},
		{Category: panel.EventCategoryLLM, Kind: "llm.a", Level: panel.EventLevelDebug, Module: "llmchat", TraceID: "trace-1"},
		{Category: panel.EventCategoryLLM, Kind: "llm.b", Level: panel.EventLevelError, Module: "llmchat", TraceID: "trace-2"},
		{Category: panel.EventCategoryMemory, Kind: "memory.a", Level: panel.EventLevelInfo, Module: "naturalmemory"},
	} {
		item, err := store.RecordEvent(ctx, event)
		if err != nil {
			t.Fatalf("RecordEvent(%s) failed: %v", event.Kind, err)
		}
		recorded = append(recorded, item)
	}

	page, err := store.ListEvents(ctx, panel.EventQuery{
		AfterID:  recorded[0].ID,
		Limit:    1,
		Category: panel.EventCategoryLLM,
		Module:   "llmchat",
	})
	if err != nil {
		t.Fatalf("ListEvents failed: %v", err)
	}

	if page.WindowStartID != recorded[0].ID || page.WindowEndID != recorded[3].ID {
		t.Fatalf("window = [%d,%d], want [%d,%d]", page.WindowStartID, page.WindowEndID, recorded[0].ID, recorded[3].ID)
	}
	if !page.HasMore {
		t.Fatal("HasMore = false, want true")
	}
	if len(page.Items) != 1 {
		t.Fatalf("len(Items) = %d, want 1", len(page.Items))
	}
	if page.Items[0].ID != recorded[1].ID {
		t.Fatalf("ordered ID[0] = %d, want %d", page.Items[0].ID, recorded[1].ID)
	}
}

func TestSQLiteRecordArtifactLinksArtifactIDBackToEvent(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	ctx := context.Background()

	recordedEvent, err := store.RecordEvent(ctx, panel.Event{
		Category: panel.EventCategoryBusinessEvent,
		Kind:     "platform.event.received",
		Level:    panel.EventLevelDebug,
	})
	if err != nil {
		t.Fatalf("RecordEvent failed: %v", err)
	}

	if _, err := store.RecordArtifact(ctx, panel.Artifact{
		ID:      "art-linked",
		EventID: recordedEvent.ID,
		Kind:    panel.ArtifactKindStructuredDebug,
		Content: "{\"kind\":\"article.created\"}",
	}); err != nil {
		t.Fatalf("RecordArtifact failed: %v", err)
	}

	reloadedEvent, err := store.GetEvent(ctx, recordedEvent.ID)
	if err != nil {
		t.Fatalf("GetEvent failed: %v", err)
	}
	if len(reloadedEvent.ArtifactIDs) != 1 {
		t.Fatalf("len(ArtifactIDs) = %d, want 1", len(reloadedEvent.ArtifactIDs))
	}
	if reloadedEvent.ArtifactIDs[0] != "art-linked" {
		t.Fatalf("ArtifactIDs[0] = %q, want art-linked", reloadedEvent.ArtifactIDs[0])
	}
}

func TestSQLiteArtifactLookup(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	ctx := context.Background()

	event, err := store.RecordEvent(ctx, panel.Event{
		Category: panel.EventCategoryLLM,
		Kind:     "llm.one",
		Level:    panel.EventLevelInfo,
	})
	if err != nil {
		t.Fatalf("RecordEvent failed: %v", err)
	}
	if _, err := store.RecordArtifact(ctx, panel.Artifact{
		ID:      "art-x",
		EventID: event.ID,
		Kind:    panel.ArtifactKindPromptFull,
		Content: "abc",
	}); err != nil {
		t.Fatalf("RecordArtifact failed: %v", err)
	}

	artifact, err := store.GetArtifact(ctx, "art-x")
	if err != nil {
		t.Fatalf("GetArtifact failed: %v", err)
	}
	if artifact.Content != "abc" {
		t.Fatalf("artifact.Content = %q, want %q", artifact.Content, "abc")
	}

	if _, err := store.GetArtifact(ctx, "nonexistent"); !errors.Is(err, panel.ErrArtifactNotFound) {
		t.Fatalf("GetArtifact(nonexistent) error = %v, want ErrArtifactNotFound", err)
	}
}

func TestSQLiteDataPersistsAcrossReopen(t *testing.T) {
	t.Parallel()

	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "management.db")

	store, err := NewSQLiteStore(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore (first): %v", err)
	}
	ctx := context.Background()

	recorded, err := store.RecordEvent(ctx, panel.Event{
		Category: panel.EventCategoryRuntime,
		Kind:     "runtime.persist_test",
		Level:    panel.EventLevelInfo,
	})
	if err != nil {
		t.Fatalf("RecordEvent failed: %v", err)
	}

	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	store2, err := NewSQLiteStore(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore (reopen): %v", err)
	}
	defer store2.Close()

	reloaded, err := store2.GetEvent(ctx, recorded.ID)
	if err != nil {
		t.Fatalf("GetEvent after reopen: %v", err)
	}
	if reloaded.ID != recorded.ID {
		t.Fatalf("reloaded ID = %d, want %d", reloaded.ID, recorded.ID)
	}
	if reloaded.Kind != "runtime.persist_test" {
		t.Fatalf("reloaded Kind = %q, want %q", reloaded.Kind, "runtime.persist_test")
	}
}
