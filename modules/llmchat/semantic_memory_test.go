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
	if !strings.Contains(memories, `<tier role="recalled" count="2">`) {
		t.Fatalf("memories = %q, want recalled tier with count=2", memories)
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
			ranked := rankSemanticMemoryMatches(testCase.matches, defaultNaturalMemoryDecayFactor, now, testCase.current, testCase.related, nil)
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

func TestRankSemanticMemoryMatchesBoostsLinkedRecords(t *testing.T) {
	now := time.Date(2026, time.March, 17, 12, 0, 0, 0, time.UTC)

	// mem-A links to mem-B. mem-B and mem-C have nearly identical base
	// signals, but the inbound link on mem-B should boost it above mem-C.
	// mem-A has higher importance to stay on top despite no inbound links.
	matches := []ai.LLMMemoryMatch{
		{
			Record: ai.LLMMemoryRecord{
				ID:        "mem-A",
				CreatedAt: now.Add(-1 * time.Hour),
				Profile: ai.LLMMemoryProfile{
					Kind:           ai.LLMMemoryKindUnit,
					Importance:     9,
					LastAccessedAt: now.Add(-1 * time.Hour),
				},
				Links: []ai.LLMMemoryLink{
					{TargetID: "mem-B", Relation: "related"},
				},
			},
			Similarity: 0.85,
		},
		{
			Record: ai.LLMMemoryRecord{
				ID:        "mem-B",
				CreatedAt: now.Add(-1 * time.Hour),
				Profile: ai.LLMMemoryProfile{
					Kind:           ai.LLMMemoryKindUnit,
					Importance:     5,
					LastAccessedAt: now.Add(-1 * time.Hour),
				},
			},
			Similarity: 0.85,
		},
		{
			Record: ai.LLMMemoryRecord{
				ID:        "mem-C",
				CreatedAt: now.Add(-1 * time.Hour),
				Profile: ai.LLMMemoryProfile{
					Kind:           ai.LLMMemoryKindUnit,
					Importance:     5,
					LastAccessedAt: now.Add(-1 * time.Hour),
				},
			},
			Similarity: 0.85,
		},
	}

	ranked := rankSemanticMemoryMatches(matches, defaultNaturalMemoryDecayFactor, now, platform.Actor{}, nil, nil)
	if len(ranked) != 3 {
		t.Fatalf("ranked len = %d, want 3", len(ranked))
	}
	// mem-A has highest importance → first.
	if ranked[0].Record.ID != "mem-A" {
		t.Fatalf("ranked[0] = %q, want mem-A (highest importance)", ranked[0].Record.ID)
	}
	// mem-B should beat mem-C because it has an inbound link from mem-A.
	if ranked[1].Record.ID != "mem-B" {
		t.Fatalf("ranked[1] = %q, want mem-B (boosted by link from mem-A)", ranked[1].Record.ID)
	}
	if ranked[2].Record.ID != "mem-C" {
		t.Fatalf("ranked[2] = %q, want mem-C", ranked[2].Record.ID)
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

func TestRankSemanticMemoryMatchesKeywordBonus(t *testing.T) {
	now := time.Date(2026, time.March, 17, 12, 0, 0, 0, time.UTC)

	// mem-A and mem-B have identical base signals except mem-A has keywords
	// that overlap with the query terms. The keyword bonus should push mem-A
	// above mem-B.
	matches := []ai.LLMMemoryMatch{
		{
			Record: ai.LLMMemoryRecord{
				ID:        "mem-A",
				CreatedAt: now.Add(-1 * time.Hour),
				Profile: ai.LLMMemoryProfile{
					Kind:           ai.LLMMemoryKindUnit,
					Importance:     5,
					LastAccessedAt: now.Add(-1 * time.Hour),
				},
				Keywords: []string{"tea", "preference"},
			},
			Similarity: 0.85,
		},
		{
			Record: ai.LLMMemoryRecord{
				ID:        "mem-B",
				CreatedAt: now.Add(-1 * time.Hour),
				Profile: ai.LLMMemoryProfile{
					Kind:           ai.LLMMemoryKindUnit,
					Importance:     5,
					LastAccessedAt: now.Add(-1 * time.Hour),
				},
			},
			Similarity: 0.85,
		},
	}

	ranked := rankSemanticMemoryMatches(
		matches, defaultNaturalMemoryDecayFactor, now,
		platform.Actor{}, nil,
		[]string{"tea", "preference", "alice"},
	)
	if len(ranked) != 2 {
		t.Fatalf("ranked len = %d, want 2", len(ranked))
	}
	if ranked[0].Record.ID != "mem-A" {
		t.Fatalf("ranked[0] = %q, want mem-A (keyword bonus)", ranked[0].Record.ID)
	}
	if ranked[1].Record.ID != "mem-B" {
		t.Fatalf("ranked[1] = %q, want mem-B", ranked[1].Record.ID)
	}
}

func TestParseRetrievalPlanWithTimeFilter(t *testing.T) {
	testCases := []struct {
		name           string
		input          string
		wantQueries    []string
		wantTimeFilter string
		wantDepth      string
	}{
		{
			name:           "full plan with time filter and depth",
			input:          `{"queries":["alice tea","bob plans"],"time_filter":"recent","depth":"deep"}`,
			wantQueries:    []string{"alice tea", "bob plans"},
			wantTimeFilter: "recent",
			wantDepth:      "deep",
		},
		{
			name:           "last_week filter",
			input:          `{"queries":["meeting notes"],"time_filter":"last_week","depth":"few"}`,
			wantQueries:    []string{"meeting notes"},
			wantTimeFilter: "last_week",
			wantDepth:      "few",
		},
		{
			name:           "all filter normalizes to empty",
			input:          `{"queries":["general query"],"time_filter":"all","depth":"normal"}`,
			wantQueries:    []string{"general query"},
			wantTimeFilter: "",
			wantDepth:      "",
		},
		{
			name:           "missing filter fields default to empty",
			input:          `{"queries":["simple query"]}`,
			wantQueries:    []string{"simple query"},
			wantTimeFilter: "",
			wantDepth:      "",
		},
		{
			name:           "unknown filter values normalize to empty",
			input:          `{"queries":["test"],"time_filter":"yesterday","depth":"ultra"}`,
			wantQueries:    []string{"test"},
			wantTimeFilter: "",
			wantDepth:      "",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			plan, err := parseRetrievalPlanResponse(testCase.input)
			if err != nil {
				t.Fatalf("parseRetrievalPlanResponse failed: %v", err)
			}
			if len(plan.Queries) != len(testCase.wantQueries) {
				t.Fatalf("queries len = %d, want %d", len(plan.Queries), len(testCase.wantQueries))
			}
			for index, wantQuery := range testCase.wantQueries {
				if plan.Queries[index] != wantQuery {
					t.Fatalf("queries[%d] = %q, want %q", index, plan.Queries[index], wantQuery)
				}
			}
			if plan.TimeFilter != testCase.wantTimeFilter {
				t.Fatalf("time_filter = %q, want %q", plan.TimeFilter, testCase.wantTimeFilter)
			}
			if plan.Depth != testCase.wantDepth {
				t.Fatalf("depth = %q, want %q", plan.Depth, testCase.wantDepth)
			}
		})
	}
}

func TestFilterMatchesByTimeRange(t *testing.T) {
	now := time.Date(2026, time.March, 17, 12, 0, 0, 0, time.UTC)
	matches := []ai.LLMMemoryMatch{
		{Record: ai.LLMMemoryRecord{ID: "recent", CreatedAt: now.Add(-6 * time.Hour)}, Similarity: 0.9},
		{Record: ai.LLMMemoryRecord{ID: "this-week", CreatedAt: now.Add(-3 * 24 * time.Hour)}, Similarity: 0.85},
		{Record: ai.LLMMemoryRecord{ID: "old", CreatedAt: now.Add(-30 * 24 * time.Hour)}, Similarity: 0.8},
	}

	testCases := []struct {
		name    string
		filter  string
		wantIDs []string
	}{
		{
			name:    "recent filters to last 24h",
			filter:  "recent",
			wantIDs: []string{"recent"},
		},
		{
			name:    "last_week filters to last 7 days",
			filter:  "last_week",
			wantIDs: []string{"recent", "this-week"},
		},
		{
			name:    "empty filter keeps all",
			filter:  "",
			wantIDs: []string{"recent", "this-week", "old"},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			filtered := filterMatchesByTime(matches, testCase.filter, now)
			if len(filtered) != len(testCase.wantIDs) {
				t.Fatalf("filtered len = %d, want %d", len(filtered), len(testCase.wantIDs))
			}
			for index, wantID := range testCase.wantIDs {
				if filtered[index].Record.ID != wantID {
					t.Fatalf("filtered[%d] = %q, want %q", index, filtered[index].Record.ID, wantID)
				}
			}
		})
	}
}

func TestMaxSearchLimitAdjustedByDepth(t *testing.T) {
	testCases := []struct {
		name       string
		base       int
		queryCount int
		depth      string
		want       int
	}{
		{name: "normal depth single query", base: 5, queryCount: 1, depth: "", want: 10},
		{name: "few depth single query", base: 5, queryCount: 1, depth: "few", want: 5},
		{name: "deep depth single query", base: 5, queryCount: 1, depth: "deep", want: 15},
		{name: "normal depth multi query", base: 3, queryCount: 3, depth: "", want: 6},
		{name: "deep depth multi query", base: 3, queryCount: 3, depth: "deep", want: 9},
		{name: "few depth multi query", base: 3, queryCount: 3, depth: "few", want: 3},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := maxSemanticMemorySearchLimit(testCase.base, testCase.queryCount, testCase.depth)
			if got != testCase.want {
				t.Fatalf("maxSemanticMemorySearchLimit(%d, %d, %q) = %d, want %d",
					testCase.base, testCase.queryCount, testCase.depth, got, testCase.want)
			}
		})
	}
}

