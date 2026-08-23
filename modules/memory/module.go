package memory

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"ex-otogi/pkg/llm"
	llmconfig "ex-otogi/pkg/llm/config"
	"ex-otogi/pkg/otogi/ai"
	"ex-otogi/pkg/otogi/core"
	panel "ex-otogi/pkg/otogi/management"
	"ex-otogi/pkg/otogi/platform"
)

const (
	serviceLogger        = "logger"
	enqueueHandlerBuffer = 64
	enqueueHandlerTimout = 2 * time.Second
)

// Module provides automatic long-term memory formation from article activity
// and semantic memory retrieval for conversation context.
type Module struct {
	cfg Config

	semanticStore      ai.SemanticStore
	embeddingProvider  ai.EmbeddingProvider
	embeddingRegistry  ai.EmbeddingProviderRegistry
	extractionProvider ai.LLMProvider
	providerRegistry   ai.LLMProviderRegistry
	memory             core.MemoryService
	logger             *slog.Logger
	recorder           panel.Recorder
	clock              func() time.Time
	sleep              func(context.Context, time.Duration) error

	windowManager *windowManager
	flusher       *flushWorker

	stopCh    chan struct{}
	flushDone chan struct{}
}

// Option mutates memory module construction.
type Option func(*Module)

// New creates one memory module instance.
func New(options ...Option) *Module {
	module := &Module{
		cfg:    defaultConfig(),
		logger: slog.Default(),
		clock:  time.Now,
		sleep:  sleepWithContext,
	}
	for _, option := range options {
		option(module)
	}

	return module
}

// Name returns the stable module identifier.
func (m *Module) Name() string {
	return "memory"
}

// Spec declares memory module metadata and capabilities.
func (m *Module) Spec() core.ModuleSpec {
	return core.ModuleSpec{
		AdditionalCapabilities: []core.Capability{
			{
				Name:        "memory-article-observer",
				Description: "observes article events for automatic long-term memory extraction",
				Interest: core.InterestSet{
					Kinds:          []platform.EventKind{platform.EventKindArticleCreated},
					RequireArticle: true,
				},
				RequiredServices: []string{
					ai.ServiceSemanticStore,
					core.ServiceMemory,
				},
			},
		},
	}
}

// OnRegister resolves dependencies and subscribes to article events when the
// module is enabled.
func (m *Module) OnRegister(ctx context.Context, runtime core.ModuleRuntime) error {
	logger, err := core.ResolveAs[*slog.Logger](runtime.Services(), serviceLogger)
	switch {
	case err == nil:
		m.logger = logger
	case errors.Is(err, core.ErrServiceNotFound):
	default:
		return fmt.Errorf("memory resolve logger: %w", err)
	}
	recorder, err := core.ResolveAs[panel.Recorder](runtime.Services(), panel.ServiceRecorder)
	switch {
	case err == nil:
		m.recorder = recorder
	case errors.Is(err, core.ErrServiceNotFound):
	default:
		return fmt.Errorf("memory resolve recorder: %w", err)
	}

	llmCfg, err := core.ResolveAs[llmconfig.Config](runtime.Services(), llm.ServiceRuntimeConfig)
	if err != nil {
		return fmt.Errorf("memory resolve runtime config: %w", err)
	}
	cfg, err := loadConfig(llmCfg)
	if err != nil {
		return fmt.Errorf("memory load config: %w", err)
	}
	m.cfg = cfg
	m.debugConfigLoaded(ctx, cfg)

	if !m.cfg.Enabled {
		return nil
	}

	semanticStoreService, err := core.ResolveAs[ai.SemanticStore](runtime.Services(), ai.ServiceSemanticStore)
	if err != nil {
		return fmt.Errorf("memory resolve semantic store: %w", err)
	}
	memoryService, err := core.ResolveAs[core.MemoryService](runtime.Services(), core.ServiceMemory)
	if err != nil {
		return fmt.Errorf("memory resolve memory service: %w", err)
	}
	providerRegistry, err := core.ResolveAs[ai.LLMProviderRegistry](runtime.Services(), ai.ServiceLLMProviderRegistry)
	if err != nil {
		return fmt.Errorf("memory resolve provider registry: %w", err)
	}
	embeddingRegistry, err := core.ResolveAs[ai.EmbeddingProviderRegistry](
		runtime.Services(),
		ai.ServiceEmbeddingProviderRegistry,
	)
	if err != nil {
		return fmt.Errorf("memory resolve embedding registry: %w", err)
	}

	extractionProvider, err := providerRegistry.Resolve(m.cfg.ExtractionProvider)
	if err != nil {
		return fmt.Errorf("memory resolve extraction provider %s: %w", m.cfg.ExtractionProvider, err)
	}
	embeddingProvider, err := embeddingRegistry.Resolve(m.cfg.EmbeddingProvider)
	if err != nil {
		return fmt.Errorf("memory resolve embedding provider %s: %w", m.cfg.EmbeddingProvider, err)
	}

	m.semanticStore = semanticStoreService
	m.memory = memoryService
	m.extractionProvider = extractionProvider
	m.embeddingProvider = embeddingProvider
	m.providerRegistry = providerRegistry
	m.embeddingRegistry = embeddingRegistry
	m.windowManager = newWindowManager(m.cfg, m.clock)

	if err := runtime.Services().Register(ai.ServiceSemanticRetriever, m); err != nil {
		return fmt.Errorf("memory register semantic retriever service: %w", err)
	}

	if _, err := runtime.Subscribe(ctx, core.InterestSet{
		Kinds:          []platform.EventKind{platform.EventKindArticleCreated},
		RequireArticle: true,
	}, core.SubscriptionSpec{
		Name:           "memory-articles",
		Buffer:         enqueueHandlerBuffer,
		Workers:        1,
		Backpressure:   core.BackpressureDropOldest,
		HandlerTimeout: enqueueHandlerTimout,
	}, m.handleArticle); err != nil {
		return fmt.Errorf("memory subscribe: %w", err)
	}

	return nil
}

