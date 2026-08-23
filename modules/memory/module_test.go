package memory

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"ex-otogi/pkg/llm"
	llmconfig "ex-otogi/pkg/llm/config"
	"ex-otogi/pkg/otogi/ai"
	"ex-otogi/pkg/otogi/core"
	"ex-otogi/pkg/otogi/platform"
)

func TestOnRegisterLoadsConfigAndSubscribes(t *testing.T) {
	t.Parallel()

	llmConfigPath := writeMemoryLLMConfigFile(t)
	llmCfg, err := llmconfig.LoadFile(llmConfigPath)
	if err != nil {
		t.Fatalf("load LLM config: %v", err)
	}
	services := newRecordingServiceRegistry(map[string]any{
		serviceLogger:            slog.Default(),
		llm.ServiceRuntimeConfig: llmCfg,
		ai.ServiceSemanticStore:  &recordingSemanticStore{},
		core.ServiceMemory:       &memoryContextStub{},
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
		configs:  newConfigRegistryStub(),
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
	if gotSpec.Name != "memory-articles" {
		t.Fatalf("subscription name = %q, want memory-articles", gotSpec.Name)
	}
	if gotSpec.Buffer != 64 || gotSpec.Workers != 1 {
		t.Fatalf("subscription spec = %+v, want buffer=64 workers=1", gotSpec)
	}
	if gotSpec.Backpressure != core.BackpressureDropOldest {
		t.Fatalf("backpressure = %q, want %q", gotSpec.Backpressure, core.BackpressureDropOldest)
	}
	if gotSpec.HandlerTimeout != enqueueHandlerTimout {
		t.Fatalf("handler timeout = %s, want %s", gotSpec.HandlerTimeout, enqueueHandlerTimout)
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
		serviceLogger:            slog.Default(),
		llm.ServiceRuntimeConfig: llmconfig.Config{},
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

func TestHandleArticleEnqueuesIntoWindowManager(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 16, 12, 0, 0, 0, time.UTC)
	cfg := Config{
		Enabled:                  true,
		ExtractionProvider:       "openai-main",
		ExtractionModel:          "gpt-4.1-mini",
		EmbeddingProvider:        "openai-main",
		ExtractionTimeout:        time.Second,
		ExtractionMaxInputRunes:  4000,
		ConsolidationInterval:    0,
		BufferQuietPeriod:        2 * time.Minute,
		BufferMaxRunes:           3000,
		BufferMaxArticles:        30,
		BufferMaxAge:             10 * time.Minute,
		BufferCheckInterval:      15 * time.Second,
		RetrievalSearchLimit:     20,
		RetrievalPlanningEnabled: true,
		RetrievalPlanningTimeout: 10 * time.Second,
	}
	module := New(withClock(func() time.Time { return now }), withConfig(cfg))
	module.windowManager = newWindowManager(cfg, func() time.Time { return now })
	module.memory = &memoryContextStub{}
	module.semanticStore = &recordingSemanticStore{}

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

	// Verify article was enqueued into the window manager.
	scopes := module.windowManager.ActiveScopes()
	if len(scopes) != 1 {
		t.Fatalf("active scopes = %d, want 1", len(scopes))
	}
	if scopes[0].ConversationID != "chat-1" {
		t.Fatalf("scope conversation = %q, want chat-1", scopes[0].ConversationID)
	}
	if scopes[0].Platform != "telegram" {
		t.Fatalf("scope platform = %q, want telegram", scopes[0].Platform)
	}
}

func TestOnStartAndShutdownManageConsolidationLifecycle(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Enabled:                  true,
		ExtractionTimeout:        time.Second,
		ExtractionMaxInputRunes:  4000,
		ConsolidationInterval:    10 * time.Millisecond,
		BufferQuietPeriod:        2 * time.Minute,
		BufferMaxRunes:           3000,
		BufferMaxArticles:        30,
		BufferMaxAge:             10 * time.Minute,
		BufferCheckInterval:      15 * time.Second,
		RetrievalSearchLimit:     20,
		RetrievalPlanningEnabled: true,
		RetrievalPlanningTimeout: 10 * time.Second,
	}
	module := New(withConfig(cfg))
	module.semanticStore = &recordingSemanticStore{}
	module.windowManager = newWindowManager(cfg, time.Now)

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
