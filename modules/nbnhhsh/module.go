package nbnhhsh

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"ex-otogi/pkg/otogi"
)

const (
	primaryCommandName = "nbnhhsh"
	aliasCommandName   = "srh"
)

// Module expands Chinese internet abbreviations via the nbnhhsh API.
type Module struct {
	cfg        config
	dispatcher otogi.SinkDispatcher
	memory     otogi.MemoryService
	client     guessClient
}

// New creates an nbnhhsh module with runtime-loaded configuration.
func New() *Module {
	return &Module{}
}

// Name returns the stable module identifier.
func (m *Module) Name() string {
	return moduleName
}

// Spec declares nbnhhsh command registrations and capability metadata.
func (m *Module) Spec() otogi.ModuleSpec {
	return otogi.ModuleSpec{
		AdditionalCapabilities: []otogi.Capability{
			{
				Name:        "nbnhhsh-command-handler",
				Description: "expands Chinese internet abbreviations for /nbnhhsh and /srh",
				Interest: otogi.InterestSet{
					Kinds:          []otogi.EventKind{otogi.EventKindCommandReceived},
					RequireCommand: true,
					CommandNames: []string{
						primaryCommandName,
						aliasCommandName,
					},
					RequireArticle: true,
				},
				RequiredServices: []string{
					otogi.ServiceSinkDispatcher,
					otogi.ServiceMemory,
				},
			},
		},
		Commands: []otogi.CommandSpec{
			{
				Prefix:      otogi.CommandPrefixOrdinary,
				Name:        primaryCommandName,
				Description: "expand Chinese internet abbreviations from text or a replied message",
			},
			{
				Prefix:      otogi.CommandPrefixOrdinary,
				Name:        aliasCommandName,
				Description: "alias of /nbnhhsh",
			},
		},
	}
}

// OnRegister loads configuration, resolves dependencies, and subscribes the command handler.
func (m *Module) OnRegister(ctx context.Context, runtime otogi.ModuleRuntime) error {
	cfg, err := loadConfig(runtime.Config())
	if err != nil {
		return fmt.Errorf("nbnhhsh load config: %w", err)
	}

	dispatcher, err := otogi.ResolveAs[otogi.SinkDispatcher](
		runtime.Services(),
		otogi.ServiceSinkDispatcher,
	)
	if err != nil {
		return fmt.Errorf("nbnhhsh resolve sink dispatcher: %w", err)
	}

	memoryService, err := otogi.ResolveAs[otogi.MemoryService](
		runtime.Services(),
		otogi.ServiceMemory,
	)
	if err != nil {
		return fmt.Errorf("nbnhhsh resolve memory service: %w", err)
	}

	if m.client == nil {
		m.client = newHTTPGuessClient(cfg.APIURL, cfg.RequestTimeout)
	}
	m.cfg = cfg
	m.dispatcher = dispatcher
	m.memory = memoryService

	if _, err := runtime.Subscribe(ctx, otogi.InterestSet{
		Kinds:          []otogi.EventKind{otogi.EventKindCommandReceived},
		RequireCommand: true,
		CommandNames: []string{
			primaryCommandName,
			aliasCommandName,
		},
		RequireArticle: true,
	}, otogi.SubscriptionSpec{
		Name:           "nbnhhsh-commands",
		HandlerTimeout: cfg.RequestTimeout + subscriptionTimeoutGrace,
	}, m.handleCommand); err != nil {
		return fmt.Errorf("nbnhhsh subscribe: %w", err)
	}

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
	if !isSupportedCommand(event.Command.Name) {
		return nil
	}
	if m.dispatcher == nil {
		return fmt.Errorf("nbnhhsh handle command: sink dispatcher not configured")
	}
	if m.memory == nil {
		return fmt.Errorf("nbnhhsh handle command: memory service not configured")
	}
	if m.client == nil {
		return fmt.Errorf("nbnhhsh handle command: guess client not configured")
	}

	querySource, reply := m.resolveQuerySource(ctx, event)
	if reply.sent {
		return reply.err
	}

	tokens := extractAbbreviations(querySource)
	if len(tokens) == 0 {
		return m.reply(ctx, event, plainMessage(noAbbreviationMessage))
	}

	results, err := m.client.Guess(ctx, strings.Join(tokens, ","))
	if err != nil {
		message := invalidDataMessage
		if isTemporaryClientError(err) {
			message = timeoutMessage
		}
		if replyErr := m.reply(ctx, event, plainMessage(message)); replyErr != nil {
			return fmt.Errorf(
				"nbnhhsh guess request: %w",
				errors.Join(err, fmt.Errorf("nbnhhsh send fallback reply: %w", replyErr)),
			)
		}
		return fmt.Errorf("nbnhhsh guess request: %w", err)
	}

	return m.reply(ctx, event, renderGuessResults(results))
}

type replyOutcome struct {
	sent bool
	err  error
}

func (m *Module) resolveQuerySource(ctx context.Context, event *otogi.Event) (string, replyOutcome) {
	query := strings.TrimSpace(event.Command.Value)
	if query != "" {
		return query, replyOutcome{}
	}

	if event.Article == nil || strings.TrimSpace(event.Article.ReplyToArticleID) == "" {
		return "", replyOutcome{
			sent: true,
			err:  m.reply(ctx, event, usageMessage()),
		}
	}

	replied, found, err := m.memory.GetReplied(ctx, event)
	if err != nil {
		return "", replyOutcome{
			sent: true,
			err:  fmt.Errorf("nbnhhsh resolve replied message: %w", err),
		}
	}
	if !found {
		return "", replyOutcome{
			sent: true,
			err:  m.reply(ctx, event, plainMessage(replyNotFoundMessage)),
		}
	}

	query = strings.TrimSpace(replied.Article.Text)
	if query == "" {
		return "", replyOutcome{
			sent: true,
			err:  m.reply(ctx, event, plainMessage(replyNoTextMessage)),
		}
	}

	return query, replyOutcome{}
}

func (m *Module) reply(ctx context.Context, event *otogi.Event, message renderedMessage) error {
	target, err := otogi.OutboundTargetFromEvent(event)
	if err != nil {
		return fmt.Errorf("nbnhhsh derive outbound target: %w", err)
	}

	_, err = m.dispatcher.SendMessage(ctx, otogi.SendMessageRequest{
		Target:           target,
		Text:             message.Text,
		Entities:         message.Entities,
		ReplyToMessageID: event.Article.ID,
	})
	if err != nil {
		return fmt.Errorf("nbnhhsh send reply: %w", err)
	}

	return nil
}

func isSupportedCommand(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case primaryCommandName, aliasCommandName:
		return true
	default:
		return false
	}
}

var (
	_ otogi.Module          = (*Module)(nil)
	_ otogi.ModuleRegistrar = (*Module)(nil)
)
