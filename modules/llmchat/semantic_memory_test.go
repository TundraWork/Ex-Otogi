package llmchat

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"ex-otogi/pkg/otogi/ai"
	"ex-otogi/pkg/otogi/core"
	"ex-otogi/pkg/otogi/platform"
)

func TestRetrieveSemanticMemoriesSerializesMatches(t *testing.T) {
	now := time.Date(2026, time.March, 17, 12, 0, 0, 0, time.UTC)
	embeddingProvider := &embeddingProviderStub{
		response: ai.EmbeddingResponse{Vectors: [][]float32{{0.8, 0.2}}},
	}
	memoryService := &llmMemoryServiceStub{
		searchResponse: []ai.LLMMemoryMatch{
			{
				Record: ai.LLMMemoryRecord{
					ID:       "mem-1",
					Content:  `User prefers "<tea>"`,
					Category: "preference",
					Profile: ai.LLMMemoryProfile{
						Kind:           ai.LLMMemoryKindUnit,
						Importance:     7,
						AccessCount:    1,
						LastAccessedAt: now.Add(-2 * time.Hour),
						SubjectActor:   &ai.LLMMemoryActorRef{ID: "user-1", Name: "Alice"},
					},
					CreatedAt: time.Date(2026, time.March, 10, 12, 0, 0, 0, time.UTC),
				},
				Similarity: 0.91,
			},
			{
				Record: ai.LLMMemoryRecord{
					ID:       "mem-2",
					Content:  "User studies computer science",
					Category: "knowledge",
					Profile: ai.LLMMemoryProfile{
						Kind:           ai.LLMMemoryKindSynthesized,
						Importance:     6,
						AccessCount:    3,
						LastAccessedAt: now.Add(-24 * time.Hour),
						SourceActor:    &ai.LLMMemoryActorRef{ID: "user-2", Name: "Bob"},
					},
					CreatedAt: time.Date(2026, time.March, 9, 15, 30, 0, 0, time.UTC),
				},
				Similarity: 0.72,
			},
		},
	}
	module := newTestModule(validModuleConfig())
	module.embeddingRegistry = &embeddingRegistryStub{
		providers: map[string]ai.EmbeddingProvider{"embed-main": embeddingProvider},
	}
	module.llmMemory = memoryService
	module.clock = func() time.Time { return now }

	agent := module.cfg.Agents[0]
	agent.EmbeddingProvider = "embed-main"
	agent.SemanticMemory = &SemanticMemoryPolicy{
		Enabled:              true,
		MaxRetrievedMemories: 3,
		MinMemorySimilarity:  0.4,
		MaxMemoryRunes:       1000,
	}

	memories, err := module.retrieveSemanticMemories(context.Background(), testLLMChatEvent("Otogi hi"), agent, "hi")
	if err != nil {
		t.Fatalf("retrieveSemanticMemories failed: %v", err)
	}
	if !strings.Contains(memories, `<semantic_memories count="2">`) {
		t.Fatalf("memories = %q, want count=2 wrapper", memories)
	}
	if !strings.Contains(memories, `id="mem-1"`) || !strings.Contains(memories, `id="mem-2"`) {
		t.Fatalf("memories = %q, want both memory ids", memories)
	}
	if !strings.Contains(memories, `kind="unit"`) || !strings.Contains(memories, `kind="synthesized"`) {
		t.Fatalf("memories = %q, want kind attributes", memories)
	}
	if !strings.Contains(memories, `importance="7"`) || !strings.Contains(memories, `importance="6"`) {
		t.Fatalf("memories = %q, want importance attributes", memories)
	}
	if !strings.Contains(memories, `subject_actor="Alice"`) || !strings.Contains(memories, `source_actor="Bob"`) {
		t.Fatalf("memories = %q, want actor attributes", memories)
	}
	if !strings.Contains(memories, "User prefers &#34;&lt;tea&gt;&#34;") {
		t.Fatalf("memories = %q, want escaped content", memories)
	}
	if embeddingProvider.lastRequest.TaskType != ai.EmbeddingTaskTypeQuery {
		t.Fatalf("embedding task type = %q, want %q", embeddingProvider.lastRequest.TaskType, ai.EmbeddingTaskTypeQuery)
	}
	if memoryService.lastSearch.Limit != 6 {
		t.Fatalf("search limit = %d, want 6", memoryService.lastSearch.Limit)
	}
	if memoryService.lastSearch.MinSimilarity != 0.4 {
		t.Fatalf("search min similarity = %f, want 0.4", memoryService.lastSearch.MinSimilarity)
	}
	if len(memoryService.updateCalls) != 2 {
		t.Fatalf("update call count = %d, want 2", len(memoryService.updateCalls))
	}
	if memoryService.updateCalls[0].Profile.AccessCount != 2 {
		t.Fatalf("first reinforced access count = %d, want 2", memoryService.updateCalls[0].Profile.AccessCount)
	}
	if !memoryService.updateCalls[0].Profile.LastAccessedAt.Equal(now) {
		t.Fatalf("first reinforced last_accessed_at = %s, want %s",
			memoryService.updateCalls[0].Profile.LastAccessedAt,
			now,
		)
	}
}

