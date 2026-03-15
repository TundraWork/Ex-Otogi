package llmmemory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"ex-otogi/pkg/otogi/ai"
	"ex-otogi/pkg/otogi/core"

	"github.com/google/uuid"
)

const (
	defaultFlushInterval = 5 * time.Minute
	defaultMaxEntries    = 1000
	serviceLogger        = "logger"
)

// Config configures llmmemory module behavior.
type Config struct {
	// PersistenceFile is the optional JSON file used to persist stored memory
	// records across restarts.
	PersistenceFile string
	// FlushInterval controls how often the in-memory store is flushed to
	// PersistenceFile while the module is running.
	//
	// Zero disables periodic flush while still allowing final shutdown flush.
	FlushInterval time.Duration
	// MaxEntries caps the number of stored records per scope.
	MaxEntries int
}

// Validate checks llmmemory configuration coherence.
func (cfg Config) Validate() error {
	if cfg.FlushInterval < 0 {
		return fmt.Errorf("validate llmmemory config: flush_interval must be >= 0")
	}
	if cfg.MaxEntries <= 0 {
		return fmt.Errorf("validate llmmemory config: max_entries must be > 0")
	}

	return nil
}

type fileConfig struct {
	PersistenceFile string `json:"persistence_file"`
	FlushInterval   string `json:"flush_interval"`
	MaxEntries      int    `json:"max_entries"`
}

// Module provides a lifecycle-managed semantic memory service.
type Module struct {
	store     *Store
	cfg       Config
	logger    *slog.Logger
	clock     func() time.Time
	newID     func() string
	stopCh    chan struct{}
	flushDone chan struct{}
}

// Option mutates llmmemory module construction behavior.
type Option func(*Module)

// WithLogger injects a logger directly, bypassing service lookup.
func WithLogger(logger *slog.Logger) Option {
	return func(module *Module) {
		if logger != nil {
			module.logger = logger
		}
	}
}

// New creates one llmmemory module instance.
func New(options ...Option) *Module {
	module := &Module{
		cfg:    defaultConfig(),
		logger: slog.Default(),
		clock:  time.Now,
		newID:  uuid.NewString,
	}
	for _, option := range options {
		option(module)
	}
	module.store = newStore(module.cfg.MaxEntries, module.clock, module.newID)

	return module
}

// Name returns the stable module identifier.
func (m *Module) Name() string {
	return "llmmemory"
}

// Spec declares llmmemory module metadata.
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
		return fmt.Errorf("llmmemory resolve logger: %w", err)
	}

	cfg, err := loadConfig(runtime.Config())
	if err != nil {
		return fmt.Errorf("llmmemory load config: %w", err)
	}
	m.cfg = cfg
	m.store = newStore(m.cfg.MaxEntries, m.clock, m.newID)

	if err := runtime.Services().Register(ai.ServiceLLMMemory, m); err != nil {
		return fmt.Errorf("llmmemory register service %s: %w", ai.ServiceLLMMemory, err)
	}

	return nil
}

// OnStart loads persisted state and starts periodic flushing when configured.
func (m *Module) OnStart(ctx context.Context) error {
	if m == nil {
		return fmt.Errorf("llmmemory start: nil module")
	}
	if m.store == nil {
		m.store = newStore(m.cfg.MaxEntries, m.clock, m.newID)
	}
	if strings.TrimSpace(m.cfg.PersistenceFile) != "" {
		if err := m.store.LoadFromFile(m.cfg.PersistenceFile); err != nil && !errors.Is(err, os.ErrNotExist) {
			m.logger.WarnContext(ctx, "llmmemory load persistence", "error", err)
		} else {
			m.debugPersistenceLoad(ctx, m.cfg.PersistenceFile)
		}
	}
	if m.cfg.FlushInterval > 0 && strings.TrimSpace(m.cfg.PersistenceFile) != "" {
		m.startPeriodicFlush(ctx)
	}

	return nil
}

// OnShutdown stops the periodic flusher and persists the latest state.
func (m *Module) OnShutdown(ctx context.Context) error {
	if m == nil {
		return fmt.Errorf("llmmemory shutdown: nil module")
	}

	if err := m.stopPeriodicFlush(ctx); err != nil {
		return err
	}
	if strings.TrimSpace(m.cfg.PersistenceFile) != "" {
		if err := m.store.SaveToFile(m.cfg.PersistenceFile); err != nil {
			return fmt.Errorf("llmmemory final flush: %w", err)
		}
		m.debugPersistenceSave(ctx, m.cfg.PersistenceFile)
	}

	return nil
}

