package llmchat

import (
	"context"
	"fmt"
	"strings"
	"time"

	"ex-otogi/pkg/otogi"
)

// Module provides keyword-triggered LLM chat behavior.
type Module struct {
	cfg Config

	dispatcher otogi.SinkDispatcher
	memory     otogi.MemoryService
	providers  map[string]otogi.LLMProvider

	providerRegistry otogi.LLMProviderRegistry
	clock            func() time.Time
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
		clock:     time.Now,
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
				Subscription: otogi.NewDefaultSubscriptionSpec("llmchat-articles"),
				Handler:      m.handleArticle,
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

	reqCtx := ctx
	cancel := func() {}
	if m.cfg.RequestTimeout > 0 {
		timeoutCtx, timeoutCancel := context.WithTimeout(ctx, m.cfg.RequestTimeout)
		reqCtx = timeoutCtx
		cancel = timeoutCancel
	}
	defer cancel()

	req, err := m.buildGenerateRequest(reqCtx, event, agent, prompt)
	if err != nil {
		return fmt.Errorf("llmchat build request for agent %s: %w", agent.Name, err)
	}

	target, err := otogi.OutboundTargetFromEvent(event)
	if err != nil {
		return fmt.Errorf("llmchat derive outbound target for agent %s: %w", agent.Name, err)
	}

	if err := m.streamProviderReply(reqCtx, target, event.Article.ID, provider, req); err != nil {
		return fmt.Errorf("llmchat stream response for agent %s: %w", agent.Name, err)
	}

	return nil
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
