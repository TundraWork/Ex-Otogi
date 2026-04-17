package memory

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"ex-otogi/pkg/otogi/ai"
)

// retryingLLMProviderStub adapts llmProviderStub so it can announce retry
// classifications to retryLLMOperation via the LLMRetryDelayProvider
// interface.
type retryingLLMProviderStub struct {
	*llmProviderStub
	retryDelay func(err error) (time.Duration, bool)
}

func (s *retryingLLMProviderStub) RetryDelay(err error) (time.Duration, bool) {
	if s.retryDelay == nil {
		return 0, false
	}
	return s.retryDelay(err)
}

func retrieverTestConfig() Config {
	cfg := defaultConfig()
	cfg.Enabled = true
	cfg.ExtractionProvider = "planner-main"
	cfg.ExtractionModel = "planner-model"
	cfg.EmbeddingProvider = "embed-main"
	cfg.RetrievalPlanningEnabled = false
	cfg.RetrievalPlanningTimeout = time.Second
	return cfg
}

func retrieverTestRequest(prompt string) ai.SemanticRetrievalRequest {
	return ai.SemanticRetrievalRequest{
		Scope: ai.SemanticScope{
			TenantID:       "tenant-1",
			Platform:       "telegram",
			ConversationID: "chat-1",
		},
		Prompt:            prompt,
		EmbeddingProvider: "embed-main",
		Policy: ai.SemanticRetrievalPolicy{
			MaxRetrievedMemories: 3,
			MinSimilarity:        0.4,
			MaxMemoryRunes:       1000,
		},
	}
}

func TestRetrieveSerializesMatches(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 17, 12, 0, 0, 0, time.UTC)
	embeddingProvider := &embeddingProviderStub{
		response: ai.EmbeddingResponse{Vectors: [][]float32{{0.8, 0.2}}},
	}
	store := &recordingSemanticStore{
		searchResp: []ai.SemanticMatch{
			{
				Record: ai.SemanticRecord{
					ID:       "mem-1",
					Content:  `User prefers "<tea>"`,
					Category: "preference",
					Profile: ai.SemanticProfile{
						Kind:           ai.SemanticKindUnit,
						Importance:     7,
						AccessCount:    1,
						LastAccessedAt: now.Add(-2 * time.Hour),
						SubjectActor:   &ai.SemanticActorRef{ID: "user-1", Name: "Alice"},
					},
					CreatedAt: time.Date(2026, time.March, 10, 12, 0, 0, 0, time.UTC),
				},
				Similarity: 0.91,
			},
			{
				Record: ai.SemanticRecord{
					ID:       "mem-2",
					Content:  "User studies computer science",
					Category: "knowledge",
					Profile: ai.SemanticProfile{
						Kind:           ai.SemanticKindSynthesized,
						Importance:     6,
						AccessCount:    3,
						LastAccessedAt: now.Add(-24 * time.Hour),
						SourceActor:    &ai.SemanticActorRef{ID: "user-2", Name: "Bob"},
					},
					CreatedAt: time.Date(2026, time.March, 9, 15, 30, 0, 0, time.UTC),
				},
				Similarity: 0.72,
			},
		},
	}
	module := New(withClock(func() time.Time { return now }), withConfig(retrieverTestConfig()))
	module.semanticStore = store
	module.embeddingRegistry = &embeddingRegistryStub{
		providers: map[string]ai.EmbeddingProvider{"embed-main": embeddingProvider},
	}

	req := retrieverTestRequest("hi")
	req.CurrentActor = ai.SemanticActorRef{ID: "user-1", Name: "Alice"}

	result, err := module.Retrieve(context.Background(), req)
	if err != nil {
		t.Fatalf("Retrieve failed: %v", err)
	}
	if result.MatchCount != 2 {
		t.Fatalf("MatchCount = %d, want 2", result.MatchCount)
	}
	if !strings.Contains(result.Content, `<semantic_memories count="2">`) {
		t.Fatalf("content = %q, want count=2 wrapper", result.Content)
	}
	if !strings.Contains(result.Content, `id="mem-1"`) || !strings.Contains(result.Content, `id="mem-2"`) {
		t.Fatalf("content = %q, want both memory ids", result.Content)
	}
	if !strings.Contains(result.Content, `importance="7"`) || !strings.Contains(result.Content, `importance="6"`) {
		t.Fatalf("content = %q, want importance attributes", result.Content)
	}
	if !strings.Contains(result.Content, `subject_actor="Alice"`) || !strings.Contains(result.Content, `source_actor="Bob"`) {
		t.Fatalf("content = %q, want actor attributes", result.Content)
	}
	if !strings.Contains(result.Content, "User prefers &#34;&lt;tea&gt;&#34;") {
		t.Fatalf("content = %q, want escaped content", result.Content)
	}
	if embeddingProvider.lastRequest.TaskType != ai.EmbeddingTaskTypeQuery {
		t.Fatalf("embedding task type = %q, want %q", embeddingProvider.lastRequest.TaskType, ai.EmbeddingTaskTypeQuery)
	}
	if store.lastSearch.Limit != 6 {
		t.Fatalf("search limit = %d, want 6 (base 3 × normal depth 2)", store.lastSearch.Limit)
	}
	if store.lastSearch.MinSimilarity != 0.4 {
		t.Fatalf("search min similarity = %f, want 0.4", store.lastSearch.MinSimilarity)
	}
	if len(store.updates) != 2 {
		t.Fatalf("reinforce update count = %d, want 2", len(store.updates))
	}
	if store.updates[0].Profile.AccessCount != 2 {
		t.Fatalf("first reinforced access count = %d, want 2", store.updates[0].Profile.AccessCount)
	}
	if !store.updates[0].Profile.LastAccessedAt.Equal(now) {
		t.Fatalf("first reinforced last_accessed_at = %s, want %s",
			store.updates[0].Profile.LastAccessedAt, now)
	}
}