// Store persists one semantic memory entry.
func (m *Module) Store(ctx context.Context, entry ai.LLMMemoryEntry) (ai.LLMMemoryRecord, error) {
	if m == nil || m.store == nil {
		return ai.LLMMemoryRecord{}, fmt.Errorf("llmmemory store: store unavailable")
	}

	m.debugStore(ctx, entry)
	record, err := m.store.Store(ctx, entry)
	if err != nil {
		return ai.LLMMemoryRecord{}, err
	}
	m.debugStoreResult(ctx, record)

	return record, nil
}

// Search finds semantic memory matches within one scope.
func (m *Module) Search(ctx context.Context, query ai.LLMMemoryQuery) ([]ai.LLMMemoryMatch, error) {
	if m == nil || m.store == nil {
		return nil, fmt.Errorf("llmmemory search: store unavailable")
	}

	m.debugSearch(ctx, query)
	matches, err := m.store.Search(ctx, query)
	if err != nil {
		return nil, err
	}
	m.debugSearchResult(ctx, query, matches)

	return matches, nil
}

// Update replaces the content and embedding of one stored memory record.
func (m *Module) Update(ctx context.Context, id string, content string, embedding []float32) error {
	if m == nil || m.store == nil {
		return fmt.Errorf("llmmemory update: store unavailable")
	}

	m.debugUpdate(ctx, id, content)

	return m.store.Update(ctx, id, content, embedding)
}

// Delete removes one stored memory record by ID.
func (m *Module) Delete(ctx context.Context, id string) error {
	if m == nil || m.store == nil {
		return fmt.Errorf("llmmemory delete: store unavailable")
	}

	m.debugDelete(ctx, id)

	return m.store.Delete(ctx, id)
}

// ListByScope returns stored memory records for one scope.
func (m *Module) ListByScope(ctx context.Context, scope ai.LLMMemoryScope, limit int) ([]ai.LLMMemoryRecord, error) {
	if m == nil || m.store == nil {
		return nil, fmt.Errorf("llmmemory list by scope: store unavailable")
	}

	m.debugListByScope(ctx, scope, limit)
	records, err := m.store.ListByScope(ctx, scope, limit)
	if err != nil {
		return nil, err
	}
	m.debugListByScopeResult(ctx, scope, len(records))

	return records, nil
}

func defaultConfig() Config {
	return Config{
		FlushInterval: defaultFlushInterval,
		MaxEntries:    defaultMaxEntries,
	}
}

func loadConfig(registry core.ConfigRegistry) (Config, error) {
	cfg := defaultConfig()
	if registry == nil {
		return Config{}, fmt.Errorf("nil config registry")
	}

	raw, err := registry.Resolve("llmmemory")
	switch {
	case err == nil:
	case errors.Is(err, core.ErrConfigNotFound):
		return cfg, nil
	default:
		return Config{}, fmt.Errorf("resolve module config: %w", err)
	}

	var parsed fileConfig
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return Config{}, fmt.Errorf("unmarshal: %w", err)
	}

	cfg.PersistenceFile = strings.TrimSpace(parsed.PersistenceFile)
	if strings.TrimSpace(parsed.FlushInterval) != "" {
		duration, err := time.ParseDuration(strings.TrimSpace(parsed.FlushInterval))
		if err != nil {
			return Config{}, fmt.Errorf("parse flush_interval: %w", err)
		}
		cfg.FlushInterval = duration
	}
	if parsed.MaxEntries != 0 {
		cfg.MaxEntries = parsed.MaxEntries
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func (m *Module) startPeriodicFlush(ctx context.Context) {
	stopCh := make(chan struct{})
	doneCh := make(chan struct{})
	m.stopCh = stopCh
	m.flushDone = doneCh

	go func() {
		defer close(doneCh)
		defer func() {
			if recovered := recover(); recovered != nil {
				m.logger.ErrorContext(ctx, "llmmemory periodic flush panic", "recover", recovered)
			}
		}()

		ticker := time.NewTicker(m.cfg.FlushInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-stopCh:
				return
			case <-ticker.C:
				if err := m.store.SaveToFile(m.cfg.PersistenceFile); err != nil {
					m.logger.ErrorContext(ctx, "llmmemory periodic flush", "error", err)
				}
			}
		}
	}()
}

func (m *Module) stopPeriodicFlush(ctx context.Context) error {
	if m.stopCh == nil {
		return nil
	}

	stopCh := m.stopCh
	doneCh := m.flushDone
	m.stopCh = nil
	m.flushDone = nil
	close(stopCh)

	if doneCh == nil {
		return nil
	}

	select {
	case <-doneCh:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("llmmemory stop periodic flush: %w", ctx.Err())
	}
}

func withClock(clock func() time.Time) Option {
	return func(module *Module) {
		if clock != nil {
			module.clock = clock
		}
	}
}

func withIDGenerator(generator func() string) Option {
	return func(module *Module) {
		if generator != nil {
			module.newID = generator
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
	_ ai.LLMMemoryService  = (*Module)(nil)
)
