package pingpong

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"ex-otogi/pkg/otogi"
)

const (
	defaultSubscriptionBuffer  = 32
	defaultSubscriptionWorkers = 1
	defaultHandlerTimeout      = time.Second
)

// Module replies with "pong" when it receives a "ping" message event.
type Module struct {
	logger     *slog.Logger
	dispatcher otogi.OutboundDispatcher
}

// New creates a ping-pong module with default configuration.
func New() *Module {
	return newWithLogger(nil)
}

func newWithLogger(logger *slog.Logger) *Module {
	if logger == nil {
		logger = slog.Default()
	}

	return &Module{logger: logger}
}

// Name returns the stable module identifier.
func (m *Module) Name() string {
	return "pingpong"
}

// Spec declares interest in newly-created messages.
func (m *Module) Spec() otogi.ModuleSpec {
	return otogi.ModuleSpec{
		Handlers: []otogi.ModuleHandler{
			{
				Capability: otogi.Capability{
					Name:        "ping-listener",
					Description: "responds with pong for ping messages",
					Interest: otogi.InterestSet{
						Kinds: []otogi.EventKind{otogi.EventKindMessageCreated},
					},
					RequiredServices: []string{otogi.ServiceOutboundDispatcher},
				},
				Subscription: otogi.SubscriptionSpec{
					Name:           "pingpong-messages",
					Buffer:         defaultSubscriptionBuffer,
					Workers:        defaultSubscriptionWorkers,
					HandlerTimeout: defaultHandlerTimeout,
					Backpressure:   otogi.BackpressureDropNewest,
				},
				Handler: m.handleMessage,
			},
		},
	}
}

// OnRegister resolves outbound dependencies required by this module.
func (m *Module) OnRegister(_ context.Context, runtime otogi.ModuleRuntime) error {
	dispatcher, err := otogi.ResolveAs[otogi.OutboundDispatcher](
		runtime.Services(),
		otogi.ServiceOutboundDispatcher,
	)
	if err != nil {
		return fmt.Errorf("pingpong resolve outbound dispatcher: %w", err)
	}

	m.dispatcher = dispatcher

	return nil
}

// OnStart starts the module lifecycle.
func (m *Module) OnStart(_ context.Context) error {
	return nil
}

// OnShutdown stops the module lifecycle.
func (m *Module) OnShutdown(_ context.Context) error {
	return nil
}

func (m *Module) handleMessage(ctx context.Context, event *otogi.Event) error {
	if event == nil {
		return fmt.Errorf("pingpong module: nil event")
	}
	if event.Message == nil {
		return fmt.Errorf("pingpong module event %s: missing message payload", event.Kind)
	}
	if !isPing(event.Message.Text) {
		return nil
	}
	if m.dispatcher == nil {
		return fmt.Errorf("pingpong module: outbound dispatcher not configured")
	}

	target, err := otogi.OutboundTargetFromEvent(event)
	if err != nil {
		return fmt.Errorf("pingpong derive outbound target: %w", err)
	}
	sent, err := m.dispatcher.SendMessage(ctx, otogi.SendMessageRequest{
		Target:           target,
		Text:             "pong",
		ReplyToMessageID: event.Message.ID,
	})
	if err != nil {
		return fmt.Errorf("pingpong send pong message: %w", err)
	}

	m.logger.InfoContext(ctx, "pong",
		"module", m.Name(),
		"conversation", event.Conversation.ID,
		"message_id", event.Message.ID,
		"sent_message_id", sent.ID,
	)

	return nil
}

func isPing(text string) bool {
	return strings.EqualFold(strings.TrimSpace(text), "ping")
}
