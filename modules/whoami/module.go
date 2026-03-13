package whoami

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"ex-otogi/pkg/otogi/core"
	"ex-otogi/pkg/otogi/platform"
)

const commandName = "whoami"

// Module replies with the caller's user ID, chat ID, and related identity
// metadata when it receives a "~whoami" system command.
type Module struct {
	dispatcher platform.SinkDispatcher
}

// New creates a whoami module with default configuration.
func New() *Module {
	return &Module{}
}

// Name returns the stable module identifier.
func (m *Module) Name() string {
	return "whoami"
}

// Spec declares interest in the ~whoami system command event.
func (m *Module) Spec() core.ModuleSpec {
	return core.ModuleSpec{
		Handlers: []core.ModuleHandler{
			{
				Capability: core.Capability{
					Name:        "whoami-command-handler",
					Description: "reports caller identity and conversation metadata for ~whoami",
					Interest: core.InterestSet{
						Kinds:          []platform.EventKind{platform.EventKindSystemCommandReceived},
						RequireCommand: true,
						CommandNames:   []string{commandName},
						RequireArticle: true,
					},
					RequiredServices: []string{platform.ServiceSinkDispatcher},
				},
				Subscription: core.NewDefaultSubscriptionSpec("whoami-commands"),
				Handler:      m.handleCommand,
			},
		},
		Commands: []platform.CommandSpec{
			{
				Prefix:      platform.CommandPrefixSystem,
				Name:        commandName,
				Description: "show caller user ID, chat ID, and identity metadata",
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
		return fmt.Errorf("whoami resolve outbound dispatcher: %w", err)
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
	if event.Kind != platform.EventKindSystemCommandReceived {
		return nil
	}
	if event.Command.Name != commandName {
		return nil
	}
	if m.dispatcher == nil {
		return fmt.Errorf("whoami handle command: outbound dispatcher not configured")
	}

	body := formatIdentity(event)

	target, err := platform.OutboundTargetFromEvent(event)
	if err != nil {
		return fmt.Errorf("whoami derive outbound target: %w", err)
	}
	_, err = m.dispatcher.SendMessage(ctx, platform.SendMessageRequest{
		Target:           target,
		Text:             body,
		Entities:         preformattedEntity(body),
		ReplyToMessageID: event.Article.ID,
	})
	if err != nil {
		return fmt.Errorf("whoami send identity message: %w", err)
	}

	return nil
}

func formatIdentity(event *platform.Event) string {
	var lines []string

	lines = append(lines, fmt.Sprintf("source_platform: %s", valueOrDash(string(event.Source.Platform))))

	lines = append(lines, fmt.Sprintf("user_id: %s", qualifiedIdentityKey(event.Source.ID, event.Actor.ID)))
	if event.Actor.Username != "" {
		lines = append(lines, fmt.Sprintf("username: @%s", event.Actor.Username))
	}
	if event.Actor.DisplayName != "" {
		lines = append(lines, fmt.Sprintf("display_name: %s", event.Actor.DisplayName))
	}

	lines = append(lines, fmt.Sprintf("chat_id: %s", qualifiedIdentityKey(event.Source.ID, event.Conversation.ID)))
	if event.Conversation.Type != "" {
		lines = append(lines, fmt.Sprintf("chat_type: %s", event.Conversation.Type))
	}
	if event.Conversation.Title != "" {
		lines = append(lines, fmt.Sprintf("chat_title: %s", event.Conversation.Title))
	}

	return strings.Join(lines, "\n")
}

func preformattedEntity(text string) []platform.TextEntity {
	if text == "" {
		return nil
	}

	return []platform.TextEntity{
		{
			Type:   platform.TextEntityTypePre,
			Offset: 0,
			Length: utf8.RuneCountInString(text),
		},
	}
}

func qualifiedIdentityKey(sourceID string, identityID string) string {
	if identityID == "" {
		return "-"
	}
	if sourceID == "" {
		return identityID
	}

	return sourceID + "/" + identityID
}

func valueOrDash(value string) string {
	if value == "" {
		return "-"
	}

	return value
}

var (
	_ core.Module          = (*Module)(nil)
	_ core.ModuleRegistrar = (*Module)(nil)
)
