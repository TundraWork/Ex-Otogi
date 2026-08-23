package llmchat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"ex-otogi/pkg/otogi/ai"
	"ex-otogi/pkg/otogi/core"
	panel "ex-otogi/pkg/otogi/management"
	"ex-otogi/pkg/otogi/platform"

	"ex-otogi/pkg/llm"
	llmconfig "ex-otogi/pkg/llm/config"
)

const llmchatHandlerTimeoutGrace = 5 * time.Second

const placeholderFailureMessage = "Sorry, I couldn't generate a response right now."

const serviceLogger = "logger"

// Module provides keyword-triggered LLM chat behavior.
type Module struct {
	cfg Config

	dispatcher      platform.SinkDispatcher
	memory          core.MemoryService
	providers       map[string]ai.LLMProvider
	parser          platform.MarkdownParser
	mediaDownloader platform.MediaDownloader

	providerRegistry  ai.LLMProviderRegistry
	semanticRetriever ai.SemanticRetriever
	logger            *slog.Logger
	recorder          panel.Recorder
	clock             func() time.Time
	sleep             func(context.Context, time.Duration) error
}

// Option mutates one llmchat module construction input.
type Option func(*Module)

// New creates one llmchat module instance. Configuration is loaded from
// the ConfigRegistry during OnRegister.
func New(options ...Option) *Module {
	module := &Module{
		providers: make(map[string]ai.LLMProvider),
		logger:    slog.Default(),
		clock:     time.Now,
		sleep:     sleepWithContext,
	}
	for _, option := range options {
		option(module)
	}

	return module
}

// Name returns the stable module identifier.
func (m *Module) Name() string {
	return "llmchat"
}

// Spec declares llmchat article trigger capabilities.
//
// Handler subscription is registered imperatively in OnRegister after config
// is loaded, so the handler timeout can incorporate the configured request timeout.
func (m *Module) Spec() core.ModuleSpec {
	return core.ModuleSpec{
		AdditionalCapabilities: []core.Capability{
			{
				Name:        "llm-chat-trigger",
				Description: "handles keyword-triggered llm chat from article events",
				Interest: core.InterestSet{
					Kinds:          []platform.EventKind{platform.EventKindArticleCreated},
					RequireArticle: true,
				},
				RequiredServices: []string{
					platform.ServiceSinkDispatcher,
					core.ServiceMemory,
					platform.ServiceMarkdownParser,
				},
			},
		},
	}
}

