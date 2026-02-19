package pingpong

import (
	"context"
	"fmt"

	"ex-otogi/pkg/otogi"
)

const pingCommandName = "ping"

// Module replies with "pong!" when it receives a "/ping" command event.
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

// Spec declares interest in received ping command events.
func (m *Module) Spec() otogi.ModuleSpec {
	return otogi.ModuleSpec{
		Handlers: []otogi.ModuleHandler{
			{
				Capability: otogi.Capability{
					Name:        "ping-command-handler",
					Description: "responds with pong! for /ping commands",
					Interest: otogi.InterestSet{
						Kinds:          []otogi.EventKind{otogi.EventKindCommandReceived},
						RequireCommand: true,
						CommandNames:   []string{pingCommandName},
						RequireArticle: true,
					},
					RequiredServices: []string{otogi.ServiceOutboundDispatcher},
				},
				Subscription: otogi.NewDefaultSubscriptionSpec("pingpong-commands"),
				Handler:      m.handleCommand,
			},
		},
		Commands: []otogi.CommandSpec{
			{
				Prefix:      otogi.CommandPrefixOrdinary,
				Name:        pingCommandName,
				Description: "reply with pong!",
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

func (m *Module) handleCommand(ctx context.Context, event *otogi.Event) error {
	if event == nil || event.Command == nil || event.Article == nil {
		return nil
	}
	if event.Kind != otogi.EventKindCommandReceived {
		return nil
	}
	if event.Command.Name != pingCommandName {
		return nil
	}

	target, err := otogi.OutboundTargetFromEvent(event)
	if err != nil {
		return fmt.Errorf("pingpong derive outbound target: %w", err)
	}
	_, err = m.dispatcher.SendMessage(ctx, otogi.SendMessageRequest{
		Target:           target,
		Text:             "pong!",
		ReplyToMessageID: event.Article.ID,
	})
	if err != nil {
		return fmt.Errorf("pingpong send pong message: %w", err)
	}

	return nil
}