func TestRetrieveSemanticMemoriesTrimsToBudget(t *testing.T) {
	embeddingProvider := &embeddingProviderStub{
		response: ai.EmbeddingResponse{Vectors: [][]float32{{0.8, 0.2}}},
	}
	module := newTestModule(validModuleConfig())
	module.embeddingRegistry = &embeddingRegistryStub{
		providers: map[string]ai.EmbeddingProvider{"embed-main": embeddingProvider},
	}
	module.llmMemory = &llmMemoryServiceStub{
		searchResponse: []ai.LLMMemoryMatch{
			{
				Record: ai.LLMMemoryRecord{
					ID:        "mem-1",
					Content:   "Short memory",
					Category:  "preference",
					CreatedAt: time.Date(2026, time.March, 10, 12, 0, 0, 0, time.UTC),
				},
				Similarity: 0.91,
			},
			{
				Record: ai.LLMMemoryRecord{
					ID:        "mem-2",
					Content:   strings.Repeat("Very long memory ", 40),
					Category:  "knowledge",
					CreatedAt: time.Date(2026, time.March, 9, 15, 30, 0, 0, time.UTC),
				},
				Similarity: 0.72,
			},
		},
	}

	agent := module.cfg.Agents[0]
	agent.EmbeddingProvider = "embed-main"
	agent.SemanticMemory = &SemanticMemoryPolicy{
		Enabled:              true,
		MaxRetrievedMemories: 5,
		MinMemorySimilarity:  0.3,
		MaxMemoryRunes:       180,
	}

	memories, err := module.retrieveSemanticMemories(context.Background(), testLLMChatEvent("Otogi hi"), agent, "hi")
	if err != nil {
		t.Fatalf("retrieveSemanticMemories failed: %v", err)
	}
	if !strings.Contains(memories, `<semantic_memories count="1">`) {
		t.Fatalf("memories = %q, want trimmed count=1", memories)
	}
	if !strings.Contains(memories, `id="mem-1"`) {
		t.Fatalf("memories = %q, want first memory", memories)
	}
	if strings.Contains(memories, `id="mem-2"`) {
		t.Fatalf("memories = %q, did not expect second memory after trimming", memories)
	}
}

func TestRankSemanticMemoryMatchesUsesCompositeSignals(t *testing.T) {
	now := time.Date(2026, time.March, 17, 12, 0, 0, 0, time.UTC)
	testCases := []struct {
		name      string
		matches   []ai.LLMMemoryMatch
		current   platform.Actor
		related   map[string]struct{}
		wantOrder []string
	}{
		{
			name: "importance actor and synthesis outrank raw similarity",
			matches: []ai.LLMMemoryMatch{
				{
					Record: ai.LLMMemoryRecord{
						ID:        "unit-memory",
						CreatedAt: now.Add(-6 * time.Hour),
						Profile: ai.LLMMemoryProfile{
							Kind:           ai.LLMMemoryKindUnit,
							Importance:     3,
							LastAccessedAt: now.Add(-72 * time.Hour),
						},
					},
					Similarity: 0.93,
				},
				{
					Record: ai.LLMMemoryRecord{
						ID:        "synth-memory",
						CreatedAt: now.Add(-2 * time.Hour),
						Profile: ai.LLMMemoryProfile{
							Kind:           ai.LLMMemoryKindSynthesized,
							Importance:     9,
							LastAccessedAt: now.Add(-90 * time.Minute),
							SubjectActor:   &ai.LLMMemoryActorRef{ID: "user-1", Name: "Alice"},
						},
					},
					Similarity: 0.81,
				},
			},
			current:   platform.Actor{ID: "user-1", DisplayName: "Alice"},
			related:   map[string]struct{}{},
			wantOrder: []string{"synth-memory", "unit-memory"},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			ranked := rankSemanticMemoryMatches(testCase.matches, defaultNaturalMemoryDecayFactor, now, testCase.current, testCase.related)
			if len(ranked) != len(testCase.wantOrder) {
				t.Fatalf("ranked len = %d, want %d", len(ranked), len(testCase.wantOrder))
			}
			for index, wantID := range testCase.wantOrder {
				if ranked[index].Record.ID != wantID {
					t.Fatalf("ranked[%d] = %q, want %q", index, ranked[index].Record.ID, wantID)
				}
			}
		})
	}
}

