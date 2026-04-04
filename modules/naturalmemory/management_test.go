package naturalmemory

import (
	"context"
	"testing"
	"time"

	"ex-otogi/pkg/otogi/ai"
	panel "ex-otogi/pkg/otogi/management"
	"ex-otogi/pkg/otogi/platform"
)

func TestExtractMemoriesEmitsManagementEvents(t *testing.T) {
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
		ConsolidationTimeout:         time.Second,
		MaxMemoriesPerScope:          10,
		DecayFactor:                  0.99,
		MinImportance:                1,
		DuplicateSimilarityThreshold: 0.85,
		ContextWindowSize:            5,
		SynthesisMatchLimit:          5,
		ReflectionMinSourceMemories:  2,
		ReflectionSourceLimit:        5,
		ReflectionMaxGenerated:       1,
	}))
	module.llmMemory = memoryStore
	module.recorder = recorder
	module.embeddingProvider = &embeddingProviderStub{
		response: ai.EmbeddingResponse{Vectors: [][]float32{{1, 0}}},
	}
	module.extractionProvider = &llmProviderStub{
		stream: &llmStreamStub{chunks: []ai.LLMGenerateChunk{
			{Kind: ai.LLMGenerateChunkKindOutputText, Delta: `[{"content":"Alice likes tea","category":"preference","importance":7}]`},
		}},
	}

	ctx := panel.WithTrace(context.Background(), panel.TraceContext{TraceID: "trace-extract"})
	err := module.extractMemories(ctx, ai.LLMMemoryScope{
		Platform:       "telegram",
		ConversationID: "chat-1",
	}, extractionContext{
		ConversationText: "alice: I like tea",
		AnchorTime:       now,
		SourceArticleID:  "a-1",
		SourceActor:      platform.Actor{ID: "user-1", DisplayName: "Alice"},
	})
	if err != nil {
		t.Fatalf("extractMemories failed: %v", err)
	}
	if len(recorder.events) != 2 {
		t.Fatalf("event count = %d, want 2", len(recorder.events))
	}
	if recorder.events[0].Kind != "memory.extract.started" || recorder.events[1].Kind != "memory.extract.completed" {
		t.Fatalf("event kinds = [%q,%q], want extraction start/completed", recorder.events[0].Kind, recorder.events[1].Kind)
	}
	for _, event := range recorder.events {
		if event.TraceID != "trace-extract" {
			t.Fatalf("trace_id = %q, want trace-extract", event.TraceID)
		}
	}
}
