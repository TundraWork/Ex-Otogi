package llmchat

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"ex-otogi/pkg/llm"
	llmconfig "ex-otogi/pkg/llm/config"
	"ex-otogi/pkg/llm/providers/gemini"
	"ex-otogi/pkg/llm/providers/openai"
	"ex-otogi/pkg/otogi"
)

const llmchatHandlerTimeoutGrace = 5 * time.Second

const placeholderFailureMessage = "Sorry, I couldn't generate a response right now."

const serviceLogger = "logger"

// fileModuleConfig is the JSON layout for llmchat module configuration.
type fileModuleConfig struct {
	// ConfigFile is the path to the LLM configuration file.
	ConfigFile string `json:"config_file"`
}

// Module provides keyword-triggered LLM chat behavior.
type Module struct {
	cfg Config

	dispatcher      otogi.SinkDispatcher
	memory          otogi.MemoryService
	providers       map[string]otogi.LLMProvider
	parser          otogi.MarkdownParser
	mediaDownloader otogi.MediaDownloader

	providerRegistry otogi.LLMProviderRegistry
	logger           *slog.Logger
	clock            func() time.Time
	sleep            func(context.Context, time.Duration) error
}

// Option mutates one llmchat module construction input.
type Option func(*Module)

// New creates one llmchat module instance. Configuration is loaded from
// the ConfigRegistry during OnRegister.
func New(options ...Option) *Module {
	module := &Module{
		providers: make(map[string]otogi.LLMProvider),
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
func (m *Module) Spec() otogi.ModuleSpec {
	return otogi.ModuleSpec{
		AdditionalCapabilities: []otogi.Capability{
			{
				Name:        "llm-chat-trigger",
				Description: "handles keyword-triggered llm chat from article events",
				Interest: otogi.InterestSet{
					Kinds:          []otogi.EventKind{otogi.EventKindArticleCreated},
					RequireArticle: true,
				},
				RequiredServices: []string{
					otogi.ServiceSinkDispatcher,
					otogi.ServiceMemory,
					otogi.ServiceMarkdownParser,
				},
			},
		},
	}
}

// OnRegister loads configuration, builds LLM providers, and resolves dependencies.
func (m *Module) OnRegister(ctx context.Context, runtime otogi.ModuleRuntime) error {
	logger, err := otogi.ResolveAs[*slog.Logger](runtime.Services(), serviceLogger)
	switch {
	case err == nil:
		m.logger = logger
	case errors.Is(err, otogi.ErrServiceNotFound):
	default:
		return fmt.Errorf("llmchat resolve logger: %w", err)
	}

	cfg, registry, err := m.loadConfig(ctx, runtime)
	if err != nil {
		return fmt.Errorf("llmchat load config: %w", err)
	}
	m.cfg = cfg

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

	markdownParser, err := otogi.ResolveAs[otogi.MarkdownParser](
		runtime.Services(),
		otogi.ServiceMarkdownParser,
	)
	if err != nil {
		return fmt.Errorf("llmchat resolve markdown parser: %w", err)
	}
	mediaDownloader, err := otogi.ResolveAs[otogi.MediaDownloader](
		runtime.Services(),
		otogi.ServiceMediaDownloader,
	)
	switch {
	case err == nil:
	case errors.Is(err, otogi.ErrServiceNotFound):
		mediaDownloader = nil
	default:
		return fmt.Errorf("llmchat resolve media downloader: %w", err)
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
	m.parser = markdownParser
	m.mediaDownloader = mediaDownloader
	m.providerRegistry = registry
	m.providers = resolvedProviders

	handlerTimeout := m.cfg.RequestTimeout + llmchatHandlerTimeoutGrace
	if _, err := runtime.Subscribe(ctx, otogi.InterestSet{
		Kinds:          []otogi.EventKind{otogi.EventKindArticleCreated},
		RequireArticle: true,
	}, otogi.SubscriptionSpec{
		Name:           "llmchat-articles",
		HandlerTimeout: handlerTimeout,
	}, m.handleArticle); err != nil {
		return fmt.Errorf("llmchat subscribe: %w", err)
	}
	if err := runtime.Services().Register(otogi.ServiceLLMProviderRegistry, registry); err != nil {
		return fmt.Errorf("llmchat register provider registry service: %w", err)
	}

	return nil
}

// loadConfig reads the module config from the registry and loads the LLM config file.
func (m *Module) loadConfig(
	ctx context.Context,
	runtime otogi.ModuleRuntime,
) (Config, otogi.LLMProviderRegistry, error) {
	moduleCfg, err := otogi.ParseModuleConfig[fileModuleConfig](runtime.Config(), "llmchat")
	if err != nil {
		return Config{}, nil, fmt.Errorf("parse module config: %w", err)
	}

	configFile := strings.TrimSpace(moduleCfg.ConfigFile)
	if envPath := strings.TrimSpace(os.Getenv("OTOGI_LLM_CONFIG_FILE")); envPath != "" {
		configFile = envPath
	}
	if configFile == "" {
		return Config{}, nil, fmt.Errorf("config_file is required")
	}

	llmCfg, err := llmconfig.LoadFile(configFile)
	if err != nil {
		return Config{}, nil, fmt.Errorf("load llm config file %s: %w", configFile, err)
	}

	registry, err := buildProviderRegistry(ctx, llmCfg, m.logger)
	if err != nil {
		return Config{}, nil, err
	}

	chatCfg := toLLMChatConfig(llmCfg)
	if err := chatCfg.Validate(); err != nil {
		return Config{}, nil, fmt.Errorf("validate config: %w", err)
	}

	return chatCfg, registry, nil
}

// buildProviderRegistry builds the provider registry from one runtime LLM config file.
func buildProviderRegistry(
	ctx context.Context,
	cfg llmconfig.Config,
	logger *slog.Logger,
) (otogi.LLMProviderRegistry, error) {
	providers := make(map[string]otogi.LLMProvider, len(cfg.Providers))
	for profileKey, profile := range cfg.Providers {
		providerType := strings.ToLower(strings.TrimSpace(profile.Type))
		switch providerType {
		case "openai":
			openAICfg := openai.ProviderConfig{
				APIKey:  profile.APIKey,
				BaseURL: profile.BaseURL,
			}
			if profile.OpenAI != nil {
				openAICfg.Organization = profile.OpenAI.Organization
				openAICfg.Project = profile.OpenAI.Project
				openAICfg.MaxRetries = cloneOptionalInt(profile.OpenAI.MaxRetries)
			}

			provider, err := openai.New(openAICfg)
			if err != nil {
				return nil, fmt.Errorf("provider profile %s: %w", profileKey, err)
			}
			providers[profileKey] = provider
		case "gemini":
			geminiCfg := gemini.ProviderConfig{
				APIKey:  profile.APIKey,
				BaseURL: profile.BaseURL,
				Logger:  logger,
			}
			if profile.Gemini != nil {
				geminiCfg.APIVersion = profile.Gemini.APIVersion
				geminiCfg.GoogleSearch = cloneOptionalBool(profile.Gemini.RequestDefaults.GoogleSearch)
				geminiCfg.URLContext = cloneOptionalBool(profile.Gemini.RequestDefaults.URLContext)
				geminiCfg.ThinkingBudget = cloneOptionalInt(profile.Gemini.RequestDefaults.ThinkingBudget)
				geminiCfg.IncludeThoughts = cloneOptionalBool(profile.Gemini.RequestDefaults.IncludeThoughts)
				geminiCfg.ThinkingLevel = profile.Gemini.RequestDefaults.ThinkingLevel
				geminiCfg.ResponseMIMEType = profile.Gemini.RequestDefaults.ResponseMIMEType
				geminiCfg.SafetyFilterOff = cloneOptionalBool(profile.Gemini.RequestDefaults.SafetyFilterOff)
			}

			provider, err := gemini.New(ctx, geminiCfg)
			if err != nil {
				return nil, fmt.Errorf("provider profile %s: %w", profileKey, err)
			}
			providers[profileKey] = provider
		default:
			return nil, fmt.Errorf("provider profile %s: unsupported type %q", profileKey, profile.Type)
		}
	}

	registry, err := llm.NewRegistry(providers)
	if err != nil {
		return nil, fmt.Errorf("new llm provider registry: %w", err)
	}

	return registry, nil
}

func toLLMChatConfig(cfg llmconfig.Config) Config {
	agents := make([]Agent, 0, len(cfg.Agents))
	for _, agent := range cfg.Agents {
		agents = append(agents, Agent{
			Name:                 agent.Name,
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
			ImageInputs: ImageInputPolicy{
				Enabled:       agent.ImageInputs.Enabled,
				MaxImages:     agent.ImageInputs.MaxImages,
				MaxImageBytes: agent.ImageInputs.MaxImageBytes,
				MaxTotalBytes: agent.ImageInputs.MaxTotalBytes,
				Detail:        agent.ImageInputs.Detail,
			},
		})
	}

	return Config{
		RequestTimeout: cfg.RequestTimeout,
		Agents:         agents,
	}
}

func cloneOptionalInt(value *int) *int {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneOptionalBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
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
