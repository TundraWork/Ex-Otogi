package naturalmemory

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"ex-otogi/pkg/otogi/ai"
	"ex-otogi/pkg/otogi/core"
	"ex-otogi/pkg/otogi/platform"
)

func TestOnRegisterLoadsConfigAndSubscribes(t *testing.T) {
	t.Parallel()

	llmConfigPath := writeNaturalMemoryLLMConfigFile(t)
	services := newRecordingServiceRegistry(map[string]any{
		serviceLogger:       slog.Default(),
		ai.ServiceLLMMemory: &recordingLLMMemoryService{},
		core.ServiceMemory:  &memoryContextStub{},
		ai.ServiceLLMProviderRegistry: &llmProviderRegistryStub{providers: map[string]ai.LLMProvider{
			"openai-main":          &llmProviderStub{stream: &llmStreamStub{}},
			"openai-consolidation": &llmProviderStub{stream: &llmStreamStub{}},
		}},
		ai.ServiceEmbeddingProviderRegistry: &embeddingRegistryStub{providers: map[string]ai.EmbeddingProvider{
			"openai-main": &embeddingProviderStub{response: ai.EmbeddingResponse{Vectors: [][]float32{{1, 0}}}},
		}},
	})

	var (
		gotInterest core.InterestSet
		gotSpec     core.SubscriptionSpec
	)
	runtime := registrationRuntimeStub{
		registry: services,
		configs:  testNaturalMemoryConfigRegistry(t, llmConfigPath),
		subscribe: func(
			_ context.Context,
			interest core.InterestSet,
			spec core.SubscriptionSpec,
			handler core.EventHandler,
		) (core.Subscription, error) {
			if handler == nil {
				t.Fatal("expected handler")
			}
			gotInterest = interest
			gotSpec = spec
			return registrationSubscriptionStub{name: spec.Name}, nil
		},
	}

	module := New()
	if err := module.OnRegister(context.Background(), runtime); err != nil {
		t.Fatalf("OnRegister failed: %v", err)
	}

	if !module.cfg.Enabled {
		t.Fatal("cfg.Enabled = false, want true")
	}
	if module.extractionProvider == nil {
		t.Fatal("expected extraction provider")
	}
	if module.embeddingProvider == nil {
		t.Fatal("expected embedding provider")
	}
	if gotSpec.Name != "naturalmemory-articles" {
		t.Fatalf("subscription name = %q, want naturalmemory-articles", gotSpec.Name)
	}
	if gotSpec.Buffer != 64 || gotSpec.Workers != 1 {
		t.Fatalf("subscription spec = %+v, want buffer=64 workers=1", gotSpec)
	}
	if gotSpec.Backpressure != core.BackpressureDropOldest {
		t.Fatalf("backpressure = %q, want %q", gotSpec.Backpressure, core.BackpressureDropOldest)
	}
	if gotSpec.HandlerTimeout != 35*time.Second {
		t.Fatalf("handler timeout = %s, want 35s", gotSpec.HandlerTimeout)
	}
	if len(gotInterest.Kinds) != 1 || gotInterest.Kinds[0] != platform.EventKindArticleCreated {
		t.Fatalf("interest kinds = %v, want article.created", gotInterest.Kinds)
	}
	if !gotInterest.RequireArticle {
		t.Fatal("RequireArticle = false, want true")
	}
}

func TestOnRegisterWithoutConfigLeavesModuleDisabled(t *testing.T) {
	t.Parallel()

	services := newRecordingServiceRegistry(map[string]any{
		serviceLogger: slog.Default(),
	})
	subscribed := false
	runtime := registrationRuntimeStub{
		registry: services,
		configs:  newConfigRegistryStub(),
		subscribe: func(context.Context, core.InterestSet, core.SubscriptionSpec, core.EventHandler) (core.Subscription, error) {
			subscribed = true
			return registrationSubscriptionStub{name: "unused"}, nil
		},
	}

	module := New()
	if err := module.OnRegister(context.Background(), runtime); err != nil {
		t.Fatalf("OnRegister failed: %v", err)
	}
	if module.cfg.Enabled {
		t.Fatal("cfg.Enabled = true, want false")
	}
	if subscribed {
		t.Fatal("did not expect subscription when module is disabled")
	}
}