func TestExtractQueryTerms(t *testing.T) {
	terms := extractQueryTerms([]string{"Alice tea preference", "bob's study plan"})
	if len(terms) == 0 {
		t.Fatal("extractQueryTerms returned no terms")
	}
	found := make(map[string]bool)
	for _, term := range terms {
		found[term] = true
	}
	for _, want := range []string{"alice", "tea", "preference", "bob's", "study", "plan"} {
		if !found[want] {
			t.Errorf("missing term %q in %v", want, terms)
		}
	}
	// Short words (< 3 chars) should be excluded.
	for _, bad := range []string{"a", "of", "is"} {
		if found[bad] {
			t.Errorf("found short term %q, should be excluded", bad)
		}
	}
}

func TestRenderSemanticMemoryDocumentTiered(t *testing.T) {
	matches := []ai.LLMMemoryMatch{
		{
			Record: ai.LLMMemoryRecord{
				ID:       "mem-reflection",
				Content:  "User values routine",
				Category: "reflection",
				Profile:  ai.LLMMemoryProfile{Kind: ai.LLMMemoryKindSynthesized, Importance: 8},
			},
			Similarity: 0.9,
		},
		{
			Record: ai.LLMMemoryRecord{
				ID:       "mem-theme",
				Content:  "Recurring interest in cooking",
				Category: "theme",
				Profile:  ai.LLMMemoryProfile{Kind: ai.LLMMemoryKindSynthesized, Importance: 7},
			},
			Similarity: 0.85,
		},
		{
			Record: ai.LLMMemoryRecord{
				ID:       "mem-unit",
				Content:  "User said they like tea",
				Category: "preference",
				Profile:  ai.LLMMemoryProfile{Kind: ai.LLMMemoryKindUnit, Importance: 5},
			},
			Similarity: 0.82,
		},
	}

	doc := renderSemanticMemoryDocument(matches)
	if !strings.Contains(doc, `<semantic_memories count="3">`) {
		t.Fatalf("doc = %q, want count=3", doc)
	}
	if !strings.Contains(doc, `<tier role="background" count="2">`) {
		t.Fatalf("doc = %q, want background tier with count=2", doc)
	}
	if !strings.Contains(doc, `<tier role="recalled" count="1">`) {
		t.Fatalf("doc = %q, want recalled tier with count=1", doc)
	}
	// Background tier should contain both reflection and theme.
	bgStart := strings.Index(doc, `<tier role="background"`)
	bgEnd := strings.Index(doc, "</tier>")
	if bgStart < 0 || bgEnd < 0 {
		t.Fatalf("doc = %q, missing background tier boundaries", doc)
	}
	bgSection := doc[bgStart:bgEnd]
	if !strings.Contains(bgSection, `id="mem-reflection"`) {
		t.Fatalf("background section = %q, want mem-reflection", bgSection)
	}
	if !strings.Contains(bgSection, `id="mem-theme"`) {
		t.Fatalf("background section = %q, want mem-theme", bgSection)
	}

	// Recalled tier should contain the unit memory.
	rcStart := strings.Index(doc, `<tier role="recalled"`)
	if rcStart < 0 {
		t.Fatalf("doc = %q, missing recalled tier", doc)
	}
	rcSection := doc[rcStart:]
	if !strings.Contains(rcSection, `id="mem-unit"`) {
		t.Fatalf("recalled section = %q, want mem-unit", rcSection)
	}
}

func TestRenderSemanticMemoryDocumentSingleTier(t *testing.T) {
	matches := []ai.LLMMemoryMatch{
		{
			Record: ai.LLMMemoryRecord{
				ID:       "mem-1",
				Content:  "Alice likes tea",
				Category: "preference",
				Profile:  ai.LLMMemoryProfile{Kind: ai.LLMMemoryKindUnit, Importance: 5},
			},
			Similarity: 0.9,
		},
		{
			Record: ai.LLMMemoryRecord{
				ID:       "mem-2",
				Content:  "Bob studies CS",
				Category: "knowledge",
				Profile:  ai.LLMMemoryProfile{Kind: ai.LLMMemoryKindUnit, Importance: 6},
			},
			Similarity: 0.85,
		},
	}

	doc := renderSemanticMemoryDocument(matches)
	if !strings.Contains(doc, `<semantic_memories count="2">`) {
		t.Fatalf("doc = %q, want count=2", doc)
	}
	if strings.Contains(doc, `role="background"`) {
		t.Fatalf("doc = %q, did not expect background tier when no background memories", doc)
	}
	if !strings.Contains(doc, `<tier role="recalled" count="2">`) {
		t.Fatalf("doc = %q, want recalled tier with count=2", doc)
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
