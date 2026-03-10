package quotehelper

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"ex-otogi/pkg/otogi"
)

const (
	moduleName = "quotehelper"

	youCommandName = "you"
	weCommandName  = "we"

	substituteFailureMessage = "命令执行失败，可能是执行超时或进程无响应。"
)

// Module applies reply-based text transforms.
type Module struct {
	dispatcher otogi.SinkDispatcher
	memory     otogi.MemoryService
	runner     substitutionRunner
}

// New creates a quotehelper module with default runtime dependencies.
func New() *Module {
	return &Module{}
}

// Name returns the stable module identifier.
func (m *Module) Name() string {
	return moduleName
}

// Spec declares reply transform handlers and public commands.
func (m *Module) Spec() otogi.ModuleSpec {
	return otogi.ModuleSpec{
		Handlers: []otogi.ModuleHandler{
			{
				Capability: otogi.Capability{
					Name:        "quotehelper-you-command-handler",
					Description: "rewrites first-person pronouns in a replied message for /you",
					Interest: otogi.InterestSet{
						Kinds:          []otogi.EventKind{otogi.EventKindCommandReceived},
						RequireArticle: true,
						RequireCommand: true,
						CommandNames:   []string{youCommandName},
					},
					RequiredServices: []string{
						otogi.ServiceSinkDispatcher,
						otogi.ServiceMemory,
					},
				},
				Subscription: otogi.NewDefaultSubscriptionSpec("quotehelper-you-commands"),
				Handler:      m.handleYouCommand,
			},
			{
				Capability: otogi.Capability{
					Name:        "quotehelper-we-command-handler",
					Description: "rewrites replied message pronouns into collective language for /we",
					Interest: otogi.InterestSet{
						Kinds:          []otogi.EventKind{otogi.EventKindCommandReceived},
						RequireArticle: true,
						RequireCommand: true,
						CommandNames:   []string{weCommandName},
					},
					RequiredServices: []string{
						otogi.ServiceSinkDispatcher,
						otogi.ServiceMemory,
					},
				},
				Subscription: otogi.NewDefaultSubscriptionSpec("quotehelper-we-commands"),
				Handler:      m.handleWeCommand,
			},
			{
				Capability: otogi.Capability{
					Name:        "quotehelper-sed-reply-handler",
					Description: "applies sed-style substitutions to replied message text",
					Interest: otogi.InterestSet{
						Kinds:          []otogi.EventKind{otogi.EventKindArticleCreated},
						RequireArticle: true,
					},
					RequiredServices: []string{
						otogi.ServiceSinkDispatcher,
						otogi.ServiceMemory,
					},
				},
				Subscription: otogi.NewDefaultSubscriptionSpec("quotehelper-sed-replies"),
				Handler:      m.handleSubstituteArticle,
			},
		},
		Commands: []otogi.CommandSpec{
			{
				Prefix:      otogi.CommandPrefixOrdinary,
				Name:        youCommandName,
				Description: "rewrite the replied message by swapping you/me pronouns",
			},
			{
				Prefix:      otogi.CommandPrefixOrdinary,
				Name:        weCommandName,
				Description: "rewrite the replied message into collective language",
			},
		},
	}
}

