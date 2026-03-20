package naturalmemory

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"ex-otogi/pkg/otogi/ai"
	"ex-otogi/pkg/otogi/core"
	"ex-otogi/pkg/otogi/platform"
)

const (
	serviceLogger               = "logger"
	consolidationHandlerTimeout = 5 * time.Second
)

// Module provides automatic long-term memory formation from article activity.
type Module struct {
	cfg Config

	llmMemory             ai.LLMMemoryService
	embeddingProvider     ai.EmbeddingProvider
	extractionProvider    ai.LLMProvider
	consolidationProvider ai.LLMProvider
	memory                core.MemoryService
	logger                *slog.Logger
	clock                 func() time.Time

	activeScopes   map[string]ai.LLMMemoryScope
	activeScopesMu sync.Mutex

	stopCh    chan struct{}
	flushDone chan struct{}
}

// Option mutates naturalmemory module construction.
type Option func(*Module)

// New creates one naturalmemory module instance.
func New(options ...Option) *Module {
	module := &Module{
		cfg:          defaultConfig(),
		logger:       slog.Default(),
		clock:        time.Now,
		activeScopes: make(map[string]ai.LLMMemoryScope),
	}
	for _, option := range options {
		option(module)
	}

	return module
}

// Name returns the stable module identifier.
func (m *Module) Name() string {
	return "naturalmemory"
}

// Spec declares naturalmemory module metadata.
func (m *Module) Spec() core.ModuleSpec {
	return core.ModuleSpec{}
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
		return fmt.Errorf("naturalmemory resolve logger: %w", err)
	}

	cfg, err := loadConfig(runtime.Config())
	if err != nil {
		return fmt.Errorf("naturalmemory load config: %w", err)
	}
	m.cfg = cfg
	m.debugConfigLoaded(ctx, cfg)

	if !m.cfg.Enabled {
		return nil
	}

	llmMemoryService, err := core.ResolveAs[ai.LLMMemoryService](runtime.Services(), ai.ServiceLLMMemory)
	if err != nil {
		return fmt.Errorf("naturalmemory resolve llm memory: %w", err)
	}
	memoryService, err := core.ResolveAs[core.MemoryService](runtime.Services(), core.ServiceMemory)
	if err != nil {
		return fmt.Errorf("naturalmemory resolve memory service: %w", err)
	}
	providerRegistry, err := core.ResolveAs[ai.LLMProviderRegistry](runtime.Services(), ai.ServiceLLMProviderRegistry)
	if err != nil {
		return fmt.Errorf("naturalmemory resolve provider registry: %w", err)
	}
	embeddingRegistry, err := core.ResolveAs[ai.EmbeddingProviderRegistry](
		runtime.Services(),
		ai.ServiceEmbeddingProviderRegistry,
	)
	if err != nil {
		return fmt.Errorf("naturalmemory resolve embedding registry: %w", err)
	}

	extractionProvider, err := providerRegistry.Resolve(m.cfg.ExtractionProvider)
	if err != nil {
		return fmt.Errorf("naturalmemory resolve extraction provider %s: %w", m.cfg.ExtractionProvider, err)
	}
	embeddingProvider, err := embeddingRegistry.Resolve(m.cfg.EmbeddingProvider)
	if err != nil {
		return fmt.Errorf("naturalmemory resolve embedding provider %s: %w", m.cfg.EmbeddingProvider, err)
	}

	var consolidationProvider ai.LLMProvider
	if m.cfg.ConsolidationInterval > 0 {
		consolidationProvider, err = providerRegistry.Resolve(m.cfg.ConsolidationProvider)
		if err != nil {
			return fmt.Errorf(
				"naturalmemory resolve consolidation provider %s: %w",
				m.cfg.ConsolidationProvider,
				err,
			)
		}
	}

	m.llmMemory = llmMemoryService
	m.memory = memoryService
	m.extractionProvider = extractionProvider
	m.embeddingProvider = embeddingProvider
	m.consolidationProvider = consolidationProvider

	handlerTimeout := m.cfg.ExtractionTimeout + consolidationHandlerTimeout
	if _, err := runtime.Subscribe(ctx, core.InterestSet{
		Kinds:          []platform.EventKind{platform.EventKindArticleCreated},
		RequireArticle: true,
	}, core.SubscriptionSpec{
		Name:           "naturalmemory-articles",
		Buffer:         64,
		Workers:        1,
		Backpressure:   core.BackpressureDropOldest,
		HandlerTimeout: handlerTimeout,
	}, m.handleArticle); err != nil {
		return fmt.Errorf("naturalmemory subscribe: %w", err)
	}

	return nil
}

// OnStart starts background consolidation when configured.
func (m *Module) OnStart(ctx context.Context) error {
	if m == nil {
		return fmt.Errorf("naturalmemory start: nil module")
	}
	if m.cfg.Enabled && m.cfg.ConsolidationInterval > 0 {
		m.startConsolidation(ctx)
	}

	return nil
}

// OnShutdown stops background consolidation.
func (m *Module) OnShutdown(ctx context.Context) error {
	if m == nil {
		return fmt.Errorf("naturalmemory shutdown: nil module")
	}

	return m.stopConsolidation(ctx)
}

func (m *Module) handleArticle(ctx context.Context, event *platform.Event) error {
	if !m.cfg.Enabled || event == nil || event.Article == nil {
		return nil
	}
	if strings.TrimSpace(event.Article.Text) == "" {
		return nil
	}

	scope := ai.LLMMemoryScope{
		TenantID:       event.TenantID,
		Platform:       string(event.Source.Platform),
		ConversationID: event.Conversation.ID,
	}
	m.rememberScope(scope)
	m.debugArticleObserved(ctx, scope, event.Article.ID)

	contextWindow, err := m.buildExtractionContext(ctx, event)
	if err != nil {
		return fmt.Errorf("naturalmemory build context: %w", err)
	}
	if err := m.extractMemories(ctx, scope, contextWindow); err != nil {
		return fmt.Errorf("naturalmemory extract memories: %w", err)
	}

	return nil
}

func (m *Module) rememberScope(scope ai.LLMMemoryScope) {
	if m == nil {
		return
	}

	key := scopeKey(scope)

	m.activeScopesMu.Lock()
	defer m.activeScopesMu.Unlock()

	if m.activeScopes == nil {
		m.activeScopes = make(map[string]ai.LLMMemoryScope)
	}
	m.activeScopes[key] = scope
}

func (m *Module) drainActiveScopes() []ai.LLMMemoryScope {
	if m == nil {
		return nil
	}

	m.activeScopesMu.Lock()
	defer m.activeScopesMu.Unlock()

	if len(m.activeScopes) == 0 {
		return nil
	}

	scopes := make([]ai.LLMMemoryScope, 0, len(m.activeScopes))
	for key, scope := range m.activeScopes {
		scopes = append(scopes, scope)
		delete(m.activeScopes, key)
	}

	return scopes
}

func scopeKey(scope ai.LLMMemoryScope) string {
	return scope.TenantID + "\x00" + scope.Platform + "\x00" + scope.ConversationID
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
)
