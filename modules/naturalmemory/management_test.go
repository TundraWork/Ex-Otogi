package naturalmemory

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

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
	if !strings.Contains(recorder.events[0].Description, "I like tea") {
		t.Fatalf("flushed description = %q, want article preview", recorder.events[0].Description)
	}
	if !strings.Contains(recorder.events[1].Description, "I like tea") {
		t.Fatalf("extract started description = %q, want conversation preview", recorder.events[1].Description)
	}
	if !strings.Contains(recorder.events[2].Description, "Alice likes tea") {
		t.Fatalf("extract completed description = %q, want candidate preview", recorder.events[2].Description)
	}
	for _, event := range recorder.events {
		if event.TraceID != "trace-extract" {
			t.Fatalf("trace_id = %q, want trace-extract", event.TraceID)
		}
		if event.Platform != "telegram" || event.ConversationID != "chat-1" {
			t.Fatalf("event scope = [%q,%q], want telegram/chat-1", event.Platform, event.ConversationID)
		}
	}

	flushedPayload, ok := recorder.events[0].Payload.(panel.MemoryWindowFlushedPayload)
	if !ok {
		t.Fatalf("flushed payload type = %T, want MemoryWindowFlushedPayload", recorder.events[0].Payload)
	}
	if flushedPayload.ArticleCount != 1 || flushedPayload.RuneCount != utf8.RuneCountInString("I like tea") {
		t.Fatalf("flushed payload = %+v, want 1 article and matching rune count", flushedPayload)
	}

	startedPayload, ok := recorder.events[1].Payload.(panel.MemoryExtractStartedPayload)
	if !ok {
		t.Fatalf("started payload type = %T, want MemoryExtractStartedPayload", recorder.events[1].Payload)
	}
	if startedPayload.ExistingMemoryCount != 0 || startedPayload.InputRunes <= 0 {
		t.Fatalf("started payload = %+v, want zero existing memories and positive input runes", startedPayload)
	}

	completedPayload, ok := recorder.events[2].Payload.(panel.MemoryExtractCompletedPayload)
	if !ok {
		t.Fatalf("completed payload type = %T, want MemoryExtractCompletedPayload", recorder.events[2].Payload)
	}
	if completedPayload.ExtractedCount != 1 || completedPayload.AppliedCount != 1 || completedPayload.FailedCount != 0 {
		t.Fatalf("completed payload = %+v, want extracted=1 applied=1 failed=0", completedPayload)
	}
}

func TestHandleArticleEmitsWindowEnqueuedEvent(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 16, 12, 0, 0, 0, time.UTC)
	recorder := newRecorderStub()
	module := New(
		withClock(func() time.Time { return now }),
		withConfig(Config{
			Enabled:                      true,
			BufferQuietPeriod:            2 * time.Minute,
			BufferMaxRunes:               3000,
			BufferMaxArticles:            30,
			BufferMaxAge:                 10 * time.Minute,
			BufferCheckInterval:          15 * time.Second,
			ExtractionProvider:           "openai-main",
			ExtractionModel:              "gpt-4.1-mini",
			EmbeddingProvider:            "openai-main",
			MaxMemoriesPerScope:          10,
			DecayFactor:                  0.99,
			MinImportance:                1,
			DuplicateSimilarityThreshold: 0.85,
			RetrievalSearchLimit:         20,
		}),
	)
	module.recorder = recorder
	module.windowManager = newWindowManager(module.cfg, module.clock)

	event := &platform.Event{
		Kind: platform.EventKindArticleCreated,
		Source: platform.EventSource{
			Platform: "telegram",
			ID:       "telegram-main",
		},
		Conversation: platform.Conversation{ID: "chat-1"},
		Actor:        platform.Actor{ID: "user-1"},
		Article:      &platform.Article{ID: "a-1", Text: "hello world"},
		OccurredAt:   now,
	}

	if err := module.handleArticle(context.Background(), event); err != nil {
		t.Fatalf("handleArticle failed: %v", err)
	}
	if len(recorder.events) != 1 {
		t.Fatalf("event count = %d, want 1", len(recorder.events))
	}
	if recorder.events[0].Kind != "memory.window.enqueued" {
		t.Fatalf("event kind = %q, want memory.window.enqueued", recorder.events[0].Kind)
	}
	if !strings.Contains(recorder.events[0].Description, "hello world") {
		t.Fatalf("event description = %q, want article preview", recorder.events[0].Description)
	}
	if recorder.events[0].Platform != "telegram" || recorder.events[0].ConversationID != "chat-1" {
		t.Fatalf("event scope = [%q,%q], want telegram/chat-1", recorder.events[0].Platform, recorder.events[0].ConversationID)
	}
	payload, ok := recorder.events[0].Payload.(panel.MemoryWindowEnqueuedPayload)
	if !ok {
		t.Fatalf("payload type = %T, want MemoryWindowEnqueuedPayload", recorder.events[0].Payload)
	}
	if payload.ArticleID != "a-1" || payload.WindowArticleCount != 1 || payload.WindowRuneCount != utf8.RuneCountInString("hello world") {
		t.Fatalf("payload = %+v, want article a-1 count 1 rune count %d", payload, utf8.RuneCountInString("hello world"))
	}
}

