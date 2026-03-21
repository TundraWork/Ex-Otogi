package naturalmemory

import (
	"context"
	"strings"
	"testing"
	"time"

	"ex-otogi/pkg/otogi/ai"
	"ex-otogi/pkg/otogi/platform"
)

func TestProcessCandidateUsesSynthesisRewrite(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 16, 12, 0, 0, 0, time.UTC)
	memoryStore := &recordingLLMMemoryService{
		searchResp: []ai.LLMMemoryMatch{
			{
				Record: ai.LLMMemoryRecord{
					ID:       "mem-1",
					Scope:    ai.LLMMemoryScope{Platform: "telegram", ConversationID: "chat-1"},
					Content:  "Alice likes tea",
					Category: "preference",
					Profile: ai.LLMMemoryProfile{
						Kind:           ai.LLMMemoryKindUnit,
						Importance:     6,
						LastAccessedAt: now.Add(-2 * time.Hour),
					},
					Embedding: []float32{1, 0},
				},
				Similarity: 0.95,
			},
			{
				Record: ai.LLMMemoryRecord{
					ID:       "mem-2",
					Scope:    ai.LLMMemoryScope{Platform: "telegram", ConversationID: "chat-1"},
					Content:  "Alice likes jasmine tea",
					Category: "preference",
					Profile: ai.LLMMemoryProfile{
						Kind:           ai.LLMMemoryKindUnit,
						Importance:     7,
						LastAccessedAt: now.Add(-1 * time.Hour),
					},
					Embedding: []float32{0.9, 0.1},
				},
				Similarity: 0.92,
			},
		},
	}
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
		RetrievalPlanningEnabled:     true,
		RetrievalPlanningTimeout:     10 * time.Second,
	}))
	module.llmMemory = memoryStore
	module.embeddingProvider = &embeddingProviderStub{
		response: ai.EmbeddingResponse{Vectors: [][]float32{{1, 0}}},
	}
	module.extractionProvider = &llmProviderStub{
		stream: &llmStreamStub{chunks: []ai.LLMGenerateChunk{
			{Kind: ai.LLMGenerateChunkKindOutputText, Delta: `{"action":"rewrite","target_id":"mem-1","content":"Alice prefers jasmine tea","category":"preference","importance":9,"subject_actor_id":"user-1","subject_actor_name":"Alice","absorbed_record_ids":["mem-2"]}`},
		}},
	}

	err := module.processCandidate(context.Background(), ai.LLMMemoryScope{
		Platform:       "telegram",
		ConversationID: "chat-1",
	}, extractionContext{
		AnchorTime:      now,
		SourceArticleID: "a-2",
		SourceActor:     platform.Actor{ID: "user-1", DisplayName: "Alice"},
		Participants: []ai.LLMMemoryActorRef{
			{ID: "user-1", Name: "Alice"},
		},
	}, extractedMemory{
		Content:          "Alice likes jasmine tea",
		Category:         "preference",
		Importance:       8,
		SubjectActorID:   "user-1",
		SubjectActorName: "Alice",
	})
	if err != nil {
		t.Fatalf("processCandidate failed: %v", err)
	}

	if len(memoryStore.updates) != 1 {
		t.Fatalf("updates len = %d, want 1", len(memoryStore.updates))
	}
	update := memoryStore.updates[0]
	if update.ID != "mem-1" {
		t.Fatalf("update id = %q, want mem-1", update.ID)
	}
	if update.Content != "Alice prefers jasmine tea" {
		t.Fatalf("update content = %q, want rewritten content", update.Content)
	}
	if update.Profile.Kind != ai.LLMMemoryKindSynthesized {
		t.Fatalf("profile.kind = %q, want %q", update.Profile.Kind, ai.LLMMemoryKindSynthesized)
	}
	if update.Profile.Importance != 9 {
		t.Fatalf("profile.importance = %d, want 9", update.Profile.Importance)
	}
	if len(update.Profile.EvidenceRecordIDs) != 1 || update.Profile.EvidenceRecordIDs[0] != "mem-2" {
		t.Fatalf("evidence ids = %v, want [mem-2]", update.Profile.EvidenceRecordIDs)
	}
	if got := update.Metadata[ai.LLMMemoryMetadataSourceRecordIDs]; got != "mem-2" {
		t.Fatalf("metadata[source_record_ids] = %q, want mem-2", got)
	}
	if len(memoryStore.deleted) != 1 || memoryStore.deleted[0] != "mem-2" {
		t.Fatalf("deleted = %v, want [mem-2]", memoryStore.deleted)
	}
}