func TestHandleArticleExtractsAndStoresMemory(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 16, 12, 0, 0, 0, time.UTC)
	memoryStore := &recordingLLMMemoryService{}
	extractionProvider := &llmProviderStub{
		stream: &llmStreamStub{chunks: []ai.LLMGenerateChunk{
			{Kind: ai.LLMGenerateChunkKindOutputText, Delta: `[{"content":"Alice likes tea","category":"preference","importance":8}]`},
		}},
	}
	embeddingProvider := &embeddingProviderStub{
		response: ai.EmbeddingResponse{Vectors: [][]float32{{1, 0}}},
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
	}))
	module.memory = &memoryContextStub{
		leadingContext: []core.ConversationContextEntry{
			{
				Actor:   platform.Actor{ID: "user-1", DisplayName: "Alice"},
				Article: platform.Article{ID: "a-1", Text: "I love tea."},
			},
		},
	}
	module.llmMemory = memoryStore
	module.extractionProvider = extractionProvider
	module.embeddingProvider = embeddingProvider

	event := &platform.Event{
		Kind:       platform.EventKindArticleCreated,
		OccurredAt: now,
		Source:     platform.EventSource{Platform: platform.PlatformTelegram, ID: "tg-main"},
		Conversation: platform.Conversation{
			ID:   "chat-1",
			Type: platform.ConversationTypeGroup,
		},
		Actor: platform.Actor{ID: "user-1", DisplayName: "Alice"},
		Article: &platform.Article{
			ID:   "a-2",
			Text: "My favorite is jasmine tea.",
		},
	}

	if err := module.handleArticle(context.Background(), event); err != nil {
		t.Fatalf("handleArticle failed: %v", err)
	}

	if len(memoryStore.storedEntries) != 1 {
		t.Fatalf("stored entries = %d, want 1", len(memoryStore.storedEntries))
	}
	entry := memoryStore.storedEntries[0]
	if entry.Content != "Alice likes tea" {
		t.Fatalf("stored content = %q, want Alice likes tea", entry.Content)
	}
	if entry.Category != "preference" {
		t.Fatalf("stored category = %q, want preference", entry.Category)
	}
	if entry.Profile.Kind != ai.LLMMemoryKindUnit {
		t.Fatalf("profile.kind = %q, want %q", entry.Profile.Kind, ai.LLMMemoryKindUnit)
	}
	if entry.Profile.Source != "natural" {
		t.Fatalf("profile.source = %q, want natural", entry.Profile.Source)
	}
	if entry.Profile.SourceArticleID != "a-2" {
		t.Fatalf("profile.source_article_id = %q, want a-2", entry.Profile.SourceArticleID)
	}
	if entry.Profile.SourceActor == nil || entry.Profile.SourceActor.Name != "Alice" {
		t.Fatalf("profile.source_actor = %+v, want Alice", entry.Profile.SourceActor)
	}
	if entry.Profile.SubjectActor == nil || entry.Profile.SubjectActor.Name != "Alice" {
		t.Fatalf("profile.subject_actor = %+v, want Alice", entry.Profile.SubjectActor)
	}
	if entry.Scope.ConversationID != "chat-1" || entry.Scope.Platform != "telegram" {
		t.Fatalf("stored scope = %+v, want chat-1/telegram", entry.Scope)
	}
	if extractionProvider.lastReq.Model != "gpt-4.1-mini" {
		t.Fatalf("extraction model = %q, want gpt-4.1-mini", extractionProvider.lastReq.Model)
	}
	if len(extractionProvider.lastReq.Messages) != 2 {
		t.Fatalf("message count = %d, want 2", len(extractionProvider.lastReq.Messages))
	}
	if embeddingProvider.lastRequest.TaskType != ai.EmbeddingTaskTypeDocument {
		t.Fatalf("embedding task type = %q, want document", embeddingProvider.lastRequest.TaskType)
	}
}

func TestOnStartAndShutdownManageConsolidationLifecycle(t *testing.T) {
	t.Parallel()

	module := New(withConfig(Config{
		Enabled:                      true,
		ExtractionTimeout:            time.Second,
		ExtractionMaxInputRunes:      1000,
		ConsolidationInterval:        10 * time.Millisecond,
		ConsolidationTimeout:         time.Second,
		MaxMemoriesPerScope:          10,
		DecayFactor:                  0.99,
		MinImportance:                1,
		DuplicateSimilarityThreshold: 0.85,
		ContextWindowSize:            5,
	}))
	module.llmMemory = &recordingLLMMemoryService{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := module.OnStart(ctx); err != nil {
		t.Fatalf("OnStart failed: %v", err)
	}
	if module.stopCh == nil || module.flushDone == nil {
		t.Fatal("expected consolidation channels to be initialized")
	}
	if err := module.OnShutdown(context.Background()); err != nil {
		t.Fatalf("OnShutdown failed: %v", err)
	}
	if module.stopCh != nil || module.flushDone != nil {
		t.Fatal("expected consolidation channels to be cleared")
	}
}

func TestConfigRegistryMarshalRoundTrip(t *testing.T) {
	t.Parallel()

	raw, err := json.Marshal(fileModuleConfig{ConfigFile: "config/llm.json"})
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	var cfg fileModuleConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if cfg.ConfigFile != "config/llm.json" {
		t.Fatalf("config_file = %q, want config/llm.json", cfg.ConfigFile)
	}
}
