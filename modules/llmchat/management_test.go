package llmchat

import (
	"context"
	"testing"
	"time"

	"ex-otogi/pkg/otogi/ai"
	panel "ex-otogi/pkg/otogi/management"
)

func TestRetrieveSemanticMemoriesEmitsManagementEvents(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 17, 12, 0, 0, 0, time.UTC)
	recorder := newRecorderStub()
	embeddingProvider := &embeddingProviderStub{
		response: ai.EmbeddingResponse{Vectors: [][]float32{{0.8, 0.2}}},
	}
	memoryService := &llmMemoryServiceStub{
		searchResponse: []ai.LLMMemoryMatch{
			{
				Record: ai.LLMMemoryRecord{
					ID:       "mem-1",
					Content:  "Alice likes tea",
					Category: "preference",
					Profile: ai.LLMMemoryProfile{
						Kind: ai.LLMMemoryKindUnit,
					},
					CreatedAt: now.Add(-time.Hour),
				},
				Similarity: 0.91,
			},
		},
	}
	module := newTestModule(validModuleConfig())
	module.embeddingRegistry = &embeddingRegistryStub{
		providers: map[string]ai.EmbeddingProvider{"embed-main": embeddingProvider},
	}
	module.llmMemory = memoryService
	module.clock = func() time.Time { return now }
	module.recorder = recorder

	agent := module.cfg.Agents[0]
	agent.EmbeddingProvider = "embed-main"
	agent.SemanticMemory = &SemanticMemoryPolicy{
		Enabled:              true,
		MaxRetrievedMemories: 3,
		MinMemorySimilarity:  0.4,
		MaxMemoryRunes:       1000,
	}

	ctx := panel.WithTrace(context.Background(), panel.TraceContext{TraceID: "trace-retrieve"})
	if _, err := module.retrieveSemanticMemories(ctx, testLLMChatEvent("Otogi hi"), agent, "hi"); err != nil {
		t.Fatalf("retrieveSemanticMemories failed: %v", err)
	}
	if len(recorder.events) < 4 {
		t.Fatalf("event count = %d, want at least 4", len(recorder.events))
	}
	if recorder.events[0].Kind != "memory.retrieve.started" {
		t.Fatalf("first event kind = %q, want memory.retrieve.started", recorder.events[0].Kind)
	}
	if recorder.events[len(recorder.events)-1].Kind != "memory.retrieve.completed" {
		t.Fatalf("last event kind = %q, want memory.retrieve.completed", recorder.events[len(recorder.events)-1].Kind)
	}
	for _, event := range recorder.events {
		if event.TraceID != "trace-retrieve" {
			t.Fatalf("event trace_id = %q, want trace-retrieve", event.TraceID)
		}
	}
}
