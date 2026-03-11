package whoami

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"ex-otogi/pkg/otogi"
)

const commandName = "whoami"

// Module replies with the caller's user ID, chat ID, and related identity
// metadata when it receives a "~whoami" system command.
type Module struct {
	dispatcher otogi.SinkDispatcher
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
func (m *Module) Spec() otogi.ModuleSpec {
	return otogi.ModuleSpec{
		Handlers: []otogi.ModuleHandler{
			{
				Capability: otogi.Capability{
					Name:        "whoami-command-handler",
					Description: "reports caller identity and conversation metadata for ~whoami",
					Interest: otogi.InterestSet{
						Kinds:          []otogi.EventKind{otogi.EventKindSystemCommandReceived},
						RequireCommand: true,
						CommandNames:   []string{commandName},
						RequireArticle: true,
					},
					RequiredServices: []string{otogi.ServiceSinkDispatcher},
				},
				Subscription: otogi.NewDefaultSubscriptionSpec("whoami-commands"),
				Handler:      m.handleCommand,
			},
		},
		Commands: []otogi.CommandSpec{
			{
				Prefix:      otogi.CommandPrefixSystem,
				Name:        commandName,
				Description: "show caller user ID, chat ID, and identity metadata",
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

func (m *Module) handleCommand(ctx context.Context, event *otogi.Event) error {
	if event == nil || event.Command == nil || event.Article == nil {
		return nil
	}
	if event.Kind != otogi.EventKindSystemCommandReceived {
		return nil
	}
	if event.Command.Name != commandName {
		return nil
	}
	if m.dispatcher == nil {
		return fmt.Errorf("whoami handle command: outbound dispatcher not configured")
	}

	body := formatIdentity(event)

	target, err := otogi.OutboundTargetFromEvent(event)
	if err != nil {
		return fmt.Errorf("whoami derive outbound target: %w", err)
	}
	_, err = m.dispatcher.SendMessage(ctx, otogi.SendMessageRequest{
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

func formatIdentity(event *otogi.Event) string {
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

func preformattedEntity(text string) []otogi.TextEntity {
	if text == "" {
		return nil
	}

	return []otogi.TextEntity{
		{
			Type:   otogi.TextEntityTypePre,
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
	_ otogi.Module          = (*Module)(nil)
	_ otogi.ModuleRegistrar = (*Module)(nil)
)
