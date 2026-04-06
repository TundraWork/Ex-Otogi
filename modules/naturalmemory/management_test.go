package naturalmemory

import (
	"context"
	"testing"
	"time"

	"ex-otogi/pkg/otogi/ai"
	panel "ex-otogi/pkg/otogi/management"
	"ex-otogi/pkg/otogi/platform"
)

func TestProcessWindowEmitsManagementEvents(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 16, 12, 0, 0, 0, time.UTC)
	recorder := newRecorderStub()
	memoryStore := &recordingLLMMemoryService{}
	module := New(withClock(func() time.Time { return now }), withConfig(Config{
		Enabled:                      true,
		ExtractionProvider:           "openai-main",
		ExtractionModel:              "gpt-4.1-mini",
		EmbeddingProvider:            "openai-main",
		ExtractionTimeout:            time.Second,
		ExtractionMaxInputRunes:      4000,
		ConsolidationInterval:        0,
		MaxMemoriesPerScope:          10,
		DecayFactor:                  0.99,
		MinImportance:                1,
		DuplicateSimilarityThreshold: 0.85,
		BufferQuietPeriod:            2 * time.Minute,
		BufferMaxRunes:               3000,
		BufferMaxArticles:            30,
		BufferMaxAge:                 10 * time.Minute,
		BufferCheckInterval:          15 * time.Second,
		RetrievalSearchLimit:         20,
		RetrievalPlanningEnabled:     true,
		RetrievalPlanningTimeout:     10 * time.Second,
	}))
	module.llmMemory = memoryStore
	module.memory = &memoryContextStub{}
	module.recorder = recorder
	module.embeddingProvider = &embeddingProviderStub{
		response: ai.EmbeddingResponse{Vectors: [][]float32{{1, 0}}},
	}
	module.extractionProvider = &llmProviderStub{
		stream: &llmStreamStub{chunks: []ai.LLMGenerateChunk{
			{Kind: ai.LLMGenerateChunkKindOutputText, Delta: `[{"action":"new","content":"Alice likes tea","category":"preference","importance":7}]`},
		}},
	}

	ctx := panel.WithTrace(context.Background(), panel.TraceContext{TraceID: "trace-extract"})
	err := module.processWindow(ctx, ai.LLMMemoryScope{
		Platform:       "telegram",
		ConversationID: "chat-1",
	}, []bufferedArticle{
		{
			Article:    platform.Article{ID: "a-1", Text: "I like tea"},
			Actor:      platform.Actor{ID: "user-1", DisplayName: "Alice"},
			OccurredAt: now,
			ReceivedAt: now,
		},
	}, FlushReasonQuiet)
	if err != nil {
		t.Fatalf("processWindow failed: %v", err)
	}
	if len(recorder.events) != 3 {
		t.Fatalf("event count = %d, want 3", len(recorder.events))
	}
	if recorder.events[0].Kind != "memory.window.flushed" {
		t.Fatalf("event[0] kind = %q, want memory.window.flushed", recorder.events[0].Kind)
	}
	if recorder.events[1].Kind != "memory.extract.started" || recorder.events[2].Kind != "memory.extract.completed" {
		t.Fatalf("event kinds = [%q,%q,%q], want flushed/started/completed",
			recorder.events[0].Kind, recorder.events[1].Kind, recorder.events[2].Kind)
	}
	for _, event := range recorder.events {
		if event.TraceID != "trace-extract" {
			t.Fatalf("trace_id = %q, want trace-extract", event.TraceID)
		}
	}
}