func TestRetrieveTrimsToBudget(t *testing.T) {
	t.Parallel()

	embeddingProvider := &embeddingProviderStub{
		response: ai.EmbeddingResponse{Vectors: [][]float32{{0.8, 0.2}}},
	}
	store := &recordingSemanticStore{
		searchResp: []ai.SemanticMatch{
			{
				Record: ai.SemanticRecord{
					ID:        "mem-1",
					Content:   "Short memory",
					Category:  "preference",
					CreatedAt: time.Date(2026, time.March, 10, 12, 0, 0, 0, time.UTC),
				},
				Similarity: 0.91,
			},
			{
				Record: ai.SemanticRecord{
					ID:        "mem-2",
					Content:   strings.Repeat("Very long memory ", 40),
					Category:  "knowledge",
					CreatedAt: time.Date(2026, time.March, 9, 15, 30, 0, 0, time.UTC),
				},
				Similarity: 0.72,
			},
		},
	}
	module := New(withConfig(retrieverTestConfig()))
	module.semanticStore = store
	module.embeddingRegistry = &embeddingRegistryStub{
		providers: map[string]ai.EmbeddingProvider{"embed-main": embeddingProvider},
	}

	req := retrieverTestRequest("hi")
	req.Policy.MaxRetrievedMemories = 5
	req.Policy.MinSimilarity = 0.3
	req.Policy.MaxMemoryRunes = 180

	result, err := module.Retrieve(context.Background(), req)
	if err != nil {
		t.Fatalf("Retrieve failed: %v", err)
	}
	if !strings.Contains(result.Content, `<semantic_memories count="1">`) {
		t.Fatalf("content = %q, want trimmed count=1", result.Content)
	}
	if !strings.Contains(result.Content, `id="mem-1"`) {
		t.Fatalf("content = %q, want first memory", result.Content)
	}
	if strings.Contains(result.Content, `id="mem-2"`) {
		t.Fatalf("content = %q, did not expect second memory after trimming", result.Content)
	}
}

