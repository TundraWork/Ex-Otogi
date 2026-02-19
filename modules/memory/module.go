package memory

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"ex-otogi/pkg/otogi"
)

const (
	defaultMaxEntries = 10000
	defaultTTL        = 24 * time.Hour
)

// ServiceLogger is the optional service registry key for structured logging.
const ServiceLogger = "logger"

// Option mutates memory module configuration.
type Option func(*Module)

// WithLogger injects a logger directly, bypassing service lookup.
func WithLogger(logger *slog.Logger) Option {
	return func(module *Module) {
		if logger != nil {
			module.logger = logger
		}
	}
}

// WithMaxEntries sets the in-memory cache capacity.
func WithMaxEntries(maxEntries int) Option {
	return func(module *Module) {
		if maxEntries > 0 {
			module.maxEntries = maxEntries
		}
	}
}

// WithTTL sets how long an entry can be returned without refresh.
func WithTTL(ttl time.Duration) Option {
	return func(module *Module) {
		if ttl > 0 {
			module.ttl = ttl
		}
	}
}

// Module stores article projections and per-article event history.
type Module struct {
	logger     *slog.Logger
	dispatcher otogi.OutboundDispatcher
	maxEntries int
	ttl        time.Duration
	clock      func() time.Time

	mu       sync.Mutex
	records  map[cacheKey]*cacheRecord
	entities map[cacheKey]memorySnapshot
	events   map[cacheKey][]otogi.Event
	lru      *list.List
	index    map[cacheKey]*list.Element
}

type cacheKey struct {
	tenantID       string
	platform       otogi.Platform
	conversationID string
	articleID      string
}

type cacheRecord struct {
	expiresAt time.Time
}

type memorySnapshot struct {
	TenantID     string
	Platform     otogi.Platform
	Conversation otogi.Conversation
	Actor        otogi.Actor
	Article      otogi.Article
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// New creates a memory module with bounded in-memory storage.
func New(options ...Option) *Module {
	module := &Module{
		logger:     slog.Default(),
		maxEntries: defaultMaxEntries,
		ttl:        defaultTTL,
		clock:      time.Now,
		records:    make(map[cacheKey]*cacheRecord),
		entities:   make(map[cacheKey]memorySnapshot),
		events:     make(map[cacheKey][]otogi.Event),
		lru:        list.New(),
		index:      make(map[cacheKey]*list.Element),
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

// Spec declares which events mutate memory.
func (m *Module) Spec() otogi.ModuleSpec {
	return otogi.ModuleSpec{
		Handlers: []otogi.ModuleHandler{
			{
				Capability: otogi.Capability{
					Name:        "memory-writer",
					Description: "persists per-article event history and projects article state",
					Interest: otogi.InterestSet{
						Kinds: []otogi.EventKind{
							otogi.EventKindArticleCreated,
							otogi.EventKindArticleEdited,
							otogi.EventKindArticleRetracted,
							otogi.EventKindArticleReactionAdded,
							otogi.EventKindArticleReactionRemoved,
						},
					},
					RequiredServices: []string{otogi.ServiceOutboundDispatcher},
				},
				Subscription: otogi.NewDefaultSubscriptionSpec("memory-writer"),
				Handler:      m.handleEvent,
			},
			{
				Capability: otogi.Capability{
					Name:        "memory-introspection-command-handler",
					Description: "handles ~raw and ~history introspection commands",
					Interest: otogi.InterestSet{
						Kinds:          []otogi.EventKind{otogi.EventKindSystemCommandReceived},
						RequireCommand: true,
						CommandNames: []string{
							rawCommandName,
							historyCommandName,
						},
					},
					RequiredServices: []string{otogi.ServiceOutboundDispatcher},
				},
				Subscription: otogi.NewDefaultSubscriptionSpec("memory-command-handler"),
				Handler:      m.handleCommandEvent,
			},
		},
		Commands: []otogi.CommandSpec{
			{
				Prefix:      otogi.CommandPrefixSystem,
				Name:        rawCommandName,
				Description: "reply with raw article projection JSON",
			},
			{
				Prefix:      otogi.CommandPrefixSystem,
				Name:        historyCommandName,
				Description: "reply with article event history JSON",
			},
		},
	}
}

// OnRegister resolves dependencies and registers this module as MemoryService.
func (m *Module) OnRegister(_ context.Context, runtime otogi.ModuleRuntime) error {
	logger, err := otogi.ResolveAs[*slog.Logger](runtime.Services(), ServiceLogger)
	switch {
	case err == nil:
		m.logger = logger
	case errors.Is(err, otogi.ErrServiceNotFound):
	default:
		return fmt.Errorf("memory resolve logger: %w", err)
	}

	dispatcher, err := otogi.ResolveAs[otogi.OutboundDispatcher](
		runtime.Services(),
		otogi.ServiceOutboundDispatcher,
	)
	if err != nil {
		return fmt.Errorf("memory resolve outbound dispatcher: %w", err)
	}
	m.dispatcher = dispatcher

	if err := runtime.Services().Register(otogi.ServiceMemory, m); err != nil {
		return fmt.Errorf("memory register service %s: %w", otogi.ServiceMemory, err)
	}

	return nil
}

// OnStart starts the module lifecycle.
func (m *Module) OnStart(ctx context.Context) error {
	m.logger.InfoContext(ctx,
		"memory module started",
		"module", m.Name(),
		"max_entries", m.maxEntries,
		"ttl", m.ttl,
	)

	return nil
}

// OnShutdown clears cached state.
func (m *Module) OnShutdown(ctx context.Context) error {
	m.mu.Lock()
	recordCount := len(m.records)
	m.records = make(map[cacheKey]*cacheRecord)
	m.entities = make(map[cacheKey]memorySnapshot)
	m.events = make(map[cacheKey][]otogi.Event)
	m.index = make(map[cacheKey]*list.Element)
	m.lru.Init()
	m.mu.Unlock()

	m.logger.InfoContext(ctx,
		"memory module shutdown",
		"module", m.Name(),
		"entries", recordCount,
	)

	return nil
}

func withClock(clock func() time.Time) Option {
	return func(module *Module) {
		if clock != nil {
			module.clock = clock
		}
	}
}

var (
	_ otogi.Module          = (*Module)(nil)
	_ otogi.ModuleRegistrar = (*Module)(nil)
	_ otogi.MemoryService   = (*Module)(nil)
)
