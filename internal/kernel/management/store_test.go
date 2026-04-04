package management

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"

	panel "ex-otogi/pkg/otogi/management"
)

func TestRecordEventAssignsMonotonicIDs(t *testing.T) {
	t.Parallel()

	service := NewService(Limits{MaxEvents: 16})

	const workers = 24
	var (
		wg      sync.WaitGroup
		results = make(chan int64, workers)
	)
	for i := range workers {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()

			recorded, err := service.RecordEvent(context.Background(), panel.Event{
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

func TestListEventsOrderedAndFiltered(t *testing.T) {
	t.Parallel()

	service := NewService(Limits{MaxEvents: 8})
	ctx := context.Background()

	recorded := make([]panel.Event, 0, 4)
	for _, event := range []panel.Event{
		{Category: panel.EventCategoryRuntime, Kind: "runtime.a", Level: panel.EventLevelInfo, Module: "kernel"},
		{Category: panel.EventCategoryLLM, Kind: "llm.a", Level: panel.EventLevelDebug, Module: "llmchat", TraceID: "trace-1"},
		{Category: panel.EventCategoryLLM, Kind: "llm.b", Level: panel.EventLevelError, Module: "llmchat", TraceID: "trace-2"},
		{Category: panel.EventCategoryMemory, Kind: "memory.a", Level: panel.EventLevelInfo, Module: "naturalmemory"},
	} {
		item, err := service.RecordEvent(ctx, event)
		if err != nil {
			t.Fatalf("RecordEvent(%s) failed: %v", event.Kind, err)
		}
		recorded = append(recorded, item)
	}

	page, err := service.ListEvents(ctx, panel.EventQuery{
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

func TestRetentionEvictsOldestEventsAndArtifacts(t *testing.T) {
	t.Parallel()

	service := NewService(Limits{
		MaxEvents:        2,
		MaxArtifacts:     4,
		MaxArtifactBytes: 1024,
	})
	ctx := context.Background()

	first, err := service.RecordEvent(ctx, panel.Event{Category: panel.EventCategoryLLM, Kind: "llm.one", Level: panel.EventLevelInfo})
	if err != nil {
		t.Fatalf("RecordEvent(first) failed: %v", err)
	}
	if _, err := service.RecordArtifact(ctx, panel.Artifact{ID: "art-1", EventID: first.ID, Kind: panel.ArtifactKindPromptFull, Content: "first prompt"}); err != nil {
		t.Fatalf("RecordArtifact(first) failed: %v", err)
	}

	second, err := service.RecordEvent(ctx, panel.Event{Category: panel.EventCategoryLLM, Kind: "llm.two", Level: panel.EventLevelInfo})
	if err != nil {
		t.Fatalf("RecordEvent(second) failed: %v", err)
	}
	third, err := service.RecordEvent(ctx, panel.Event{Category: panel.EventCategoryLLM, Kind: "llm.three", Level: panel.EventLevelInfo})
	if err != nil {
		t.Fatalf("RecordEvent(third) failed: %v", err)
	}

	page, err := service.ListEvents(ctx, panel.EventQuery{AfterID: 0, Limit: 10})
	if err != nil {
		t.Fatalf("ListEvents failed: %v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("len(Items) = %d, want 2", len(page.Items))
	}
	if page.Items[0].ID != second.ID || page.Items[1].ID != third.ID {
		t.Fatalf("retained IDs = [%d,%d], want [%d,%d]", page.Items[0].ID, page.Items[1].ID, second.ID, third.ID)
	}

	if _, err := service.GetArtifact(ctx, "art-1"); !errors.Is(err, panel.ErrArtifactNotFound) {
		t.Fatalf("GetArtifact(evicted) error = %v, want ErrArtifactNotFound", err)
	}
}

func TestCursorResetRequiredWhenAfterIDFallsBehindWindow(t *testing.T) {
	t.Parallel()

	service := NewService(Limits{MaxEvents: 3})
	ctx := context.Background()

	for index := range 5 {
		if _, err := service.RecordEvent(ctx, panel.Event{
			Category: panel.EventCategoryRuntime,
			Kind:     fmt.Sprintf("runtime.%d", index),
			Level:    panel.EventLevelInfo,
		}); err != nil {
			t.Fatalf("RecordEvent(%d) failed: %v", index, err)
		}
	}

	page, err := service.ListEvents(ctx, panel.EventQuery{AfterID: 1, Limit: 10})
	if err != nil {
		t.Fatalf("ListEvents failed: %v", err)
	}
	if !page.CursorResetRequired {
		t.Fatal("CursorResetRequired = false, want true")
	}
	if page.WindowStartID != 3 || page.WindowEndID != 5 {
		t.Fatalf("window = [%d,%d], want [3,5]", page.WindowStartID, page.WindowEndID)
	}
}

func TestArtifactLookupAndByteEviction(t *testing.T) {
	t.Parallel()

	service := NewService(Limits{
		MaxEvents:        4,
		MaxArtifacts:     8,
		MaxArtifactBytes: 10,
	})
	ctx := context.Background()

	eventOne, err := service.RecordEvent(ctx, panel.Event{Category: panel.EventCategoryLLM, Kind: "llm.one", Level: panel.EventLevelInfo})
	if err != nil {
		t.Fatalf("RecordEvent(eventOne) failed: %v", err)
	}
	if _, err := service.RecordArtifact(ctx, panel.Artifact{
		ID:      "art-a",
		EventID: eventOne.ID,
		Kind:    panel.ArtifactKindPromptFull,
		Content: "12345",
	}); err != nil {
		t.Fatalf("RecordArtifact(art-a) failed: %v", err)
	}

	eventTwo, err := service.RecordEvent(ctx, panel.Event{Category: panel.EventCategoryLLM, Kind: "llm.two", Level: panel.EventLevelInfo})
	if err != nil {
		t.Fatalf("RecordEvent(eventTwo) failed: %v", err)
	}
	if _, err := service.RecordArtifact(ctx, panel.Artifact{
		ID:      "art-b",
		EventID: eventTwo.ID,
		Kind:    panel.ArtifactKindPromptFull,
		Content: "67890",
	}); err != nil {
		t.Fatalf("RecordArtifact(art-b) failed: %v", err)
	}
	if _, err := service.RecordArtifact(ctx, panel.Artifact{
		ID:      "art-c",
		EventID: eventTwo.ID,
		Kind:    panel.ArtifactKindPromptResponse,
		Content: "abc",
	}); err != nil {
		t.Fatalf("RecordArtifact(art-c) failed: %v", err)
	}

	if _, err := service.GetArtifact(ctx, "art-a"); !errors.Is(err, panel.ErrArtifactNotFound) {
		t.Fatalf("GetArtifact(art-a) error = %v, want ErrArtifactNotFound", err)
	}
	artifact, err := service.GetArtifact(ctx, "art-c")
	if err != nil {
		t.Fatalf("GetArtifact(art-c) failed: %v", err)
	}
	if artifact.Content != "abc" {
		t.Fatalf("artifact.Content = %q, want %q", artifact.Content, "abc")
	}
}

func TestRecordArtifactLinksArtifactIDBackToEvent(t *testing.T) {
	t.Parallel()

	service := NewService(Limits{
		MaxEvents:        4,
		MaxArtifacts:     4,
		MaxArtifactBytes: 1024,
	})
	ctx := context.Background()

	recordedEvent, err := service.RecordEvent(ctx, panel.Event{
		Category: panel.EventCategoryBusinessEvent,
		Kind:     "platform.event.received",
		Level:    panel.EventLevelDebug,
	})
	if err != nil {
		t.Fatalf("RecordEvent failed: %v", err)
	}

	if _, err := service.RecordArtifact(ctx, panel.Artifact{
		ID:      "art-linked",
		EventID: recordedEvent.ID,
		Kind:    panel.ArtifactKindStructuredDebug,
		Content: "{\"kind\":\"article.created\"}",
	}); err != nil {
		t.Fatalf("RecordArtifact failed: %v", err)
	}

	reloadedEvent, err := service.GetEvent(ctx, recordedEvent.ID)
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
