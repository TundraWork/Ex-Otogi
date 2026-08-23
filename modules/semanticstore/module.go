package semanticstore

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

	"github.com/google/uuid"
)

const serviceLogger = "logger"

// Config configures semanticstore module behavior.
type Config struct {
	// DatabaseFile is the current-schema SQLite store path.
	DatabaseFile string
	// EmbeddingFingerprint identifies the only vector space accepted by the store.
	EmbeddingFingerprint EmbeddingFingerprint
}

// Validate checks semanticstore configuration coherence.
func (cfg Config) Validate() error {
	if strings.TrimSpace(cfg.DatabaseFile) == "" {
		return fmt.Errorf("validate semanticstore config: database_file is required")
	}
	return cfg.EmbeddingFingerprint.Validate()
}

// Module provides a lifecycle-managed semantic memory service.
type Module struct {
	store    ai.SemanticStore
	database *SQLiteStore
	cfg      Config
	enabled  bool
	logger   *slog.Logger
	clock    func() time.Time
	newID    func() string
}

// Option mutates semanticstore module construction behavior.
type Option func(*Module)

// WithLogger injects a logger directly, bypassing service lookup.
func WithLogger(logger *slog.Logger) Option {
	return func(module *Module) {
		if logger != nil {
			module.logger = logger
		}
	}
}

// New creates one semanticstore module instance.
func New(options ...Option) *Module {
	module := &Module{
		cfg:    Config{},
		logger: slog.Default(),
		clock:  time.Now,
		newID:  uuid.NewString,
	}
	for _, option := range options {
		option(module)
	}
	module.store = newStore(module.clock, module.newID)

	return module
}

// Name returns the stable module identifier.
func (m *Module) Name() string {
	return "semanticstore"
}

// Spec declares semanticstore module metadata.
func (m *Module) Spec() core.ModuleSpec {
	return core.ModuleSpec{}
}

// OnRegister resolves dependencies, loads config, and registers the memory
// service.
func (m *Module) OnRegister(_ context.Context, runtime core.ModuleRuntime) error {
	logger, err := core.ResolveAs[*slog.Logger](runtime.Services(), serviceLogger)
	switch {
	case err == nil:
		m.logger = logger
	case errors.Is(err, core.ErrServiceNotFound):
	default:
		return fmt.Errorf("semanticstore resolve logger: %w", err)
	}
	runtimeCfg, err := core.ResolveAs[llmconfig.Config](runtime.Services(), llm.ServiceRuntimeConfig)
	if err != nil {
		return fmt.Errorf("semanticstore resolve runtime config: %w", err)
	}
	cfg, err := loadConfig(runtimeCfg)
	if err != nil {
		return fmt.Errorf("semanticstore load config: %w", err)
	}
	m.cfg = cfg
	if runtimeCfg.Memory == nil {
		m.enabled = false
		return nil
	}
	m.enabled = true

	if err := runtime.Services().Register(ai.ServiceSemanticStore, m); err != nil {
		return fmt.Errorf("semanticstore register service %s: %w", ai.ServiceSemanticStore, err)
	}

	return nil
}

// OnStart opens the transactional current-schema memory database.
func (m *Module) OnStart(ctx context.Context) error {
	if m == nil {
		return fmt.Errorf("semanticstore start: nil module")
	}
	if !m.enabled {
		return nil
	}
	database, err := OpenSQLiteStore(ctx, m.cfg.DatabaseFile, m.cfg.EmbeddingFingerprint, m.clock, m.newID)
	if err != nil {
		return fmt.Errorf("semanticstore open database: %w", err)
	}
	m.database = database
	m.store = database
	return nil
}

// OnShutdown closes the transactional memory database.
func (m *Module) OnShutdown(ctx context.Context) error {
	if m == nil {
		return fmt.Errorf("semanticstore shutdown: nil module")
	}

	if m.database == nil {
		return nil
	}
	if err := m.database.Close(ctx); err != nil {
		return fmt.Errorf("semanticstore close database: %w", err)
	}
	m.database = nil
	return nil
}

// Store persists one semantic memory entry.
func (m *Module) Store(ctx context.Context, entry ai.SemanticEntry) (ai.SemanticRecord, error) {
	if m == nil || m.store == nil {
		return ai.SemanticRecord{}, fmt.Errorf("semanticstore store: store unavailable")
	}

	m.debugStore(ctx, entry)
	record, err := m.store.Store(ctx, entry)
	if err != nil {
		return ai.SemanticRecord{}, fmt.Errorf("semanticstore store record: %w", err)
	}
	m.debugStoreResult(ctx, record)

	return record, nil
}

// Search finds semantic memory matches within one scope.
func (m *Module) Search(ctx context.Context, query ai.SemanticQuery) ([]ai.SemanticMatch, error) {
	if m == nil || m.store == nil {
		return nil, fmt.Errorf("semanticstore search: store unavailable")
	}

	m.debugSearch(ctx, query)
	matches, err := m.store.Search(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("semanticstore search records: %w", err)
	}
	m.debugSearchResult(ctx, query, matches)

	return matches, nil
}

// Update replaces the mutable fields of one stored memory record.
func (m *Module) Update(ctx context.Context, update ai.SemanticUpdate) (ai.SemanticRecord, error) {
	if m == nil || m.store == nil {
		return ai.SemanticRecord{}, fmt.Errorf("semanticstore update: store unavailable")
	}

	m.debugUpdate(ctx, update)
	record, err := m.store.Update(ctx, update)
	if err != nil {
		return ai.SemanticRecord{}, fmt.Errorf("semanticstore update record: %w", err)
	}

	return record, nil
}

// Delete removes one stored memory record by ID.
func (m *Module) Delete(ctx context.Context, id string) error {
	if m == nil || m.store == nil {
		return fmt.Errorf("semanticstore delete: store unavailable")
	}

	m.debugDelete(ctx, id)
	if err := m.store.Delete(ctx, id); err != nil {
		return fmt.Errorf("semanticstore delete record: %w", err)
	}
	return nil
}

// ListByScope returns stored memory records for one scope.
func (m *Module) ListByScope(ctx context.Context, scope ai.SemanticScope, limit int) ([]ai.SemanticRecord, error) {
	if m == nil || m.store == nil {
		return nil, fmt.Errorf("semanticstore list by scope: store unavailable")
	}

	m.debugListByScope(ctx, scope, limit)
	records, err := m.store.ListByScope(ctx, scope, limit)
	if err != nil {
		return nil, fmt.Errorf("semanticstore list records: %w", err)
	}
	m.debugListByScopeResult(ctx, scope, len(records))

	return records, nil
}

func loadConfig(runtime llmconfig.Config) (Config, error) {
	if runtime.Memory == nil {
		return Config{}, nil
	}
	provider, exists := runtime.Providers[strings.TrimSpace(runtime.Memory.EmbeddingProvider)]
	if !exists {
		return Config{}, fmt.Errorf("embedding provider %s is not configured", runtime.Memory.EmbeddingProvider)
	}
	cfg := Config{
		DatabaseFile: strings.TrimSpace(runtime.Memory.DatabaseFile),
		EmbeddingFingerprint: EmbeddingFingerprint{
			Provider:   strings.TrimSpace(runtime.Memory.EmbeddingProvider),
			Model:      strings.TrimSpace(provider.EmbeddingModel),
			Dimensions: provider.EmbeddingDimensions,
		},
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

var (
	_ core.Module          = (*Module)(nil)
	_ core.ModuleRegistrar = (*Module)(nil)
	_ ai.SemanticStore     = (*Module)(nil)
)
