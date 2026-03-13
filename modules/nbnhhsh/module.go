package nbnhhsh

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"ex-otogi/pkg/otogi/core"
	"ex-otogi/pkg/otogi/platform"
)

const (
	primaryCommandName = "nbnhhsh"
	aliasCommandName   = "srh"
)

// Module expands Chinese internet abbreviations via the nbnhhsh API.
type Module struct {
	cfg        config
	dispatcher platform.SinkDispatcher
	memory     core.MemoryService
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
func (m *Module) Spec() core.ModuleSpec {
	return core.ModuleSpec{
		AdditionalCapabilities: []core.Capability{
			{
				Name:        "nbnhhsh-command-handler",
				Description: "expands Chinese internet abbreviations for /nbnhhsh and /srh",
				Interest: core.InterestSet{
					Kinds:          []platform.EventKind{platform.EventKindCommandReceived},
					RequireCommand: true,
					CommandNames: []string{
						primaryCommandName,
						aliasCommandName,
					},
					RequireArticle: true,
				},
				RequiredServices: []string{
					platform.ServiceSinkDispatcher,
					core.ServiceMemory,
				},
			},
		},
		Commands: []platform.CommandSpec{
			{
				Prefix:      platform.CommandPrefixOrdinary,
				Name:        primaryCommandName,
				Description: "expand Chinese internet abbreviations from text or a replied article",
			},
			{
				Prefix:      platform.CommandPrefixOrdinary,
				Name:        aliasCommandName,
				Description: "alias of /nbnhhsh",
			},
		},
	}
}

// OnRegister loads configuration, resolves dependencies, and subscribes the command handler.
func (m *Module) OnRegister(ctx context.Context, runtime core.ModuleRuntime) error {
	cfg, err := loadConfig(runtime.Config())
	if err != nil {
		return fmt.Errorf("nbnhhsh load config: %w", err)
	}

	dispatcher, err := core.ResolveAs[platform.SinkDispatcher](
		runtime.Services(),
		platform.ServiceSinkDispatcher,
	)
	if err != nil {
		return fmt.Errorf("nbnhhsh resolve sink dispatcher: %w", err)
	}

	memoryService, err := core.ResolveAs[core.MemoryService](
		runtime.Services(),
		core.ServiceMemory,
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

	if _, err := runtime.Subscribe(ctx, core.InterestSet{
		Kinds:          []platform.EventKind{platform.EventKindCommandReceived},
		RequireCommand: true,
		CommandNames: []string{
			primaryCommandName,
			aliasCommandName,
		},
		RequireArticle: true,
	}, core.SubscriptionSpec{
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

func (m *Module) handleCommand(ctx context.Context, event *platform.Event) error {
	if event == nil || event.Command == nil || event.Article == nil {
		return nil
	}
	if event.Kind != platform.EventKindCommandReceived {
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

func (m *Module) resolveQuerySource(ctx context.Context, event *platform.Event) (string, replyOutcome) {
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
			err:  fmt.Errorf("nbnhhsh resolve replied article: %w", err),
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

func (m *Module) reply(ctx context.Context, event *platform.Event, message renderedMessage) error {
	target, err := platform.OutboundTargetFromEvent(event)
	if err != nil {
		return fmt.Errorf("nbnhhsh derive outbound target: %w", err)
	}

	_, err = m.dispatcher.SendMessage(ctx, platform.SendMessageRequest{
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
	_ core.Module          = (*Module)(nil)
	_ core.ModuleRegistrar = (*Module)(nil)
)