func TestRetrieveSemanticMemoriesUsesPlannerQueries(t *testing.T) {
	embeddingProvider := &embeddingProviderStub{
		response: ai.EmbeddingResponse{Vectors: [][]float32{{0.8, 0.2}}},
	}
	plannerProvider := &llmProviderStub{
		stream: &llmStreamStub{
			chunks: []ai.LLMGenerateChunk{
				{Kind: ai.LLMGenerateChunkKindOutputText, Delta: `{"queries":["alice tea preference","alice study plan"]}`},
			},
		},
	}
	memoryService := &llmMemoryServiceStub{}

	module := newTestModule(validModuleConfig())
	module.embeddingRegistry = &embeddingRegistryStub{
		providers: map[string]ai.EmbeddingProvider{"embed-main": embeddingProvider},
	}
	module.providerRegistry = &llmProviderRegistryStub{
		providers: map[string]ai.LLMProvider{"planner-main": plannerProvider},
	}
	module.llmMemory = memoryService
	module.cfg.NaturalMemory = NaturalMemorySettings{
		ExtractionProvider:       "planner-main",
		ExtractionModel:          "planner-model",
		DecayFactor:              defaultNaturalMemoryDecayFactor,
		RetrievalPlanningEnabled: true,
		RetrievalPlanningTimeout: time.Second,
	}

	agent := module.cfg.Agents[0]
	agent.EmbeddingProvider = "embed-main"
	agent.SemanticMemory = &SemanticMemoryPolicy{
		Enabled:              true,
		MaxRetrievedMemories: 3,
		MinMemorySimilarity:  0.4,
		MaxMemoryRunes:       1000,
	}

	memories, err := module.retrieveSemanticMemories(context.Background(), testLLMChatEvent("Otogi what tea should I drink?"), agent, "what tea should I drink?")
	if err != nil {
		t.Fatalf("retrieveSemanticMemories failed: %v", err)
	}
	if memories != "" {
		t.Fatalf("memories = %q, want empty when planner searches find no matches", memories)
	}
	if len(memoryService.searchCalls) != 2 {
		t.Fatalf("search call count = %d, want 2", len(memoryService.searchCalls))
	}
	if plannerProvider.lastReq.Model != "planner-model" {
		t.Fatalf("planner model = %q, want planner-model", plannerProvider.lastReq.Model)
	}
	if len(plannerProvider.lastReq.Messages) != 2 {
		t.Fatalf("planner messages len = %d, want 2", len(plannerProvider.lastReq.Messages))
	}
	if !strings.Contains(plannerProvider.lastReq.Messages[1].Content, "<current_message>") {
		t.Fatalf("planner prompt = %q, want current message markup", plannerProvider.lastReq.Messages[1].Content)
	}
}

