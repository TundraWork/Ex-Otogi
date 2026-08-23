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

func TestSQLiteListEventsPagination(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	ctx := context.Background()

	recorded := make([]panel.Event, 0, 4)
	for _, event := range []panel.Event{
		{Category: panel.EventCategoryRuntime, Kind: "runtime.a", Level: panel.EventLevelInfo, Module: "kernel"},
		{Category: panel.EventCategoryLLM, Kind: "llm.a", Level: panel.EventLevelDebug, Module: "llmchat", TraceID: "trace-1"},
		{Category: panel.EventCategoryLLM, Kind: "llm.b", Level: panel.EventLevelError, Module: "llmchat", TraceID: "trace-2"},
		{Category: panel.EventCategoryMemory, Kind: "memory.a", Level: panel.EventLevelInfo, Module: "memory"},
	} {
		item, err := store.RecordEvent(ctx, event)
		if err != nil {
			t.Fatalf("RecordEvent(%s) failed: %v", event.Kind, err)
		}
		recorded = append(recorded, item)
	}

	tests := []struct {
		name         string
		query        panel.EventQuery
		wantIDs      []int64
		wantHasOlder bool
		wantHasNewer bool
	}{
		{name: "latest starts at newest", query: panel.EventQuery{Limit: 2}, wantIDs: []int64{recorded[3].ID, recorded[2].ID}, wantHasOlder: true},
		{name: "newer advances without skipping", query: panel.EventQuery{AfterID: recorded[0].ID, Limit: 2}, wantIDs: []int64{recorded[3].ID, recorded[2].ID}},
		{name: "older continues history", query: panel.EventQuery{BeforeID: recorded[2].ID, Limit: 2}, wantIDs: []int64{recorded[0].ID}},
		{name: "filters apply to latest", query: panel.EventQuery{Limit: 1, Category: panel.EventCategoryLLM, Module: "llmchat"}, wantIDs: []int64{recorded[2].ID}},
		{name: "filters apply to older", query: panel.EventQuery{BeforeID: recorded[2].ID, Limit: 1, Category: panel.EventCategoryLLM}},
		{name: "debug is available explicitly", query: panel.EventQuery{Limit: 2, Level: panel.EventLevelDebug}, wantIDs: []int64{recorded[1].ID}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			page, err := store.ListEvents(ctx, test.query)
			if err != nil {
				t.Fatalf("ListEvents failed: %v", err)
			}
			if page.WindowStartID != recorded[0].ID || page.WindowEndID != recorded[3].ID {
				t.Fatalf("window = [%d,%d], want [%d,%d]", page.WindowStartID, page.WindowEndID, recorded[0].ID, recorded[3].ID)
			}
			gotIDs := make([]int64, len(page.Items))
			for index := range page.Items {
				gotIDs[index] = page.Items[index].ID
			}
			if !slices.Equal(gotIDs, test.wantIDs) {
				t.Fatalf("IDs = %v, want %v", gotIDs, test.wantIDs)
			}
			if page.HasOlder != test.wantHasOlder || page.HasNewer != test.wantHasNewer {
				t.Fatalf("page flags = older:%t newer:%t, want older:%t newer:%t", page.HasOlder, page.HasNewer, test.wantHasOlder, test.wantHasNewer)
			}
			if len(test.wantIDs) > 0 && (page.NewestID != test.wantIDs[0] || page.OldestID != test.wantIDs[len(test.wantIDs)-1]) {
				t.Fatalf("page cursors = [%d,%d], want [%d,%d]", page.OldestID, page.NewestID, test.wantIDs[len(test.wantIDs)-1], test.wantIDs[0])
			}
		})
	}
}

func TestSQLiteListEventsRejectsMixedCursors(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	_, err := store.ListEvents(context.Background(), panel.EventQuery{AfterID: 1, BeforeID: 2})
	if !errors.Is(err, panel.ErrInvalidQuery) {
		t.Fatalf("ListEvents error = %v, want ErrInvalidQuery", err)
	}
}

func TestSQLiteListEventsNewerPaginationDoesNotSkipBurst(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	ctx := context.Background()
	anchor, err := store.RecordEvent(ctx, panel.Event{
		Category: panel.EventCategoryRuntime,
		Kind:     "runtime.anchor",
		Level:    panel.EventLevelInfo,
	})
	if err != nil {
		t.Fatalf("record anchor: %v", err)
	}

	const burstSize = 20
	var wg sync.WaitGroup
	for index := range burstSize {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, recordErr := store.RecordEvent(ctx, panel.Event{
				Category: panel.EventCategoryRuntime,
				Kind:     fmt.Sprintf("runtime.burst.%d", index),
				Level:    panel.EventLevelInfo,
			}); recordErr != nil {
				t.Errorf("record burst event %d: %v", index, recordErr)
			}
		}()
	}
	wg.Wait()

	cursor := anchor.ID
	seen := make(map[int64]struct{}, burstSize)
	for {
		page, listErr := store.ListEvents(ctx, panel.EventQuery{AfterID: cursor, Limit: 3})
		if listErr != nil {
			t.Fatalf("list newer page: %v", listErr)
		}
		for _, event := range page.Items {
			seen[event.ID] = struct{}{}
		}
		if len(page.Items) > 0 {
			cursor = page.NewestID
		}
		if !page.HasNewer {
			break
		}
	}
	if len(seen) != burstSize {
		t.Fatalf("seen %d burst IDs, want %d", len(seen), burstSize)
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