func TestProcessCandidateUsesSynthesisSupersede(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 16, 12, 0, 0, 0, time.UTC)
	memoryStore := &recordingLLMMemoryService{
		searchResp: []ai.LLMMemoryMatch{
			{
				Record: ai.LLMMemoryRecord{
					ID:       "mem-1",
					Scope:    ai.LLMMemoryScope{Platform: "telegram", ConversationID: "chat-1"},
					Content:  "Alice likes coffee",
					Category: "preference",
					Profile: ai.LLMMemoryProfile{
						Kind:           ai.LLMMemoryKindUnit,
						Importance:     6,
						LastAccessedAt: now.Add(-2 * time.Hour),
					},
					Embedding: []float32{1, 0},
				},
				Similarity: 0.90,
			},
		},
	}
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
		RetrievalPlanningEnabled:     true,
		RetrievalPlanningTimeout:     10 * time.Second,
	}))
	module.llmMemory = memoryStore
	module.embeddingProvider = &embeddingProviderStub{
		response: ai.EmbeddingResponse{Vectors: [][]float32{{1, 0}}},
	}
	module.extractionProvider = &llmProviderStub{
		stream: &llmStreamStub{chunks: []ai.LLMGenerateChunk{
			{Kind: ai.LLMGenerateChunkKindOutputText, Delta: `{"action":"supersede","target_id":"mem-1","content":"Alice dislikes coffee and prefers tea","category":"preference","importance":8,"subject_actor_id":"user-1","subject_actor_name":"Alice","absorbed_record_ids":[]}`},
		}},
	}

	err := module.processCandidate(context.Background(), ai.LLMMemoryScope{
		Platform:       "telegram",
		ConversationID: "chat-1",
	}, extractionContext{
		AnchorTime:      now,
		SourceArticleID: "a-3",
		SourceActor:     platform.Actor{ID: "user-1", DisplayName: "Alice"},
		Participants: []ai.LLMMemoryActorRef{
			{ID: "user-1", Name: "Alice"},
		},
	}, extractedMemory{
		Content:          "Alice dislikes coffee and prefers tea",
		Category:         "preference",
		Importance:       8,
		SubjectActorID:   "user-1",
		SubjectActorName: "Alice",
	})
	if err != nil {
		t.Fatalf("processCandidate failed: %v", err)
	}

	if len(memoryStore.deleted) != 1 || memoryStore.deleted[0] != "mem-1" {
		t.Fatalf("deleted = %v, want [mem-1]", memoryStore.deleted)
	}
	if len(memoryStore.storedEntries) != 1 {
		t.Fatalf("stored entries len = %d, want 1", len(memoryStore.storedEntries))
	}
	entry := memoryStore.storedEntries[0]
	if entry.Content != "Alice dislikes coffee and prefers tea" {
		t.Fatalf("content = %q, want superseding content", entry.Content)
	}
	if entry.Profile.Importance != 8 {
		t.Fatalf("importance = %d, want 8", entry.Profile.Importance)
	}
	if len(memoryStore.updates) != 0 {
		t.Fatalf("updates len = %d, want 0 (supersede should not update)", len(memoryStore.updates))
	}
}

func TestNormalizeSynthesisDecisionSupersede(t *testing.T) {
	t.Parallel()

	matches := []ai.LLMMemoryMatch{
		{Record: ai.LLMMemoryRecord{ID: "mem-1", Content: "Alice likes coffee"}, Similarity: 0.9},
	}
	candidate := extractedMemory{
		Content:    "Alice dislikes coffee",
		Category:   "preference",
		Importance: 7,
	}

	tests := []struct {
		name       string
		decision   synthesisDecision
		wantAction synthesisAction
	}{
		{
			name: "valid supersede",
			decision: synthesisDecision{
				Action:   synthesisActionSupersede,
				TargetID: "mem-1",
				Content:  "Alice dislikes coffee",
			},
			wantAction: synthesisActionSupersede,
		},
		{
			name: "supersede with invalid target falls back",
			decision: synthesisDecision{
				Action:   synthesisActionSupersede,
				TargetID: "mem-999",
				Content:  "Alice dislikes coffee",
			},
			wantAction: synthesisActionAdd,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			fallback := synthesisDecision{Action: synthesisActionAdd, Content: candidate.Content, Category: candidate.Category, Importance: candidate.Importance}
			result := normalizeSynthesisDecision(testCase.decision, candidate, matches, fallback)
			if result.Action != testCase.wantAction {
				t.Fatalf("action = %q, want %q", result.Action, testCase.wantAction)
			}
		})
	}
}

func TestDecideSynthesisFallbackWithNoMatches(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 16, 12, 0, 0, 0, time.UTC)
	module := New(withClock(func() time.Time { return now }), withConfig(Config{
		Enabled:                      true,
		DuplicateSimilarityThreshold: 0.85,
	}))

	decision := module.decideSynthesis(
		context.Background(),
		extractedMemory{Content: "Alice likes tea", Category: "preference", Importance: 7},
		nil,
		extractionContext{AnchorTime: now},
	)
	if decision.Action != synthesisActionAdd {
		t.Fatalf("action = %q, want add (fallback with no matches)", decision.Action)
	}
	if decision.Content != "Alice likes tea" {
		t.Fatalf("content = %q, want original candidate content", decision.Content)
	}
}