// OnRegister loads configuration, builds LLM providers, and resolves dependencies.
func (m *Module) OnRegister(ctx context.Context, runtime core.ModuleRuntime) error {
	logger, err := core.ResolveAs[*slog.Logger](runtime.Services(), serviceLogger)
	switch {
	case err == nil:
		m.logger = logger
	case errors.Is(err, core.ErrServiceNotFound):
	default:
		return fmt.Errorf("llmchat resolve logger: %w", err)
	}
	recorder, err := core.ResolveAs[panel.Recorder](runtime.Services(), panel.ServiceRecorder)
	switch {
	case err == nil:
		m.recorder = recorder
	case errors.Is(err, core.ErrServiceNotFound):
	default:
		return fmt.Errorf("llmchat resolve recorder: %w", err)
	}

	llmCfg, err := core.ResolveAs[llmconfig.Config](runtime.Services(), llm.ServiceRuntimeConfig)
	if err != nil {
		return fmt.Errorf("llmchat resolve runtime config: %w", err)
	}
	cfg := toLLMChatConfig(llmCfg)
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("llmchat validate config: %w", err)
	}
	registry, err := core.ResolveAs[ai.LLMProviderRegistry](runtime.Services(), ai.ServiceLLMProviderRegistry)
	if err != nil {
		return fmt.Errorf("llmchat resolve provider registry: %w", err)
	}
	m.cfg = cfg

	dispatcher, err := core.ResolveAs[platform.SinkDispatcher](
		runtime.Services(),
		platform.ServiceSinkDispatcher,
	)
	if err != nil {
		return fmt.Errorf("llmchat resolve sink dispatcher: %w", err)
	}

	memoryService, err := core.ResolveAs[core.MemoryService](
		runtime.Services(),
		core.ServiceMemory,
	)
	if err != nil {
		return fmt.Errorf("llmchat resolve memory service: %w", err)
	}

	markdownParser, err := core.ResolveAs[platform.MarkdownParser](
		runtime.Services(),
		platform.ServiceMarkdownParser,
	)
	if err != nil {
		return fmt.Errorf("llmchat resolve markdown parser: %w", err)
	}
	mediaDownloader, err := core.ResolveAs[platform.MediaDownloader](
		runtime.Services(),
		platform.ServiceMediaDownloader,
	)
	switch {
	case err == nil:
	case errors.Is(err, core.ErrServiceNotFound):
		mediaDownloader = nil
	default:
		return fmt.Errorf("llmchat resolve media downloader: %w", err)
	}
	resolvedProviders := make(map[string]ai.LLMProvider)
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
	for _, agent := range m.cfg.Agents {
		for _, subAgent := range agent.SubAgents {
			subProviderName := strings.TrimSpace(subAgent.Provider)
			if subProviderName == "" || resolvedProviders[subProviderName] != nil {
				continue
			}

			provider, err := registry.Resolve(subProviderName)
			if err != nil {
				return fmt.Errorf("llmchat resolve provider %s for sub-agent %s (agent %s): %w",
					subProviderName, subAgent.Name, agent.Name, err)
			}
			resolvedProviders[subProviderName] = provider
		}
	}

	m.dispatcher = dispatcher
	m.memory = memoryService
	m.parser = markdownParser
	m.mediaDownloader = mediaDownloader
	m.providerRegistry = registry
	m.providers = resolvedProviders

	handlerTimeout := m.cfg.RequestTimeout + llmchatHandlerTimeoutGrace
	if _, err := runtime.Subscribe(ctx, core.InterestSet{
		Kinds:          []platform.EventKind{platform.EventKindArticleCreated},
		RequireArticle: true,
	}, core.SubscriptionSpec{
		Name:           "llmchat-articles",
		HandlerTimeout: handlerTimeout,
	}, m.handleArticle); err != nil {
		return fmt.Errorf("llmchat subscribe: %w", err)
	}
	retriever, err := core.ResolveAs[ai.SemanticRetriever](runtime.Services(), ai.ServiceSemanticRetriever)
	if semanticRetrievalEnabled(m.cfg.Agents) {
		if err != nil {
			return fmt.Errorf("llmchat resolve required semantic retriever: %w", err)
		}
		m.semanticRetriever = retriever
	} else {
		switch {
		case err == nil:
			m.semanticRetriever = retriever
		case errors.Is(err, core.ErrServiceNotFound):
			m.semanticRetriever = nil
		default:
			return fmt.Errorf("llmchat resolve semantic retriever: %w", err)
		}
	}

	return nil
}

func semanticRetrievalEnabled(agents []Agent) bool {
	for _, agent := range agents {
		if agent.MemoryEnabled {
			return true
		}
	}
	return false
}

func toLLMChatConfig(cfg llmconfig.Config) Config {
	agents := make([]Agent, 0, len(cfg.Agents))
	for _, agent := range cfg.Agents {
		agents = append(agents, Agent{
			Name:                 agent.Name,
			Aliases:              cloneStringSlice(agent.Aliases),
			Description:          agent.Description,
			Provider:             agent.Provider,
			Model:                agent.Model,
			SystemPromptTemplate: agent.SystemPromptTemplate,
			TemplateVariables:    cloneStringMap(agent.TemplateVariables),
			MaxOutputTokens:      agent.MaxOutputTokens,
			Temperature:          agent.Temperature,
			RequestTimeout:       agent.RequestTimeout,
			RequestMetadata:      cloneStringMap(agent.RequestMetadata),
			ContextPolicy: ContextPolicy{
				ReplyChainMaxMessages:  agent.ContextPolicy.ReplyChainMaxMessages,
				LeadingContextMessages: agent.ContextPolicy.LeadingContextMessages,
				LeadingContextMaxAge:   agent.ContextPolicy.LeadingContextMaxAge,
				MaxContextRunes:        agent.ContextPolicy.MaxContextRunes,
				MaxMessageRunes:        agent.ContextPolicy.MaxMessageRunes,
				QuoteReplyDepth:        agent.ContextPolicy.QuoteReplyDepth,
			},
			MemoryEnabled: agent.MemoryEnabled,
			ImageInputs: ImageInputPolicy{
				Enabled:       agent.ImageInputs.Enabled,
				MaxImages:     agent.ImageInputs.MaxImages,
				MaxImageBytes: agent.ImageInputs.MaxImageBytes,
				MaxTotalBytes: agent.ImageInputs.MaxTotalBytes,
				Detail:        agent.ImageInputs.Detail,
			},
			SubAgents: toSubAgentConfigs(agent.SubAgents),
		})
	}

	return Config{
		RequestTimeout: cfg.RequestTimeout,
		Agents:         agents,
	}
}