func TestProcessWindowEmitsSkipEventForEmptySerializedConversation(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 16, 12, 0, 0, 0, time.UTC)
	recorder := newRecorderStub()
	module := New(withClock(func() time.Time { return now }), withConfig(Config{
		Enabled:                      true,
		ExtractionProvider:           "openai-main",
		ExtractionModel:              "gpt-4.1-mini",
		EmbeddingProvider:            "openai-main",
		ExtractionTimeout:            time.Second,
		ExtractionMaxInputRunes:      8,
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
	module.llmMemory = &recordingLLMMemoryService{}
	module.memory = &memoryContextStub{}
	module.recorder = recorder
	module.embeddingProvider = &embeddingProviderStub{
		response: ai.EmbeddingResponse{Vectors: [][]float32{{1, 0}}},
	}
	module.extractionProvider = &llmProviderStub{
		stream: &llmStreamStub{chunks: []ai.LLMGenerateChunk{{Kind: ai.LLMGenerateChunkKindOutputText, Delta: `[]`}}},
	}

	err := module.processWindow(context.Background(), ai.LLMMemoryScope{
		Platform:       "telegram",
		ConversationID: "chat-1",
	}, []bufferedArticle{{
		Article:    platform.Article{ID: "a-1", Text: "hello world"},
		Actor:      platform.Actor{ID: "user-1", DisplayName: "Alice"},
		OccurredAt: now,
		ReceivedAt: now,
	}}, FlushReasonQuiet)
	if err != nil {
		t.Fatalf("processWindow failed: %v", err)
	}

	if len(recorder.events) != 2 {
		t.Fatalf("event count = %d, want 2", len(recorder.events))
	}
	if recorder.events[1].Kind != "memory.window.skipped" {
		t.Fatalf("event[1] kind = %q, want memory.window.skipped", recorder.events[1].Kind)
	}
	if !strings.Contains(recorder.events[1].Description, "hello world") {
		t.Fatalf("skip description = %q, want article preview", recorder.events[1].Description)
	}
}

func TestProcessWindowEmitsParseFailureEvent(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 16, 12, 0, 0, 0, time.UTC)
	recorder := newRecorderStub()
	module := New(withClock(func() time.Time { return now }), withConfig(Config{
		Enabled:                      true,
		ExtractionProvider:           "openai-main",
		ExtractionModel:              "gpt-4.1-mini",
		EmbeddingProvider:            "openai-main",
		ExtractionTimeout:            time.Second,
		ExtractionMaxInputRunes:      4000,
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
	module.llmMemory = &recordingLLMMemoryService{}
	module.memory = &memoryContextStub{}
	module.recorder = recorder
	module.embeddingProvider = &embeddingProviderStub{
		response: ai.EmbeddingResponse{Vectors: [][]float32{{1, 0}}},
	}
	module.extractionProvider = &llmProviderStub{
		stream: &llmStreamStub{chunks: []ai.LLMGenerateChunk{
			{Kind: ai.LLMGenerateChunkKindOutputText, Delta: `not-json`},
		}},
	}

	err := module.processWindow(context.Background(), ai.LLMMemoryScope{
		Platform:       "telegram",
		ConversationID: "chat-1",
	}, []bufferedArticle{{
		Article:    platform.Article{ID: "a-1", Text: "I like tea"},
		Actor:      platform.Actor{ID: "user-1", DisplayName: "Alice"},
		OccurredAt: now,
		ReceivedAt: now,
	}}, FlushReasonQuiet)
	if err != nil {
		t.Fatalf("processWindow failed: %v", err)
	}

	if len(recorder.events) != 4 {
		t.Fatalf("event count = %d, want 4", len(recorder.events))
	}
	if recorder.events[2].Kind != "memory.extract.parse_failed" {
		t.Fatalf("event[2] kind = %q, want memory.extract.parse_failed", recorder.events[2].Kind)
	}
	if !strings.Contains(recorder.events[2].Description, "not-json") {
		t.Fatalf("parse failure description = %q, want response preview", recorder.events[2].Description)
	}
	completedPayload, ok := recorder.events[3].Payload.(panel.MemoryExtractCompletedPayload)
	if !ok {
		t.Fatalf("completed payload type = %T, want MemoryExtractCompletedPayload", recorder.events[3].Payload)
	}
	if completedPayload.ExtractedCount != 0 || completedPayload.AppliedCount != 0 || completedPayload.FailedCount != 0 {
		t.Fatalf("completed payload = %+v, want zero counts after parse failure", completedPayload)
	}
}

func TestProcessWindowEmitsApplyFailureEvent(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 16, 12, 0, 0, 0, time.UTC)
	recorder := newRecorderStub()
	memoryStore := &recordingLLMMemoryService{storeErr: errors.New("store failed")}
	module := New(withClock(func() time.Time { return now }), withConfig(Config{
		Enabled:                      true,
		ExtractionProvider:           "openai-main",
		ExtractionModel:              "gpt-4.1-mini",
		EmbeddingProvider:            "openai-main",
		ExtractionTimeout:            time.Second,
		ExtractionMaxInputRunes:      4000,
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

	err := module.processWindow(context.Background(), ai.LLMMemoryScope{
		Platform:       "telegram",
		ConversationID: "chat-1",
	}, []bufferedArticle{{
		Article:    platform.Article{ID: "a-1", Text: "I like tea"},
		Actor:      platform.Actor{ID: "user-1", DisplayName: "Alice"},
		OccurredAt: now,
		ReceivedAt: now,
	}}, FlushReasonQuiet)
	if err != nil {
		t.Fatalf("processWindow failed: %v", err)
	}

	if len(recorder.events) != 4 {
		t.Fatalf("event count = %d, want 4", len(recorder.events))
	}
	if recorder.events[2].Kind != "memory.extract.apply_failed" {
		t.Fatalf("event[2] kind = %q, want memory.extract.apply_failed", recorder.events[2].Kind)
	}
	if !strings.Contains(recorder.events[2].Description, "Alice likes tea") || !strings.Contains(recorder.events[2].Description, "store failed") {
		t.Fatalf("apply failure description = %q, want candidate and error detail", recorder.events[2].Description)
	}
	completedPayload, ok := recorder.events[3].Payload.(panel.MemoryExtractCompletedPayload)
	if !ok {
		t.Fatalf("completed payload type = %T, want MemoryExtractCompletedPayload", recorder.events[3].Payload)
	}
	if completedPayload.ExtractedCount != 1 || completedPayload.AppliedCount != 0 || completedPayload.FailedCount != 1 {
		t.Fatalf("completed payload = %+v, want extracted=1 applied=0 failed=1", completedPayload)
	}
}

func TestFlushWorkerEmitsProcessingFailedEvent(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 16, 12, 0, 0, 0, time.UTC)
	recorder := newRecorderStub()
	manager := newWindowManager(Config{
		BufferQuietPeriod: 0,
		BufferMaxRunes:    3000,
		BufferMaxArticles: 30,
		BufferMaxAge:      10 * time.Minute,
	}, func() time.Time { return now })
	scope := ai.LLMMemoryScope{
		Platform:       "telegram",
		ConversationID: "chat-1",
	}
	manager.Enqueue(scope, bufferedArticle{
		Article:    platform.Article{ID: "a-1", Text: "hello"},
		Actor:      platform.Actor{ID: "user-1"},
		OccurredAt: now,
		ReceivedAt: now,
	})

	worker := &flushWorker{
		clock:    func() time.Time { return now },
		manager:  manager,
		recorder: recorder,
		process: func(context.Context, readyWindow) error {
			return errors.New("boom")
		},
	}

	worker.flushReady(context.Background())

	if len(recorder.events) != 1 {
		t.Fatalf("event count = %d, want 1", len(recorder.events))
	}
	if recorder.events[0].Kind != "memory.window.processing.failed" {
		t.Fatalf("event kind = %q, want memory.window.processing.failed", recorder.events[0].Kind)
	}
	if !strings.Contains(recorder.events[0].Description, "hello") || !strings.Contains(recorder.events[0].Description, "boom") {
		t.Fatalf("processing failure description = %q, want window preview and error", recorder.events[0].Description)
	}
}

func TestRunConsolidationCycleEmitsManagementEvent(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 16, 12, 0, 0, 0, time.UTC)
	recorder := newRecorderStub()
	module := New(withClock(func() time.Time { return now }), withConfig(Config{
		Enabled:               true,
		ConsolidationInterval: time.Minute,
	}))
	module.recorder = recorder
	module.windowManager = newWindowManager(module.cfg, module.clock)
	module.llmMemory = &recordingLLMMemoryService{}

	ctx := panel.WithTrace(context.Background(), panel.TraceContext{TraceID: "trace-consolidation"})
	if err := module.runConsolidationCycle(ctx); err != nil {
		t.Fatalf("runConsolidationCycle failed: %v", err)
	}

	if len(recorder.events) != 1 {
		t.Fatalf("event count = %d, want 1", len(recorder.events))
	}
	if recorder.events[0].Kind != "memory.consolidation.cycle.completed" {
		t.Fatalf("event kind = %q, want memory.consolidation.cycle.completed", recorder.events[0].Kind)
	}
	if recorder.events[0].Description != "0 scopes in 0ms" {
		t.Fatalf("event description = %q, want 0 scopes in 0ms", recorder.events[0].Description)
	}
}