// OnStart starts the background flush worker and consolidation when configured.
func (m *Module) OnStart(ctx context.Context) error {
	if m == nil {
		return fmt.Errorf("memory start: nil module")
	}
	if !m.cfg.Enabled {
		return nil
	}

	// Start the flush worker.
	m.flusher = &flushWorker{
		interval: m.cfg.BufferCheckInterval,
		clock:    m.clock,
		manager:  m.windowManager,
		process: func(ctx context.Context, w readyWindow) error {
			return m.processWindow(ctx, w.Scope, w.Articles, w.Reason)
		},
		logger:   m.logger,
		recorder: m.recorder,
	}
	m.flusher.Start(ctx)

	// Start consolidation if configured.
	if m.cfg.ConsolidationInterval > 0 {
		m.startConsolidation(ctx)
	}

	return nil
}

// OnShutdown stops the flush worker first (draining all windows), then
// stops the consolidation worker.
func (m *Module) OnShutdown(ctx context.Context) error {
	if m == nil {
		return fmt.Errorf("memory shutdown: nil module")
	}

	// Stop flush worker first — it drains all pending windows.
	if m.flusher != nil {
		if err := m.flusher.Stop(ctx); err != nil && m.logger != nil {
			m.logger.WarnContext(ctx, "memory flush worker stop", "error", err)
		}
	}

	// Then stop the consolidation worker.
	return m.stopConsolidation(ctx)
}

func (m *Module) handleArticle(ctx context.Context, event *platform.Event) error {
	if m.recorder != nil {
		ctx = panel.WithRecorder(ctx, m.recorder)
	}
	if !m.cfg.Enabled || event == nil || event.Article == nil {
		return nil
	}
	if strings.TrimSpace(event.Article.Text) == "" {
		return nil
	}

	scope := ai.SemanticScope{
		TenantID:       event.TenantID,
		Platform:       string(event.Source.Platform),
		ConversationID: event.Conversation.ID,
	}
	m.windowManager.Enqueue(scope, bufferedArticle{
		Article:    *event.Article,
		Actor:      event.Actor,
		OccurredAt: normalizeAnchorTime(event, m.now()),
		ReceivedAt: m.now(),
	})
	m.debugWindowEnqueue(ctx, scope, event.Article.ID)

	return nil
}

func (m *Module) now() time.Time {
	if m == nil || m.clock == nil {
		return time.Now().UTC()
	}

	return m.clock().UTC()
}

func withClock(clock func() time.Time) Option {
	return func(module *Module) {
		if clock != nil {
			module.clock = clock
		}
	}
}

func withConfig(cfg Config) Option {
	return func(module *Module) {
		module.cfg = cfg
	}
}

var (
	_ core.Module          = (*Module)(nil)
	_ core.ModuleRegistrar = (*Module)(nil)
	_ ai.SemanticRetriever = (*Module)(nil)
)