func TestBuildGenerateRequestInjectsSemanticMemoriesAfterSystemPrompts(t *testing.T) {
	module := newTestModule(validModuleConfig())
	module.memory = &memoryStub{
		replyChain: []core.ReplyChainEntry{
			{
				Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
				Actor:        platform.Actor{ID: "user-1", DisplayName: "Alice"},
				Article:      platform.Article{ID: "m-1", Text: "Otogi hi"},
				IsCurrent:    true,
			},
		},
	}
	module.embeddingRegistry = &embeddingRegistryStub{
		providers: map[string]ai.EmbeddingProvider{
			"embed-main": &embeddingProviderStub{
				response: ai.EmbeddingResponse{Vectors: [][]float32{{0.8, 0.2}}},
			},
		},
	}
	module.llmMemory = &llmMemoryServiceStub{
		searchResponse: []ai.LLMMemoryMatch{
			{
				Record: ai.LLMMemoryRecord{
					ID:        "mem-1",
					Content:   "Alice likes tea",
					Category:  "preference",
					CreatedAt: time.Date(2026, time.March, 10, 12, 0, 0, 0, time.UTC),
				},
				Similarity: 0.91,
			},
		},
	}
	module.clock = func() time.Time { return time.Unix(100, 0).UTC() }

	agent := module.cfg.Agents[0]
	agent.EmbeddingProvider = "embed-main"
	agent.SemanticMemory = &SemanticMemoryPolicy{
		Enabled:              true,
		MaxRetrievedMemories: 3,
		MinMemorySimilarity:  0.3,
		MaxMemoryRunes:       1000,
	}

	req, err := module.buildGenerateRequest(context.Background(), testLLMChatEvent("Otogi hi"), agent, "hi")
	if err != nil {
		t.Fatalf("buildGenerateRequest failed: %v", err)
	}
	if len(req.Messages) != 4 {
		t.Fatalf("messages len = %d, want 4", len(req.Messages))
	}
	if strings.Contains(req.Messages[1].Content, "remember: Store important facts") {
		t.Fatalf("context handling prompt = %q, did not expect memory tool guidance", req.Messages[1].Content)
	}
	if req.Messages[2].Role != ai.LLMMessageRoleSystem {
		t.Fatalf("message[2] role = %q, want system", req.Messages[2].Role)
	}
	if !strings.Contains(req.Messages[2].Content, "<semantic_memories") {
		t.Fatalf("message[2] = %q, want semantic memories", req.Messages[2].Content)
	}
	if req.Messages[3].Role != ai.LLMMessageRoleUser {
		t.Fatalf("message[3] role = %q, want user", req.Messages[3].Role)
	}
}

func TestRetrieveSemanticMemoriesGracefullyDegradesWithoutServices(t *testing.T) {
	module := newTestModule(validModuleConfig())
	agent := module.cfg.Agents[0]
	agent.EmbeddingProvider = "embed-main"
	agent.SemanticMemory = &SemanticMemoryPolicy{
		Enabled:              true,
		MaxRetrievedMemories: 3,
		MinMemorySimilarity:  0.3,
		MaxMemoryRunes:       1000,
	}

	memories, err := module.retrieveSemanticMemories(context.Background(), testLLMChatEvent("Otogi hi"), agent, "hi")
	if err != nil {
		t.Fatalf("retrieveSemanticMemories failed: %v", err)
	}
	if memories != "" {
		t.Fatalf("memories = %q, want empty without services", memories)
	}
}

func TestRenderSystemPromptHasSemanticMemoryTemplateVariable(t *testing.T) {
	module := newTestModule(validModuleConfig())
	module.embeddingRegistry = &embeddingRegistryStub{
		providers: map[string]ai.EmbeddingProvider{
			"embed-main": &embeddingProviderStub{},
		},
	}
	module.llmMemory = &llmMemoryServiceStub{}

	agent := module.cfg.Agents[0]
	agent.EmbeddingProvider = "embed-main"
	agent.SemanticMemory = &SemanticMemoryPolicy{
		Enabled:              true,
		MaxRetrievedMemories: 5,
		MinMemorySimilarity:  0.3,
		MaxMemoryRunes:       2000,
	}
	agent.SystemPromptTemplate = `{{if .HasSemanticMemory}}enabled{{else}}disabled{{end}}`

	rendered, err := module.renderSystemPrompt(agent, testLLMChatEvent("Otogi hi"), time.Unix(100, 0).UTC())
	if err != nil {
		t.Fatalf("renderSystemPrompt failed: %v", err)
	}
	if rendered != "enabled" {
		t.Fatalf("rendered system prompt = %q, want enabled", rendered)
	}

	module.llmMemory = nil
	rendered, err = module.renderSystemPrompt(agent, testLLMChatEvent("Otogi hi"), time.Unix(100, 0).UTC())
	if err != nil {
		t.Fatalf("renderSystemPrompt without memory failed: %v", err)
	}
	if rendered != "disabled" {
		t.Fatalf("rendered system prompt without memory = %q, want disabled", rendered)
	}
}

type embeddingRegistryStub struct {
	providers map[string]ai.EmbeddingProvider
	err       error
}

func (s *embeddingRegistryStub) Resolve(provider string) (ai.EmbeddingProvider, error) {
	if s.err != nil {
		return nil, s.err
	}
	resolved, ok := s.providers[provider]
	if !ok {
		return nil, errors.New("embedding provider not found")
	}
	return resolved, nil
}