func TestRenderSynthesisPromptIncludesSupersedeAction(t *testing.T) {
	t.Parallel()

	prompt := renderSynthesisDecisionPrompt(
		extractedMemory{Content: "Alice dislikes coffee", Category: "preference", Importance: 7},
		[]ai.LLMMemoryMatch{
			{Record: ai.LLMMemoryRecord{ID: "mem-1", Content: "Alice likes coffee", Category: "preference"}, Similarity: 0.9},
		},
		extractionContext{AnchorTime: time.Date(2026, time.March, 16, 12, 0, 0, 0, time.UTC)},
	)

	if !strings.Contains(prompt, "supersede") {
		t.Fatalf("prompt should contain supersede action, got %q", prompt)
	}
	if !strings.Contains(prompt, "contradicts") {
		t.Fatalf("prompt should contain contradiction guidance, got %q", prompt)
	}
}

func TestMaybeGenerateReflectionsStoresSynthesizedMemory(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 16, 12, 0, 0, 0, time.UTC)
	memoryStore := &recordingLLMMemoryService{}
	module := New(withClock(func() time.Time { return now }), withConfig(Config{
		Enabled:                      true,
		ExtractionProvider:           "openai-main",
		ExtractionModel:              "gpt-4.1-mini",
		EmbeddingProvider:            "openai-main",
		ExtractionTimeout:            time.Second,
		ExtractionMaxInputRunes:      4000,
		ConsolidationInterval:        time.Hour,
		ConsolidationProvider:        "openai-main",
		ConsolidationModel:           "gpt-4.1-mini",
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
		RetrievalPlanningEnabled:     true,
		RetrievalPlanningTimeout:     10 * time.Second,
	}))
	module.llmMemory = memoryStore
	module.embeddingProvider = &embeddingProviderStub{
		response: ai.EmbeddingResponse{Vectors: [][]float32{{1, 0}}},
	}
	module.consolidationProvider = &llmProviderStub{
		stream: &llmStreamStub{chunks: []ai.LLMGenerateChunk{
			{Kind: ai.LLMGenerateChunkKindOutputText, Delta: `[{"content":"Alice consistently prefers tea over coffee","importance":8,"subject_actor_id":"user-1","subject_actor_name":"Alice","source_record_ids":["mem-1","mem-2"]}]`},
		}},
	}

	err := module.maybeGenerateReflections(context.Background(), ai.LLMMemoryScope{
		Platform:       "telegram",
		ConversationID: "chat-1",
	}, []ai.LLMMemoryRecord{
		{
			ID:       "mem-1",
			Scope:    ai.LLMMemoryScope{Platform: "telegram", ConversationID: "chat-1"},
			Content:  "Alice likes tea",
			Category: "preference",
			Profile: ai.LLMMemoryProfile{
				Kind:           ai.LLMMemoryKindUnit,
				Importance:     7,
				LastAccessedAt: now.Add(-1 * time.Hour),
				SubjectActor:   &ai.LLMMemoryActorRef{ID: "user-1", Name: "Alice"},
			},
		},
		{
			ID:       "mem-2",
			Scope:    ai.LLMMemoryScope{Platform: "telegram", ConversationID: "chat-1"},
			Content:  "Alice dislikes coffee",
			Category: "preference",
			Profile: ai.LLMMemoryProfile{
				Kind:           ai.LLMMemoryKindUnit,
				Importance:     6,
				LastAccessedAt: now.Add(-30 * time.Minute),
				SubjectActor:   &ai.LLMMemoryActorRef{ID: "user-1", Name: "Alice"},
			},
		},
	}, now)
	if err != nil {
		t.Fatalf("maybeGenerateReflections failed: %v", err)
	}

	if len(memoryStore.storedEntries) != 1 {
		t.Fatalf("stored entries len = %d, want 1", len(memoryStore.storedEntries))
	}
	entry := memoryStore.storedEntries[0]
	if entry.Category != "reflection" {
		t.Fatalf("category = %q, want reflection", entry.Category)
	}
	if entry.Profile.Kind != ai.LLMMemoryKindSynthesized {
		t.Fatalf("profile.kind = %q, want %q", entry.Profile.Kind, ai.LLMMemoryKindSynthesized)
	}
	if entry.Profile.Source != "natural.reflection" {
		t.Fatalf("profile.source = %q, want natural.reflection", entry.Profile.Source)
	}
	if len(entry.Profile.EvidenceRecordIDs) != 2 {
		t.Fatalf("evidence ids = %v, want 2 ids", entry.Profile.EvidenceRecordIDs)
	}
	if !strings.Contains(entry.Content, "prefers tea over coffee") {
		t.Fatalf("content = %q, want synthesized reflection", entry.Content)
	}
}
