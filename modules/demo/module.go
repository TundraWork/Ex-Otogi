package demo

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"ex-otogi/pkg/otogi"
)

// ServiceLogger is the service registry key for structured logging.
const ServiceLogger = "logger"

// Option mutates demo module configuration.
type Option func(*Module)

// WithLogger injects a logger directly, bypassing service lookup.
func WithLogger(logger *slog.Logger) Option {
	return func(module *Module) {
		if logger != nil {
			module.logger = logger
		}
	}
}

// Module demonstrates complex event handling for edits/media/state changes.
type Module struct {
	logger *slog.Logger

	mu       sync.Mutex
	counters map[otogi.EventKind]int
}

// New creates the demo module.
func New(options ...Option) *Module {
	module := &Module{
		logger:   slog.Default(),
		counters: make(map[otogi.EventKind]int),
	}
	for _, option := range options {
		option(module)
	}

	return module
}

// Name returns the module identifier.
func (m *Module) Name() string {
	return "demo"
}

// Spec declares module capabilities and subscription wiring in one place.
func (m *Module) Spec() otogi.ModuleSpec {
	return otogi.ModuleSpec{
		Handlers: []otogi.ModuleHandler{
			{
				Capability: otogi.Capability{
					Name:        "mutation-observer",
					Description: "handles message edit/retraction events",
					Interest: otogi.InterestSet{
						Kinds:           []otogi.EventKind{otogi.EventKindMessageEdited, otogi.EventKindMessageRetracted},
						RequireMutation: true,
					},
					RequiredServices: []string{ServiceLogger},
				},
				Subscription: defaultSubscription("demo-mutations"),
				Handler:      m.handleEvent,
			},
			{
				Capability: otogi.Capability{
					Name:        "media-observer",
					Description: "handles rich media messages",
					Interest: otogi.InterestSet{
						Kinds:      []otogi.EventKind{otogi.EventKindMessageCreated},
						MediaTypes: []otogi.MediaType{otogi.MediaTypePhoto, otogi.MediaTypeVideo, otogi.MediaTypeDocument},
					},
					RequiredServices: []string{ServiceLogger},
				},
				Subscription: defaultSubscription("demo-media"),
				Handler:      m.handleEvent,
			},
			{
				Capability: otogi.Capability{
					Name:        "state-observer",
					Description: "handles reactions and membership/role/chat state changes",
					Interest: otogi.InterestSet{
						Kinds: []otogi.EventKind{
							otogi.EventKindReactionAdded,
							otogi.EventKindReactionRemoved,
							otogi.EventKindMemberJoined,
							otogi.EventKindMemberLeft,
							otogi.EventKindRoleUpdated,
							otogi.EventKindChatMigrated,
						},
					},
					RequiredServices: []string{ServiceLogger},
				},
				Subscription: defaultSubscription("demo-state"),
				Handler:      m.handleEvent,
			},
		},
	}
}

// OnRegister resolves module dependencies before runtime start.
func (m *Module) OnRegister(_ context.Context, runtime otogi.ModuleRuntime) error {
	logger, err := otogi.ResolveAs[*slog.Logger](runtime.Services(), ServiceLogger)
	if err != nil {
		return fmt.Errorf("demo module resolve logger: %w", err)
	}
	m.logger = logger

	return nil
}

// OnStart emits startup telemetry.
func (m *Module) OnStart(ctx context.Context) error {
	m.logger.InfoContext(ctx, "demo module started", "module", m.Name())

	return nil
}

// OnShutdown emits final counters.
func (m *Module) OnShutdown(ctx context.Context) error {
	m.mu.Lock()
	stats := make(map[otogi.EventKind]int, len(m.counters))
	for eventKind, count := range m.counters {
		stats[eventKind] = count
	}
	m.mu.Unlock()

	m.logger.InfoContext(ctx, "demo module shutdown", "module", m.Name(), "stats", stats)

	return nil
}

// handleEvent routes each event kind to a specialized handler.
func (m *Module) handleEvent(ctx context.Context, event *otogi.Event) error {
	if event == nil {
		return fmt.Errorf("demo module received nil event")
	}
	m.increment(event.Kind)

	switch event.Kind {
	case otogi.EventKindMessageEdited, otogi.EventKindMessageRetracted:
		return m.handleMutationEvent(ctx, event)
	case otogi.EventKindMessageCreated:
		return m.handleMediaEvent(ctx, event)
	case otogi.EventKindReactionAdded, otogi.EventKindReactionRemoved:
		return m.handleReactionEvent(ctx, event)
	case otogi.EventKindMemberJoined, otogi.EventKindMemberLeft, otogi.EventKindRoleUpdated, otogi.EventKindChatMigrated:
		return m.handleStateEvent(ctx, event)
	default:
		m.logger.DebugContext(ctx, "demo module ignored event", "kind", event.Kind)
		return nil
	}
}

// handleMutationEvent logs edit/retraction mutation context.
func (m *Module) handleMutationEvent(ctx context.Context, event *otogi.Event) error {
	if event.Mutation == nil {
		return fmt.Errorf("handle mutation event %s: missing mutation payload", event.Kind)
	}

	m.logger.InfoContext(ctx,
		"demo mutation",
		"kind", event.Kind,
		"conversation", event.Conversation.ID,
		"message_id", event.Mutation.TargetMessageID,
		"reason", event.Mutation.Reason,
	)

	return nil
}

// handleMediaEvent records rich-media message events.
func (m *Module) handleMediaEvent(ctx context.Context, event *otogi.Event) error {
	if event.Message == nil {
		return fmt.Errorf("handle media event %s: missing message payload", event.Kind)
	}

	media := event.MessageMedia()
	if len(media) == 0 {
		m.logger.DebugContext(ctx, "demo media handler skipped non-media message", "message_id", event.Message.ID)
		return nil
	}

	m.logger.InfoContext(ctx,
		"demo media",
		"conversation", event.Conversation.ID,
		"message_id", event.Message.ID,
		"attachments", len(media),
	)

	return nil
}

// handleReactionEvent logs reaction add/remove events.
func (m *Module) handleReactionEvent(ctx context.Context, event *otogi.Event) error {
	if event.Reaction == nil {
		return fmt.Errorf("handle reaction event %s: missing reaction payload", event.Kind)
	}

	m.logger.InfoContext(ctx,
		"demo reaction",
		"kind", event.Kind,
		"conversation", event.Conversation.ID,
		"message_id", event.Reaction.MessageID,
		"emoji", event.Reaction.Emoji,
	)

	return nil
}

// handleStateEvent logs membership, role, and migration transitions.
func (m *Module) handleStateEvent(ctx context.Context, event *otogi.Event) error {
	if event.StateChange == nil {
		return fmt.Errorf("handle state event %s: missing state payload", event.Kind)
	}

	m.logger.InfoContext(ctx,
		"demo state",
		"kind", event.Kind,
		"conversation", event.Conversation.ID,
		"state_type", event.StateChange.Type,
	)

	return nil
}

// increment updates event counters under lock for shutdown reporting.
func (m *Module) increment(eventKind otogi.EventKind) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.counters[eventKind]++
}

func defaultSubscription(name string) otogi.SubscriptionSpec {
	return otogi.SubscriptionSpec{
		Name:           name,
		Buffer:         128,
		Workers:        2,
		HandlerTimeout: 2 * time.Second,
		Backpressure:   otogi.BackpressureDropNewest,
	}
}
