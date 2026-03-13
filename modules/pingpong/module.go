package pingpong

import (
	"context"
	"fmt"

	"ex-otogi/pkg/otogi/core"
	"ex-otogi/pkg/otogi/platform"
)

const pingCommandName = "ping"

// Module replies with "pong!" when it receives a "/ping" command event.
type Module struct {
	dispatcher platform.SinkDispatcher
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
func (m *Module) Spec() core.ModuleSpec {
	return core.ModuleSpec{
		Handlers: []core.ModuleHandler{
			{
				Capability: core.Capability{
					Name:        "ping-command-handler",
					Description: "responds with pong! for /ping commands",
					Interest: core.InterestSet{
						Kinds:          []platform.EventKind{platform.EventKindCommandReceived},
						RequireCommand: true,
						CommandNames:   []string{pingCommandName},
						RequireArticle: true,
					},
					RequiredServices: []string{platform.ServiceSinkDispatcher},
				},
				Subscription: core.NewDefaultSubscriptionSpec("pingpong-commands"),
				Handler:      m.handleCommand,
			},
		},
		Commands: []platform.CommandSpec{
			{
				Prefix:      platform.CommandPrefixOrdinary,
				Name:        pingCommandName,
				Description: "reply with pong!",
			},
		},
	}
}

// OnRegister resolves outbound dependencies required by this module.
func (m *Module) OnRegister(_ context.Context, runtime core.ModuleRuntime) error {
	dispatcher, err := core.ResolveAs[platform.SinkDispatcher](
		runtime.Services(),
		platform.ServiceSinkDispatcher,
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

func (m *Module) handleCommand(ctx context.Context, event *platform.Event) error {
	if event == nil || event.Command == nil || event.Article == nil {
		return nil
	}
	if event.Kind != platform.EventKindCommandReceived {
		return nil
	}
	if event.Command.Name != pingCommandName {
		return nil
	}

	target, err := platform.OutboundTargetFromEvent(event)
	if err != nil {
		return fmt.Errorf("pingpong derive outbound target: %w", err)
	}
	_, err = m.dispatcher.SendMessage(ctx, platform.SendMessageRequest{
		Target:           target,
		Text:             "pong!",
		ReplyToMessageID: event.Article.ID,
	})
	if err != nil {
		return fmt.Errorf("pingpong send pong message: %w", err)
	}

	return nil
}