// OnRegister resolves module dependencies.
func (m *Module) OnRegister(_ context.Context, runtime otogi.ModuleRuntime) error {
	dispatcher, err := otogi.ResolveAs[otogi.SinkDispatcher](
		runtime.Services(),
		otogi.ServiceSinkDispatcher,
	)
	if err != nil {
		return fmt.Errorf("quotehelper resolve sink dispatcher: %w", err)
	}

	memoryService, err := otogi.ResolveAs[otogi.MemoryService](
		runtime.Services(),
		otogi.ServiceMemory,
	)
	if err != nil {
		return fmt.Errorf("quotehelper resolve memory service: %w", err)
	}

	m.dispatcher = dispatcher
	m.memory = memoryService
	if m.runner == nil {
		m.runner = newExecSubstitutionRunner()
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

func (m *Module) handleYouCommand(ctx context.Context, event *otogi.Event) error {
	return m.handleReplyCommand(ctx, event, youCommandName, transformForYou)
}

func (m *Module) handleWeCommand(ctx context.Context, event *otogi.Event) error {
	return m.handleReplyCommand(ctx, event, weCommandName, transformForWe)
}

func (m *Module) handleReplyCommand(
	ctx context.Context,
	event *otogi.Event,
	commandName string,
	transform func(string) string,
) error {
	if event == nil || event.Kind != otogi.EventKindCommandReceived || event.Command == nil || event.Article == nil {
		return nil
	}
	if event.Command.Name != commandName {
		return nil
	}
	if strings.TrimSpace(event.Command.Value) != "" {
		return nil
	}
	if m.dispatcher == nil {
		return fmt.Errorf("quotehelper handle %s: sink dispatcher not configured", commandName)
	}
	if m.memory == nil {
		return fmt.Errorf("quotehelper handle %s: memory service not configured", commandName)
	}

	replyText, ok, err := m.resolveReplyText(ctx, event)
	if err != nil {
		return fmt.Errorf("quotehelper handle %s resolve reply text: %w", commandName, err)
	}
	if !ok {
		return nil
	}

	return m.reply(ctx, event, transform(replyText))
}

func (m *Module) handleSubstituteArticle(ctx context.Context, event *otogi.Event) error {
	if event == nil || event.Kind != otogi.EventKindArticleCreated || event.Article == nil {
		return nil
	}
	if m.dispatcher == nil {
		return fmt.Errorf("quotehelper handle substitute: sink dispatcher not configured")
	}
	if m.memory == nil {
		return fmt.Errorf("quotehelper handle substitute: memory service not configured")
	}
	if m.runner == nil {
		return fmt.Errorf("quotehelper handle substitute: substitution runner not configured")
	}

	expression, matched := parseSubstitutionExpression(event.Article.Text)
	if !matched {
		return nil
	}

	replyText, ok, err := m.resolveReplyText(ctx, event)
	if err != nil {
		return fmt.Errorf("quotehelper handle substitute resolve reply text: %w", err)
	}
	if !ok {
		return nil
	}

	result, err := m.runner.Run(ctx, expression, replyText)
	if err != nil {
		if replyErr := m.reply(ctx, event, substituteFailureMessage); replyErr != nil {
			return fmt.Errorf(
				"quotehelper run substitute expression: %w",
				errors.Join(err, fmt.Errorf("send substitute failure reply: %w", replyErr)),
			)
		}
		return fmt.Errorf("quotehelper run substitute expression: %w", err)
	}

	return m.reply(ctx, event, result)
}

func (m *Module) resolveReplyText(ctx context.Context, event *otogi.Event) (string, bool, error) {
	if event == nil || event.Article == nil {
		return "", false, nil
	}
	if strings.TrimSpace(event.Article.ReplyToArticleID) == "" {
		return "", false, nil
	}

	replied, found, err := m.memory.GetReplied(ctx, event)
	if err != nil {
		return "", false, fmt.Errorf("get replied message: %w", err)
	}
	if !found {
		return "", false, nil
	}

	text := strings.TrimSpace(replied.Article.Text)
	if text == "" {
		return "", false, nil
	}

	return text, true, nil
}

func (m *Module) reply(ctx context.Context, event *otogi.Event, text string) error {
	if strings.TrimSpace(text) == "" {
		return nil
	}

	target, err := otogi.OutboundTargetFromEvent(event)
	if err != nil {
		return fmt.Errorf("quotehelper derive outbound target: %w", err)
	}

	_, err = m.dispatcher.SendMessage(ctx, otogi.SendMessageRequest{
		Target:           target,
		Text:             text,
		ReplyToMessageID: event.Article.ID,
	})
	if err != nil {
		return fmt.Errorf("quotehelper send reply: %w", err)
	}

	return nil
}

var (
	_ otogi.Module          = (*Module)(nil)
	_ otogi.ModuleRegistrar = (*Module)(nil)
)
