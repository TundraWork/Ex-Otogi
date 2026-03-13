package memory

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"ex-otogi/pkg/otogi/core"
	"ex-otogi/pkg/otogi/platform"
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
	dispatcher platform.SinkDispatcher
	maxEntries int
	ttl        time.Duration
	clock      func() time.Time

	mu                       sync.RWMutex
	records                  map[cacheKey]*cacheRecord
	entities                 map[cacheKey]memorySnapshot
	events                   map[cacheKey][]platform.Event
	streams                  map[conversationStreamKey][]conversationStreamEntry
	articleStreams           map[cacheKey]conversationArticleStream
	lru                      *list.List
	index                    map[cacheKey]*list.Element
	nextConversationSequence uint64
}

type cacheKey struct {
	tenantID       string
	platform       platform.Platform
	conversationID string
	articleID      string
}

type cacheRecord struct {
	expiresAt time.Time
}

type conversationStreamKey struct {
	tenantID       string
	platform       platform.Platform
	conversationID string
	threadID       string
}

type conversationStreamEntry struct {
	key       cacheKey
	createdAt time.Time
	sequence  uint64
}

type conversationArticleStream struct {
	streamKey conversationStreamKey
	createdAt time.Time
	sequence  uint64
}

type memorySnapshot struct {
	TenantID     string
	Platform     platform.Platform
	Conversation platform.Conversation
	Actor        platform.Actor
	Article      platform.Article
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// New creates a memory module with bounded in-memory storage.
func New(options ...Option) *Module {
	module := &Module{
		logger:         slog.Default(),
		maxEntries:     defaultMaxEntries,
		ttl:            defaultTTL,
		clock:          time.Now,
		records:        make(map[cacheKey]*cacheRecord),
		entities:       make(map[cacheKey]memorySnapshot),
		events:         make(map[cacheKey][]platform.Event),
		streams:        make(map[conversationStreamKey][]conversationStreamEntry),
		articleStreams: make(map[cacheKey]conversationArticleStream),
		lru:            list.New(),
		index:          make(map[cacheKey]*list.Element),
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
func (m *Module) Spec() core.ModuleSpec {
	return core.ModuleSpec{
		Handlers: []core.ModuleHandler{
			{
				Capability: core.Capability{
					Name:        "memory-writer",
					Description: "persists per-article event history and projects article state",
					Interest: core.InterestSet{
						Kinds: []platform.EventKind{
							platform.EventKindArticleCreated,
							platform.EventKindArticleEdited,
							platform.EventKindArticleRetracted,
							platform.EventKindArticleReactionAdded,
							platform.EventKindArticleReactionRemoved,
						},
					},
					RequiredServices: []string{platform.ServiceSinkDispatcher},
				},
				Subscription: core.NewDefaultSubscriptionSpec("memory-writer"),
				Handler:      m.handleEvent,
			},
			{
				Capability: core.Capability{
					Name:        "memory-introspection-command-handler",
					Description: "handles ~raw and ~history introspection commands",
					Interest: core.InterestSet{
						Kinds:          []platform.EventKind{platform.EventKindSystemCommandReceived},
						RequireCommand: true,
						CommandNames: []string{
							rawCommandName,
							historyCommandName,
						},
					},
					RequiredServices: []string{platform.ServiceSinkDispatcher},
				},
				Subscription: core.NewDefaultSubscriptionSpec("memory-command-handler"),
				Handler:      m.handleCommandEvent,
			},
		},
		Commands: []platform.CommandSpec{
			{
				Prefix:      platform.CommandPrefixSystem,
				Name:        rawCommandName,
				Description: "reply with raw article projection JSON",
			},
			{
				Prefix:      platform.CommandPrefixSystem,
				Name:        historyCommandName,
				Description: "reply with article event history JSON",
			},
		},
	}
}

// OnRegister resolves dependencies and registers this module as MemoryService.
func (m *Module) OnRegister(_ context.Context, runtime core.ModuleRuntime) error {
	logger, err := core.ResolveAs[*slog.Logger](runtime.Services(), ServiceLogger)
	switch {
	case err == nil:
		m.logger = logger
	case errors.Is(err, core.ErrServiceNotFound):
	default:
		return fmt.Errorf("memory resolve logger: %w", err)
	}

	dispatcher, err := core.ResolveAs[platform.SinkDispatcher](
		runtime.Services(),
		platform.ServiceSinkDispatcher,
	)
	if err != nil {
		return fmt.Errorf("memory resolve outbound dispatcher: %w", err)
	}
	m.dispatcher = dispatcher

	if err := runtime.Services().Register(core.ServiceMemory, m); err != nil {
		return fmt.Errorf("memory register service %s: %w", core.ServiceMemory, err)
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
	m.events = make(map[cacheKey][]platform.Event)
	m.streams = make(map[conversationStreamKey][]conversationStreamEntry)
	m.articleStreams = make(map[cacheKey]conversationArticleStream)
	m.index = make(map[cacheKey]*list.Element)
	m.lru.Init()
	m.nextConversationSequence = 0
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
	_ core.Module          = (*Module)(nil)
	_ core.ModuleRegistrar = (*Module)(nil)
	_ core.MemoryService   = (*Module)(nil)
)