func toSubAgentConfigs(configs []llmconfig.SubAgentConfig) []SubAgentConfig {
	if len(configs) == 0 {
		return nil
	}

	result := make([]SubAgentConfig, 0, len(configs))
	for _, cfg := range configs {
		result = append(result, SubAgentConfig{
			Name:            cfg.Name,
			Description:     cfg.Description,
			Provider:        cfg.Provider,
			Model:           cfg.Model,
			SystemPrompt:    cfg.SystemPrompt,
			MaxOutputTokens: cfg.MaxOutputTokens,
			Temperature:     cfg.Temperature,
			RequestMetadata: cloneStringMap(cfg.RequestMetadata),
			Parameters:      append(json.RawMessage(nil), cfg.Parameters...),
			PromptTemplate:  cfg.PromptTemplate,
		})
	}

	return result
}

// OnStart starts the module lifecycle.
func (m *Module) OnStart(_ context.Context) error {
	return nil
}

// OnShutdown stops the module lifecycle.
func (m *Module) OnShutdown(_ context.Context) error {
	return nil
}

func (m *Module) handleArticle(ctx context.Context, event *platform.Event) (handlerErr error) {
	if m.recorder != nil {
		ctx = panel.WithRecorder(ctx, m.recorder)
	}
	if event == nil || event.Article == nil {
		return nil
	}
	if event.Kind != platform.EventKindArticleCreated {
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

	agent, prompt, matched, err := m.resolveTriggeredAgent(ctx, event)
	if err != nil {
		return fmt.Errorf("llmchat resolve triggered agent: %w", err)
	}
	if !matched {
		return nil
	}
	startedAt := m.now()
	ctx = m.beginChatRequest(ctx, event, agent)
	ctx = withChatLifecycle(ctx, &chatLifecycle{state: chatStateAccepted, m: m, agent: agent})
	defer func() { m.finishChatRequest(ctx, event, agent, startedAt, handlerErr) }()

	provider, exists := m.providers[strings.TrimSpace(agent.Provider)]
	if !exists || provider == nil {
		return fmt.Errorf("llmchat handle article: provider %s for agent %s is not available", agent.Provider, agent.Name)
	}

	target, err := platform.OutboundTargetFromEvent(event)
	if err != nil {
		return fmt.Errorf("llmchat derive outbound target for agent %s: %w", agent.Name, err)
	}

	placeholder, err := m.dispatcher.SendMessage(ctx, platform.SendMessageRequest{
		Target:           target,
		Text:             defaultThinkingPlaceholder,
		Tags:             llmchatArticleTags(agent),
		ReplyToMessageID: event.Article.ID,
	})
	if err != nil {
		deliveryErr := fmt.Errorf("llmchat send placeholder for agent %s: %w", agent.Name, err)
		return ai.NewLLMFailure(ai.LLMFailureDelivery, false, 0, deliveryErr)
	}
	if err := validateHandlerDeadlineBudget(ctx, agent.RequestTimeout); err != nil {
		preflightErr := fmt.Errorf("llmchat preflight for agent %s: %w", agent.Name, err)
		timeoutErr := ai.NewLLMFailure(ai.LLMFailureTimeout, false, 0, preflightErr)
		return m.finalizePlaceholderFailure(ctx, target, placeholder.ID, timeoutErr)
	}

	reqCtx := ctx
	cancel := func() {}
	if agent.RequestTimeout > 0 {
		timeoutCtx, timeoutCancel := context.WithTimeout(ctx, agent.RequestTimeout)
		reqCtx = timeoutCtx
		cancel = timeoutCancel
	}
	defer cancel()

	toolRegistry, err := m.buildToolRegistry(event, agent)
	if err != nil {
		buildErr := fmt.Errorf("llmchat build tools for agent %s: %w", agent.Name, err)
		return m.finalizePlaceholderFailure(ctx, target, placeholder.ID, buildErr)
	}

	req, err := m.buildGenerateRequestWithTools(reqCtx, event, agent, prompt, toolRegistry)
	if err != nil {
		buildErr := fmt.Errorf("llmchat build request for agent %s: %w", agent.Name, err)
		return m.finalizePlaceholderFailure(ctx, target, placeholder.ID, buildErr)
	}
	transitionChatState(ctx, chatStateContextReady)

	if err := m.streamProviderReplyWithTools(reqCtx, ctx, target, placeholder.ID, provider, req, toolRegistry); err != nil {
		streamErr := fmt.Errorf("llmchat stream response for agent %s: %w", agent.Name, err)
		return m.finalizePlaceholderFailure(ctx, target, placeholder.ID, streamErr)
	}

	return nil
}

// buildToolRegistry combines request-scoped sub-agent tool handlers into one
// ToolRegistry.
func (m *Module) buildToolRegistry(_ *platform.Event, agent Agent) (*ToolRegistry, error) {
	var handlers []ToolHandler

	for _, subAgentCfg := range agent.SubAgents {
		provider, exists := m.providers[strings.TrimSpace(subAgentCfg.Provider)]
		if !exists || provider == nil {
			return nil, fmt.Errorf("build tool registry sub-agent %s: provider %s not available",
				subAgentCfg.Name, subAgentCfg.Provider)
		}

		tool, err := newSubAgentTool(subAgentCfg, provider, m.logger)
		if err != nil {
			return nil, fmt.Errorf("build tool registry sub-agent %s: %w", subAgentCfg.Name, err)
		}
		handlers = append(handlers, tool)
	}

	if len(handlers) == 0 {
		return nil, nil
	}

	return NewToolRegistry(handlers), nil
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
	target platform.OutboundTarget,
	placeholderMessageID string,
	handlerErr error,
) error {
	if handlerErr == nil {
		return nil
	}
	var streamErr *streamResponseError
	if errors.As(handlerErr, &streamErr) && streamErr.answerDelivered {
		return handlerErr
	}
	if strings.TrimSpace(placeholderMessageID) == "" {
		return handlerErr
	}
	if err := ctx.Err(); err != nil {
		return handlerErr
	}

	class := ai.ClassifyLLMFailure(handlerErr)
	if class == ai.LLMFailureDelivery {
		return handlerErr
	}
	transitionChatState(ctx, chatStateDelivering)
	editErr := m.retryEditMessage(ctx, platform.EditMessageRequest{
		Target:    target,
		MessageID: placeholderMessageID,
		Text:      chatFailureMessage(ctx, class),
	})
	if editErr == nil {
		return handlerErr
	}

	deliveryErr := fmt.Errorf(
		"llmchat finalize placeholder failure: %w",
		errors.Join(
			handlerErr,
			fmt.Errorf("llmchat finalize placeholder failure %s: %w", placeholderMessageID, editErr),
		),
	)
	return ai.NewLLMFailure(ai.LLMFailureDelivery, false, 0, deliveryErr)
}

func chatFailureMessage(ctx context.Context, class ai.LLMFailureClass) string {
	message := placeholderFailureMessage
	switch class {
	case ai.LLMFailureInvalidRequest:
		message = "I couldn't process that request. Please revise it and try again."
	case ai.LLMFailureAuthentication:
		message = "The AI service is not configured correctly right now."
	case ai.LLMFailureRateLimited:
		message = "The AI service is busy right now. Please try again shortly."
	case ai.LLMFailureProviderUnavailable:
		message = "The AI service is temporarily unavailable. Please try again shortly."
	case ai.LLMFailureTimeout:
		message = "The request took too long to complete. Please try again."
	case ai.LLMFailureCanceled:
		message = "The request was canceled before it completed."
	case ai.LLMFailureSafetyRefusal:
		message = "I can't help with that request because of the AI service's safety rules."
	case ai.LLMFailureEmptyResponse:
		message = "The AI service returned no answer. Please try again."
	case ai.LLMFailureTool:
		message = "A required tool could not complete the request. Please try again."
	case ai.LLMFailureDelivery:
		message = "The response could not be delivered. Please try again."
	case ai.LLMFailureInternal, "":
		message = placeholderFailureMessage
	}
	if trace, ok := panel.TraceFromContext(ctx); ok && strings.TrimSpace(trace.TraceID) != "" {
		traceID := strings.TrimSpace(trace.TraceID)
		if len(traceID) > 8 {
			traceID = traceID[len(traceID)-8:]
		}
		message += " Reference: " + traceID
	}
	return message
}

func (m *Module) matchTriggeredAgent(text string) (agent Agent, prompt string, matched bool) {
	longest := -1
	for _, candidate := range m.cfg.Agents {
		for _, candidateName := range allAgentNames(candidate) {
			candidatePrompt, ok := matchAgentTrigger(text, candidateName)
			if !ok {
				continue
			}

			candidateLen := len([]rune(strings.TrimSpace(candidateName)))
			if candidateLen > longest {
				agent = candidate
				prompt = candidatePrompt
				longest = candidateLen
				matched = true
			}
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
	_ core.Module          = (*Module)(nil)
	_ core.ModuleRegistrar = (*Module)(nil)
)
