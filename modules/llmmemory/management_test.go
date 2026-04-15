package llmmemory

import (
	"context"
	"strings"
	"testing"

	"ex-otogi/pkg/otogi/ai"
	panel "ex-otogi/pkg/otogi/management"
)

func TestModuleEmitsManagementEventsWithTrace(t *testing.T) {
	t.Parallel()

	recorder := newRecorderStub()
	module := New(withConfig(Config{MaxEntries: 10}))
	module.recorder = recorder

	ctx := panel.WithTrace(context.Background(), panel.TraceContext{TraceID: "trace-store"})
	record, err := module.Store(ctx, ai.LLMMemoryEntry{
		Scope:     testMemoryScope("chat-1"),
		Content:   "stored memory",
		Category:  "knowledge",
		Embedding: []float32{1, 0},
	})
	if err != nil {
		t.Fatalf("Store failed: %v", err)
	}
	if _, err := module.Search(ctx, ai.LLMMemoryQuery{
		Scope:     record.Scope,
		Embedding: append([]float32(nil), record.Embedding...),
		Limit:     5,
	}); err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if _, err := module.Update(ctx, ai.LLMMemoryUpdate{
		ID:        record.ID,
		Content:   "updated memory",
		Category:  "knowledge",
		Embedding: []float32{1, 0},
		Profile:   record.Profile,
	}); err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if err := module.Delete(ctx, record.ID); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	if len(recorder.events) != 4 {
		t.Fatalf("event count = %d, want 4", len(recorder.events))
	}
	if recorder.events[0].Kind != "memory.store.upserted" {
		t.Fatalf("first event kind = %q, want memory.store.upserted", recorder.events[0].Kind)
	}
	if recorder.events[1].Kind != "memory.store.searched" {
		t.Fatalf("second event kind = %q, want memory.store.searched", recorder.events[1].Kind)
	}
	if recorder.events[2].Kind != "memory.store.updated" || recorder.events[3].Kind != "memory.store.deleted" {
		t.Fatalf("tail event kinds = [%q,%q], want memory.store.updated/deleted", recorder.events[2].Kind, recorder.events[3].Kind)
	}
	if !strings.Contains(recorder.events[0].Description, "stored memory") {
		t.Fatalf("upsert description = %q, want stored memory preview", recorder.events[0].Description)
	}
	if recorder.events[1].Description != "1 matches (limit 5)" {
		t.Fatalf("search description = %q, want 1 matches (limit 5)", recorder.events[1].Description)
	}
	if !strings.Contains(recorder.events[2].Description, "updated memory") {
		t.Fatalf("update description = %q, want updated memory preview", recorder.events[2].Description)
	}
	if recorder.events[3].Description != record.ID {
		t.Fatalf("delete description = %q, want %q", recorder.events[3].Description, record.ID)
	}
	for index, event := range recorder.events {
		if event.TraceID != "trace-store" {
			t.Fatalf("events[%d].trace_id = %q, want trace-store", index, event.TraceID)
		}
		if event.Platform != record.Scope.Platform || event.ConversationID != record.Scope.ConversationID {
			t.Fatalf("events[%d] scope = [%q,%q], want [%q,%q]", index, event.Platform, event.ConversationID, record.Scope.Platform, record.Scope.ConversationID)
		}
	}
	if recorder.events[0].PayloadType != "MemoryStoreUpsertedPayload" {
		t.Fatalf("payload_type = %q, want MemoryStoreUpsertedPayload", recorder.events[0].PayloadType)
	}
}