func TestRetrieveGracefullyDegradesWithoutServices(t *testing.T) {
	t.Parallel()

	module := New(withConfig(retrieverTestConfig()))
	// semanticStore and embeddingRegistry left nil.

	result, err := module.Retrieve(context.Background(), retrieverTestRequest("hi"))
	if err != nil {
		t.Fatalf("Retrieve failed: %v", err)
	}
	if result.Content != "" || result.MatchCount != 0 {
		t.Fatalf("result = %+v, want empty without services", result)
	}
}

func TestRetrieveDisabledModuleReturnsEmpty(t *testing.T) {
	t.Parallel()

	cfg := retrieverTestConfig()
	cfg.Enabled = false
	module := New(withConfig(cfg))
	module.semanticStore = &recordingSemanticStore{}

	result, err := module.Retrieve(context.Background(), retrieverTestRequest("hi"))
	if err != nil {
		t.Fatalf("Retrieve failed: %v", err)
	}
	if result.Content != "" || result.MatchCount != 0 {
		t.Fatalf("result = %+v, want empty when module disabled", result)
	}
}

func TestRetrieveErrorsWhenEmbeddingProviderUnresolvable(t *testing.T) {
	t.Parallel()

	module := New(withConfig(retrieverTestConfig()))
	module.semanticStore = &recordingSemanticStore{}
	module.embeddingRegistry = &embeddingRegistryStub{
		providers: map[string]ai.EmbeddingProvider{},
	}

	_, err := module.Retrieve(context.Background(), retrieverTestRequest("hi"))
	if err == nil {
		t.Fatal("Retrieve succeeded, want error for unresolved embedding provider")
	}
	if !strings.Contains(err.Error(), "resolve embedding provider") {
		t.Fatalf("error = %v, want resolve-embedding-provider message", err)
	}
}

