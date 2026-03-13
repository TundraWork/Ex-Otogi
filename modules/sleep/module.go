package sleep

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"ex-otogi/pkg/otogi/core"
	"ex-otogi/pkg/otogi/platform"
)

const (
	sleepCommandName = "sleep"
	wakeCommandName  = "wake"
)

// fileConfig is the JSON layout for sleep module configuration.
type fileConfig struct {
	// SigningKey is a base64url-encoded signing key for wake codes.
	SigningKey string `json:"signing_key"`
}

// Module provides user self-mute functionality via /sleep and /wake commands.
//
// When a user invokes /sleep <duration>, the module restricts their chat
// permissions for the specified duration. A wake code is posted in the same
// conversation so the user can restore permissions early with /wake <code>.
type Module struct {
	dispatcher  platform.SinkDispatcher
	moderation  platform.ModerationDispatcher
	codeManager *codeManager
}

// New creates a sleep module. Configuration is loaded from the
// ConfigRegistry during OnRegister.
func New() *Module {
	return &Module{}
}

// Name returns the stable module identifier.
func (m *Module) Name() string {
	return "sleep"
}

// Spec declares interest in received sleep and wake command events.
func (m *Module) Spec() core.ModuleSpec {
	return core.ModuleSpec{
		Handlers: []core.ModuleHandler{
			{
				Capability: core.Capability{
					Name:        "sleep-command-handler",
					Description: "restricts a user's chat permissions for a specified duration",
					Interest: core.InterestSet{
						Kinds:          []platform.EventKind{platform.EventKindCommandReceived},
						RequireCommand: true,
						CommandNames:   []string{sleepCommandName},
						RequireArticle: true,
					},
					RequiredServices: []string{
						platform.ServiceSinkDispatcher,
						platform.ServiceModerationDispatcher,
					},
				},
				Subscription: core.NewDefaultSubscriptionSpec("sleep-sleep-commands"),
				Handler:      m.handleSleep,
			},
			{
				Capability: core.Capability{
					Name:        "wake-command-handler",
					Description: "restores a user's chat permissions using a wake code",
					Interest: core.InterestSet{
						Kinds:          []platform.EventKind{platform.EventKindCommandReceived},
						RequireCommand: true,
						CommandNames:   []string{wakeCommandName},
						RequireArticle: true,
					},
					RequiredServices: []string{
						platform.ServiceSinkDispatcher,
						platform.ServiceModerationDispatcher,
					},
				},
				Subscription: core.NewDefaultSubscriptionSpec("sleep-wake-commands"),
				Handler:      m.handleWake,
			},
		},
		Commands: []platform.CommandSpec{
			{
				Prefix:      platform.CommandPrefixOrdinary,
				Name:        sleepCommandName,
				Description: "restrict your chat permissions for a specified duration (1s–12h)",
			},
			{
				Prefix:      platform.CommandPrefixOrdinary,
				Name:        wakeCommandName,
				Description: "restore your chat permissions using a wake code",
			},
		},
	}
}

// OnRegister loads configuration and resolves outbound dependencies.
func (m *Module) OnRegister(_ context.Context, runtime core.ModuleRuntime) error {
	signingKey, err := loadSigningKey(runtime.Config())
	if err != nil {
		return fmt.Errorf("sleep load config: %w", err)
	}

	cm, err := newCodeManager(signingKey)
	if err != nil {
		return fmt.Errorf("sleep init code manager: %w", err)
	}
	m.codeManager = cm

	dispatcher, err := core.ResolveAs[platform.SinkDispatcher](
		runtime.Services(),
		platform.ServiceSinkDispatcher,
	)
	if err != nil {
		return fmt.Errorf("sleep resolve sink dispatcher: %w", err)
	}
	m.dispatcher = dispatcher

	moderation, err := core.ResolveAs[platform.ModerationDispatcher](
		runtime.Services(),
		platform.ServiceModerationDispatcher,
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

// loadSigningKey reads and decodes the base64url signing key from module config.
func loadSigningKey(configs core.ConfigRegistry) ([]byte, error) {
	cfg, err := core.ParseModuleConfig[fileConfig](configs, "sleep")
	if err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	trimmed := strings.TrimSpace(cfg.SigningKey)
	if trimmed == "" {
		return nil, fmt.Errorf("signing_key is required")
	}

	signingKey, err := base64.RawURLEncoding.DecodeString(trimmed)
	if err != nil {
		return nil, fmt.Errorf("signing_key: decode base64url: %w", err)
	}
	if len(signingKey) < 32 {
		return nil, fmt.Errorf("signing_key: must decode to at least 32 bytes")
	}

	return append([]byte(nil), signingKey...), nil
}
