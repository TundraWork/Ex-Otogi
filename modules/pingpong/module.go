package pingpong

import (
	"context"
	"fmt"
	"strings"

	"ex-otogi/pkg/otogi"
)

// Module replies with "pong" when it receives a "ping" message event.
type Module struct {
	dispatcher otogi.OutboundDispatcher
}

// New creates a ping-pong module with default configuration.
func New() *Module {
	return &Module{}
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
						Kinds:          []otogi.EventKind{otogi.EventKindMessageCreated},
						RequireMessage: true,
					},
					RequiredServices: []string{otogi.ServiceOutboundDispatcher},
				},
				Subscription: otogi.NewDefaultSubscriptionSpec("pingpong-messages"),
				Handler:      m.handleMessage,
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
	if !isPing(event.Message.Text) {
		return nil
	}

	target, err := otogi.OutboundTargetFromEvent(event)
	if err != nil {
		return fmt.Errorf("pingpong derive outbound target: %w", err)
	}
	_, err = m.dispatcher.SendMessage(ctx, otogi.SendMessageRequest{
		Target:           target,
		Text:             "pong",
		ReplyToMessageID: event.Message.ID,
	})
	if err != nil {
		return fmt.Errorf("pingpong send pong message: %w", err)
	}

	return nil
}

func isPing(text string) bool {
	return strings.EqualFold(strings.TrimSpace(text), "ping")
}