func TestRetrieveUsesPlannerQueries(t *testing.T) {
	t.Parallel()

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
	store := &recordingSemanticStore{}

	cfg := retrieverTestConfig()
	cfg.RetrievalPlanningEnabled = true
	cfg.DecayFactor = 0.99
	module := New(withConfig(cfg))
	module.semanticStore = store
	module.embeddingRegistry = &embeddingRegistryStub{
		providers: map[string]ai.EmbeddingProvider{"embed-main": embeddingProvider},
	}
	module.providerRegistry = &llmProviderRegistryStub{
		providers: map[string]ai.LLMProvider{"planner-main": plannerProvider},
	}

	result, err := module.Retrieve(context.Background(), retrieverTestRequest("what tea should I drink?"))
	if err != nil {
		t.Fatalf("Retrieve failed: %v", err)
	}
	if result.Content != "" {
		t.Fatalf("content = %q, want empty when planner searches find no matches", result.Content)
	}
	if len(store.searchCalls) != 2 {
		t.Fatalf("search call count = %d, want 2", len(store.searchCalls))
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

func TestRetrieveRetriesTransientPlannerError(t *testing.T) {
	t.Parallel()

	embeddingProvider := &embeddingProviderStub{
		response: ai.EmbeddingResponse{Vectors: [][]float32{{0.8, 0.2}}},
	}
	plannerBase := &llmProviderStub{
		streams: []ai.LLMStream{
			&llmStreamStub{recvErr: errors.New("transient planner failure")},
			&llmStreamStub{
				chunks: []ai.LLMGenerateChunk{
					{Kind: ai.LLMGenerateChunkKindOutputText, Delta: `{"queries":["alice tea preference"]}`},
				},
			},
		},
	}
	plannerProvider := &retryingLLMProviderStub{
		llmProviderStub: plannerBase,
		retryDelay: func(err error) (time.Duration, bool) {
			if !strings.Contains(err.Error(), "transient planner failure") {
				return 0, false
			}
			return llmRetryBaseInterval, true
		},
	}
	store := &recordingSemanticStore{}

	cfg := retrieverTestConfig()
	cfg.RetrievalPlanningEnabled = true
	cfg.DecayFactor = 0.99
	module := New(withConfig(cfg))
	module.semanticStore = store
	module.embeddingRegistry = &embeddingRegistryStub{
		providers: map[string]ai.EmbeddingProvider{"embed-main": embeddingProvider},
	}
	module.providerRegistry = &llmProviderRegistryStub{
		providers: map[string]ai.LLMProvider{"planner-main": plannerProvider},
	}
	sleeps := make([]time.Duration, 0, 1)
	module.sleep = func(_ context.Context, delay time.Duration) error {
		sleeps = append(sleeps, delay)
		return nil
	}

	result, err := module.Retrieve(context.Background(), retrieverTestRequest("what tea should I drink?"))
	if err != nil {
		t.Fatalf("Retrieve failed: %v", err)
	}
	if result.Content != "" {
		t.Fatalf("content = %q, want empty when planner searches find no matches", result.Content)
	}
	if len(plannerBase.requests) != 2 {
		t.Fatalf("planner request count = %d, want 2", len(plannerBase.requests))
	}
	if len(store.searchCalls) != 1 {
		t.Fatalf("search call count = %d, want 1", len(store.searchCalls))
	}
	if len(sleeps) != 1 {
		t.Fatalf("sleep count = %d, want 1", len(sleeps))
	}
	if sleeps[0] != llmRetryBaseInterval {
		t.Fatalf("retry sleep = %s, want %s", sleeps[0], llmRetryBaseInterval)
	}
}

func TestRankSemanticMemoryMatchesUsesCompositeSignals(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 17, 12, 0, 0, 0, time.UTC)
	matches := []ai.SemanticMatch{
		{
			Record: ai.SemanticRecord{
				ID:        "unit-memory",
				CreatedAt: now.Add(-6 * time.Hour),
				Profile: ai.SemanticProfile{
					Kind:           ai.SemanticKindUnit,
					Importance:     3,
					LastAccessedAt: now.Add(-72 * time.Hour),
				},
			},
			Similarity: 0.93,
		},
		{
			Record: ai.SemanticRecord{
				ID:        "synth-memory",
				CreatedAt: now.Add(-2 * time.Hour),
				Profile: ai.SemanticProfile{
					Kind:           ai.SemanticKindSynthesized,
					Importance:     9,
					LastAccessedAt: now.Add(-90 * time.Minute),
					SubjectActor:   &ai.SemanticActorRef{ID: "user-1", Name: "Alice"},
				},
			},
			Similarity: 0.81,
		},
	}

	ranked := rankSemanticMemoryMatches(
		matches, 0.99, now,
		ai.SemanticActorRef{ID: "user-1", Name: "Alice"},
		map[string]struct{}{}, nil,
	)
	if len(ranked) != 2 {
		t.Fatalf("ranked len = %d, want 2", len(ranked))
	}
	if ranked[0].Record.ID != "synth-memory" {
		t.Fatalf("ranked[0] = %q, want synth-memory (composite signals outrank similarity)", ranked[0].Record.ID)
	}
	if ranked[1].Record.ID != "unit-memory" {
		t.Fatalf("ranked[1] = %q, want unit-memory", ranked[1].Record.ID)
	}
}

func TestRankSemanticMemoryMatchesKeywordBonus(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 17, 12, 0, 0, 0, time.UTC)
	// mem-A and mem-B have identical base signals except mem-A has keywords
	// overlapping with query terms, so the keyword bonus should rank it first.
	matches := []ai.SemanticMatch{
		{
			Record: ai.SemanticRecord{
				ID:        "mem-A",
				CreatedAt: now.Add(-1 * time.Hour),
				Profile: ai.SemanticProfile{
					Kind:           ai.SemanticKindUnit,
					Importance:     5,
					LastAccessedAt: now.Add(-1 * time.Hour),
				},
				Keywords: []string{"tea", "preference"},
			},
			Similarity: 0.85,
		},
		{
			Record: ai.SemanticRecord{
				ID:        "mem-B",
				CreatedAt: now.Add(-1 * time.Hour),
				Profile: ai.SemanticProfile{
					Kind:           ai.SemanticKindUnit,
					Importance:     5,
					LastAccessedAt: now.Add(-1 * time.Hour),
				},
			},
			Similarity: 0.85,
		},
	}

	ranked := rankSemanticMemoryMatches(
		matches, 0.99, now,
		ai.SemanticActorRef{}, nil,
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

func TestParseRetrievalPlanResponse(t *testing.T) {
	t.Parallel()

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
			t.Parallel()

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

func TestFilterMatchesByTime(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 17, 12, 0, 0, 0, time.UTC)
	matches := []ai.SemanticMatch{
		{Record: ai.SemanticRecord{ID: "recent", CreatedAt: now.Add(-6 * time.Hour)}, Similarity: 0.9},
		{Record: ai.SemanticRecord{ID: "this-week", CreatedAt: now.Add(-3 * 24 * time.Hour)}, Similarity: 0.85},
		{Record: ai.SemanticRecord{ID: "old", CreatedAt: now.Add(-30 * 24 * time.Hour)}, Similarity: 0.8},
	}

	testCases := []struct {
		name    string
		filter  string
		wantIDs []string
	}{
		{name: "recent filters to last 24h", filter: "recent", wantIDs: []string{"recent"}},
		{name: "last_week filters to last 7 days", filter: "last_week", wantIDs: []string{"recent", "this-week"}},
		{name: "empty filter keeps all", filter: "", wantIDs: []string{"recent", "this-week", "old"}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

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

func TestMaxSemanticMemorySearchLimitByDepth(t *testing.T) {
	t.Parallel()

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
			t.Parallel()

			got := maxSemanticMemorySearchLimit(testCase.base, testCase.queryCount, testCase.depth)
			if got != testCase.want {
				t.Fatalf("maxSemanticMemorySearchLimit(%d, %d, %q) = %d, want %d",
					testCase.base, testCase.queryCount, testCase.depth, got, testCase.want)
			}
		})
	}
}

func TestExtractQueryTerms(t *testing.T) {
	t.Parallel()

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
	// Short words (< 3 chars) must be excluded.
	for _, bad := range []string{"a", "of", "is"} {
		if found[bad] {
			t.Errorf("found short term %q, should be excluded", bad)
		}
	}
}

func TestRenderSemanticMemoryDocument(t *testing.T) {
	t.Parallel()

	matches := []ai.SemanticMatch{
		{
			Record: ai.SemanticRecord{
				ID:       "mem-1",
				Content:  "Alice likes tea",
				Category: "preference",
				Profile:  ai.SemanticProfile{Kind: ai.SemanticKindUnit, Importance: 5},
			},
			Similarity: 0.9,
		},
		{
			Record: ai.SemanticRecord{
				ID:       "mem-2",
				Content:  "Bob studies CS",
				Category: "knowledge",
				Profile:  ai.SemanticProfile{Kind: ai.SemanticKindSynthesized, Importance: 7},
			},
			Similarity: 0.85,
		},
	}

	doc := renderSemanticMemoryDocument(matches)
	if !strings.Contains(doc, `<semantic_memories count="2">`) {
		t.Fatalf("doc = %q, want count=2", doc)
	}
	if strings.Contains(doc, "<tier") {
		t.Fatalf("doc = %q, want flat list without tiers", doc)
	}
	if !strings.Contains(doc, `id="mem-1"`) || !strings.Contains(doc, `id="mem-2"`) {
		t.Fatalf("doc = %q, want both memory ids", doc)
	}
	if !strings.Contains(doc, `kind="unit"`) || !strings.Contains(doc, `kind="synthesized"`) {
		t.Fatalf("doc = %q, want kind attributes", doc)
	}
}

func TestPlanSemanticMemoryQueriesRequiresPlannerConfig(t *testing.T) {
	t.Parallel()

	cfg := retrieverTestConfig()
	cfg.ExtractionProvider = ""
	module := New(withConfig(cfg))
	module.providerRegistry = &llmProviderRegistryStub{}
	module.logger = slog.Default()

	_, err := module.planSemanticMemoryQueries(context.Background(), "prompt", "")
	if err == nil {
		t.Fatal("planSemanticMemoryQueries succeeded without planner configured, want error")
	}
	if !strings.Contains(err.Error(), "semantic memory planner is not configured") {
		t.Fatalf("error = %v, want semantic-memory-planner message", err)
	}
}
