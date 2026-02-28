package llmchat

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"ex-otogi/pkg/otogi"
)

const llmchatHandlerTimeoutGrace = 5 * time.Second

const placeholderFailureMessage = "Sorry, I couldn't generate a response right now."

const serviceLogger = "logger"

// Module provides keyword-triggered LLM chat behavior.
type Module struct {
	cfg Config

	dispatcher otogi.SinkDispatcher
	memory     otogi.MemoryService
	providers  map[string]otogi.LLMProvider

	providerRegistry otogi.LLMProviderRegistry
	logger           *slog.Logger
	clock            func() time.Time
	sleep            func(context.Context, time.Duration) error
}

// Option mutates one llmchat module construction input.
type Option func(*Module)

// New creates one llmchat module instance.
func New(cfg Config, options ...Option) (*Module, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("new llmchat module: %w", err)
	}

	module := &Module{
		cfg:       cloneConfig(cfg),
		providers: make(map[string]otogi.LLMProvider),
		logger:    slog.Default(),
		clock:     time.Now,
		sleep:     sleepWithContext,
	}
	for _, option := range options {
		option(module)
	}

	return module, nil
}

// Name returns the stable module identifier.
func (m *Module) Name() string {
	return "llmchat"
}

// Spec declares llmchat article trigger capabilities.
func (m *Module) Spec() otogi.ModuleSpec {
	handlerTimeout := m.cfg.RequestTimeout + llmchatHandlerTimeoutGrace

	return otogi.ModuleSpec{
		Handlers: []otogi.ModuleHandler{
			{
				Capability: otogi.Capability{
					Name:        "llm-chat-trigger",
					Description: "handles keyword-triggered llm chat from article events",
					Interest: otogi.InterestSet{
						Kinds:          []otogi.EventKind{otogi.EventKindArticleCreated},
						RequireArticle: true,
					},
					RequiredServices: []string{
						otogi.ServiceSinkDispatcher,
						otogi.ServiceMemory,
						otogi.ServiceLLMProviderRegistry,
					},
				},
				Subscription: otogi.SubscriptionSpec{
					Name:           "llmchat-articles",
					HandlerTimeout: handlerTimeout,
				},
				Handler: m.handleArticle,
			},
		},
	}
}

