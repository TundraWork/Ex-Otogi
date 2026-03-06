package sleep

import (
	"context"
	"fmt"

	"ex-otogi/pkg/otogi"
)

const (
	sleepCommandName = "sleep"
	wakeCommandName  = "wake"
)

// Config configures sleep-module wake-code generation.
type Config struct {
	// SigningKey authenticates stateless wake codes across process restarts.
	SigningKey []byte
}

// Module provides user self-mute functionality via /sleep and /wake commands.
//
// When a user invokes /sleep <duration>, the module restricts their chat
// permissions for the specified duration. A wake code is posted in the same
// conversation so the user can restore permissions early with /wake <code>.
type Module struct {
	dispatcher  otogi.SinkDispatcher
	moderation  otogi.ModerationDispatcher
	codeManager *codeManager
}

// New creates a sleep module with configured wake-code signing.
func New(cfg Config) (*Module, error) {
	cm, err := newCodeManager(cfg.SigningKey)
	if err != nil {
		return nil, fmt.Errorf("new sleep module: %w", err)
	}

	return &Module{
		codeManager: cm,
	}, nil
}

// Name returns the stable module identifier.
func (m *Module) Name() string {
	return "sleep"
}

// Spec declares interest in received sleep and wake command events.
func (m *Module) Spec() otogi.ModuleSpec {
	return otogi.ModuleSpec{
		Handlers: []otogi.ModuleHandler{
			{
				Capability: otogi.Capability{
					Name:        "sleep-command-handler",
					Description: "restricts a user's chat permissions for a specified duration",
					Interest: otogi.InterestSet{
						Kinds:          []otogi.EventKind{otogi.EventKindCommandReceived},
						RequireCommand: true,
						CommandNames:   []string{sleepCommandName},
						RequireArticle: true,
					},
					RequiredServices: []string{
						otogi.ServiceSinkDispatcher,
						otogi.ServiceModerationDispatcher,
					},
				},
				Subscription: otogi.NewDefaultSubscriptionSpec("sleep-sleep-commands"),
				Handler:      m.handleSleep,
			},
			{
				Capability: otogi.Capability{
					Name:        "wake-command-handler",
					Description: "restores a user's chat permissions using a wake code",
					Interest: otogi.InterestSet{
						Kinds:          []otogi.EventKind{otogi.EventKindCommandReceived},
						RequireCommand: true,
						CommandNames:   []string{wakeCommandName},
						RequireArticle: true,
					},
					RequiredServices: []string{
						otogi.ServiceSinkDispatcher,
						otogi.ServiceModerationDispatcher,
					},
				},
				Subscription: otogi.NewDefaultSubscriptionSpec("sleep-wake-commands"),
				Handler:      m.handleWake,
			},
		},
		Commands: []otogi.CommandSpec{
			{
				Prefix:      otogi.CommandPrefixOrdinary,
				Name:        sleepCommandName,
				Description: "restrict your chat permissions for a specified duration (1s–12h)",
			},
			{
				Prefix:      otogi.CommandPrefixOrdinary,
				Name:        wakeCommandName,
				Description: "restore your chat permissions using a wake code",
			},
		},
	}
}

// OnRegister resolves outbound dependencies required by this module.
func (m *Module) OnRegister(_ context.Context, runtime otogi.ModuleRuntime) error {
	dispatcher, err := otogi.ResolveAs[otogi.SinkDispatcher](
		runtime.Services(),
		otogi.ServiceSinkDispatcher,
	)
	if err != nil {
		return fmt.Errorf("sleep resolve sink dispatcher: %w", err)
	}
	m.dispatcher = dispatcher

	moderation, err := otogi.ResolveAs[otogi.ModerationDispatcher](
		runtime.Services(),
		otogi.ServiceModerationDispatcher,
	)
	if err != nil {
		return fmt.Errorf("sleep resolve moderation dispatcher: %w", err)
	}
	m.moderation = moderation

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
