package llmmemory

import (
	"context"
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

	if len(recorder.events) != 2 {
		t.Fatalf("event count = %d, want 2", len(recorder.events))
	}
	if recorder.events[0].Kind != "memory.store.upserted" {
		t.Fatalf("first event kind = %q, want memory.store.upserted", recorder.events[0].Kind)
	}
	if recorder.events[1].Kind != "memory.store.searched" {
		t.Fatalf("second event kind = %q, want memory.store.searched", recorder.events[1].Kind)
	}
	if recorder.events[0].TraceID != "trace-store" || recorder.events[1].TraceID != "trace-store" {
		t.Fatalf("trace IDs = [%q,%q], want trace-store", recorder.events[0].TraceID, recorder.events[1].TraceID)
	}
	if recorder.events[0].PayloadType != "MemoryStoreUpsertedPayload" {
		t.Fatalf("payload_type = %q, want MemoryStoreUpsertedPayload", recorder.events[0].PayloadType)
	}
}