// OnRegister resolves module dependencies.
func (m *Module) OnRegister(_ context.Context, runtime otogi.ModuleRuntime) error {
	logger, err := otogi.ResolveAs[*slog.Logger](runtime.Services(), serviceLogger)
	switch {
	case err == nil:
		m.logger = logger
	case errors.Is(err, otogi.ErrServiceNotFound):
	default:
		return fmt.Errorf("llmchat resolve logger: %w", err)
	}

	dispatcher, err := otogi.ResolveAs[otogi.SinkDispatcher](
		runtime.Services(),
		otogi.ServiceSinkDispatcher,
	)
	if err != nil {
		return fmt.Errorf("llmchat resolve sink dispatcher: %w", err)
	}

	memoryService, err := otogi.ResolveAs[otogi.MemoryService](
		runtime.Services(),
		otogi.ServiceMemory,
	)
	if err != nil {
		return fmt.Errorf("llmchat resolve memory service: %w", err)
	}

	registry, err := otogi.ResolveAs[otogi.LLMProviderRegistry](
		runtime.Services(),
		otogi.ServiceLLMProviderRegistry,
	)
	if err != nil {
		return fmt.Errorf("llmchat resolve provider registry: %w", err)
	}

	resolvedProviders := make(map[string]otogi.LLMProvider)
	for _, agent := range m.cfg.Agents {
		providerName := strings.TrimSpace(agent.Provider)
		if providerName == "" {
			return fmt.Errorf("llmchat register agent %s: empty provider", agent.Name)
		}
		if _, exists := resolvedProviders[providerName]; exists {
			continue
		}

		provider, err := registry.Resolve(providerName)
		if err != nil {
			return fmt.Errorf("llmchat resolve provider %s for agent %s: %w", providerName, agent.Name, err)
		}
		resolvedProviders[providerName] = provider
	}

	m.dispatcher = dispatcher
	m.memory = memoryService
	m.providerRegistry = registry
	m.providers = resolvedProviders

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

func (m *Module) handleArticle(ctx context.Context, event *otogi.Event) error {
	if event == nil || event.Article == nil {
		return nil
	}
	if event.Kind != otogi.EventKindArticleCreated {
		return nil
	}
	if event.Actor.IsBot {
		return nil
	}
	if strings.TrimSpace(event.Article.Text) == "" {
		return nil
	}
	if m.dispatcher == nil {
		return fmt.Errorf("llmchat handle article: sink dispatcher not configured")
	}
	if m.memory == nil {
		return fmt.Errorf("llmchat handle article: memory service not configured")
	}

	agent, prompt, matched := m.matchTriggeredAgent(event.Article.Text)
	if !matched {
		return nil
	}

	provider, exists := m.providers[strings.TrimSpace(agent.Provider)]
	if !exists || provider == nil {
		return fmt.Errorf("llmchat handle article: provider %s for agent %s is not available", agent.Provider, agent.Name)
	}

	target, err := otogi.OutboundTargetFromEvent(event)
	if err != nil {
		return fmt.Errorf("llmchat derive outbound target for agent %s: %w", agent.Name, err)
	}

	placeholder, err := m.dispatcher.SendMessage(ctx, otogi.SendMessageRequest{
		Target:           target,
		Text:             defaultThinkingPlaceholder,
		ReplyToMessageID: event.Article.ID,
	})
	if err != nil {
		return fmt.Errorf("llmchat send placeholder for agent %s: %w", agent.Name, err)
	}
	if err := validateHandlerDeadlineBudget(ctx, agent.RequestTimeout); err != nil {
		preflightErr := fmt.Errorf("llmchat preflight for agent %s: %w", agent.Name, err)
		return m.finalizePlaceholderFailure(ctx, target, placeholder.ID, preflightErr)
	}

	reqCtx := ctx
	cancel := func() {}
	if agent.RequestTimeout > 0 {
		timeoutCtx, timeoutCancel := context.WithTimeout(ctx, agent.RequestTimeout)
		reqCtx = timeoutCtx
		cancel = timeoutCancel
	}
	defer cancel()

	req, err := m.buildGenerateRequest(reqCtx, event, agent, prompt)
	if err != nil {
		buildErr := fmt.Errorf("llmchat build request for agent %s: %w", agent.Name, err)
		return m.finalizePlaceholderFailure(ctx, target, placeholder.ID, buildErr)
	}

	if err := m.streamProviderReply(reqCtx, ctx, target, placeholder.ID, provider, req); err != nil {
		streamErr := fmt.Errorf("llmchat stream response for agent %s: %w", agent.Name, err)
		return m.finalizePlaceholderFailure(ctx, target, placeholder.ID, streamErr)
	}

	return nil
}

func validateHandlerDeadlineBudget(ctx context.Context, requestTimeout time.Duration) error {
	if requestTimeout <= 0 {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("handler context canceled before llm request: %w", err)
	}

	deadline, hasDeadline := ctx.Deadline()
	if !hasDeadline {
		return nil
	}

	remaining := time.Until(deadline)
	if remaining >= requestTimeout {
		return nil
	}
	if remaining < 0 {
		remaining = 0
	}

	return fmt.Errorf(
		"insufficient handler deadline budget: remaining=%s request_timeout=%s; "+
			"configure llmchat subscription handler timeout to request_timeout + %s grace "+
			"or increase kernel.module_handler_timeout",
		remaining.Round(time.Millisecond),
		requestTimeout,
		llmchatHandlerTimeoutGrace,
	)
}

func (m *Module) finalizePlaceholderFailure(
	ctx context.Context,
	target otogi.OutboundTarget,
	placeholderMessageID string,
	handlerErr error,
) error {
	if handlerErr == nil {
		return nil
	}
	if strings.TrimSpace(placeholderMessageID) == "" {
		return handlerErr
	}
	if err := ctx.Err(); err != nil {
		return handlerErr
	}

	editErr := m.retryEditMessage(ctx, otogi.EditMessageRequest{
		Target:    target,
		MessageID: placeholderMessageID,
		Text:      placeholderFailureMessage,
	})
	if editErr == nil {
		return handlerErr
	}

	return fmt.Errorf(
		"llmchat finalize placeholder failure: %w",
		errors.Join(
			handlerErr,
			fmt.Errorf("llmchat finalize placeholder failure %s: %w", placeholderMessageID, editErr),
		),
	)
}

func (m *Module) matchTriggeredAgent(text string) (agent Agent, prompt string, matched bool) {
	longest := -1
	for _, candidate := range m.cfg.Agents {
		candidatePrompt, ok := matchAgentTrigger(text, candidate.Name)
		if !ok {
			continue
		}
		candidateLen := len([]rune(strings.TrimSpace(candidate.Name)))
		if candidateLen > longest {
			agent = candidate
			prompt = candidatePrompt
			longest = candidateLen
			matched = true
		}
	}

	return agent, prompt, matched
}

func (m *Module) now() time.Time {
	if m.clock == nil {
		return time.Now().UTC()
	}

	return m.clock().UTC()
}

var (
	_ otogi.Module          = (*Module)(nil)
	_ otogi.ModuleRegistrar = (*Module)(nil)
)
