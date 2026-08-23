package llmchat

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ex-otogi/pkg/llm"
	llmconfig "ex-otogi/pkg/llm/config"
	"ex-otogi/pkg/otogi/ai"
	"ex-otogi/pkg/otogi/core"
	"ex-otogi/pkg/otogi/platform"
)

func TestOnRegisterLoadsConfigBuildsProvidersAndSubscribes(t *testing.T) {
	t.Parallel()

	llmConfigPath := writeRuntimeLLMConfigFile(t, "45s", "openai-main")
	llmCfg, providerRegistry := loadRuntimeServices(t, llmConfigPath, "openai-main")
	retriever := &semanticRetrieverStub{available: true}
	services := newRecordingServiceRegistry(map[string]any{
		serviceLogger:                   slog.Default(),
		llm.ServiceRuntimeConfig:        llmCfg,
		ai.ServiceLLMProviderRegistry:   providerRegistry,
		ai.ServiceSemanticRetriever:     retriever,
		platform.ServiceSinkDispatcher:  &sinkDispatcherStub{},
		core.ServiceMemory:              &memoryStub{},
		platform.ServiceMarkdownParser:  markdownParserStub{},
		platform.ServiceMediaDownloader: &mediaDownloaderStub{},
	})

	var (
		gotInterest core.InterestSet
		gotSpec     core.SubscriptionSpec
	)
	runtime := registrationRuntimeStub{
		registry: services,
		configs:  newConfigRegistryStub(),
		subscribe: func(
			_ context.Context,
			interest core.InterestSet,
			spec core.SubscriptionSpec,
			handler core.EventHandler,
		) (core.Subscription, error) {
			if handler == nil {
				t.Fatal("expected llmchat handler")
			}
			gotInterest = interest
			gotSpec = spec

			return registrationSubscriptionStub{name: spec.Name}, nil
		},
	}

	module := New()
	if err := module.OnRegister(context.Background(), runtime); err != nil {
		t.Fatalf("OnRegister failed: %v", err)
	}

	if module.cfg.RequestTimeout != 45*time.Second {
		t.Fatalf("request timeout = %s, want 45s", module.cfg.RequestTimeout)
	}
	if len(module.cfg.Agents) != 1 || len(module.cfg.Agents[0].Aliases) != 1 || module.cfg.Agents[0].Aliases[0] != "Oto" {
		t.Fatalf("agent aliases = %+v, want [Oto]", module.cfg.Agents)
	}
	if module.dispatcher == nil {
		t.Fatal("expected sink dispatcher to be configured")
	}
	if module.memory == nil {
		t.Fatal("expected memory service to be configured")
	}
	if module.parser == nil {
		t.Fatal("expected markdown parser to be configured")
	}
	if module.providerRegistry == nil {
		t.Fatal("expected provider registry to be configured")
	}
	if module.semanticRetriever != retriever {
		t.Fatal("expected registered semantic retriever")
	}
	if module.mediaDownloader == nil {
		t.Fatal("expected media downloader to be configured")
	}
	if len(module.providers) != 1 {
		t.Fatalf("providers len = %d, want 1", len(module.providers))
	}
	if module.providers["openai-main"] == nil {
		t.Fatal("expected openai-main provider to be resolved")
	}

	if gotSpec.Name != "llmchat-articles" {
		t.Fatalf("subscription name = %q, want llmchat-articles", gotSpec.Name)
	}
	wantHandlerTimeout := 45*time.Second + llmchatHandlerTimeoutGrace
	if gotSpec.HandlerTimeout != wantHandlerTimeout {
		t.Fatalf("handler timeout = %s, want %s", gotSpec.HandlerTimeout, wantHandlerTimeout)
	}
	if !gotInterest.RequireArticle {
		t.Fatal("expected article requirement in llmchat subscription")
	}
	if len(gotInterest.Kinds) != 1 || gotInterest.Kinds[0] != platform.EventKindArticleCreated {
		t.Fatalf("interest kinds = %v, want [%s]", gotInterest.Kinds, platform.EventKindArticleCreated)
	}

	if _, err := module.providerRegistry.Resolve("openai-main"); err != nil {
		t.Fatalf("resolve openai-main failed: %v", err)
	}
}

func TestOnRegisterUsesSharedRuntimeConfig(t *testing.T) {
	llmConfigPath := writeRuntimeLLMConfigFile(t, "75s", "openai-env")
	llmCfg, providerRegistry := loadRuntimeServices(t, llmConfigPath, "openai-env")

	services := newRecordingServiceRegistry(map[string]any{
		serviceLogger:                  slog.Default(),
		llm.ServiceRuntimeConfig:       llmCfg,
		ai.ServiceLLMProviderRegistry:  providerRegistry,
		platform.ServiceSinkDispatcher: &sinkDispatcherStub{},
		core.ServiceMemory:             &memoryStub{},
		platform.ServiceMarkdownParser: markdownParserStub{},
	})
	runtime := registrationRuntimeStub{
		registry: services,
		configs:  newConfigRegistryStub(),
	}

	module := New()
	if err := module.OnRegister(context.Background(), runtime); err != nil {
		t.Fatalf("OnRegister failed: %v", err)
	}

	if module.cfg.RequestTimeout != 75*time.Second {
		t.Fatalf("request timeout = %s, want env override 75s", module.cfg.RequestTimeout)
	}
	if len(module.providers) != 1 {
		t.Fatalf("providers len = %d, want 1", len(module.providers))
	}
	if module.providers["openai-env"] == nil {
		t.Fatal("expected env-configured provider to be resolved")
	}
	if module.mediaDownloader != nil {
		t.Fatal("expected media downloader to remain optional when service is absent")
	}
}

func writeRuntimeLLMConfigFile(t *testing.T, requestTimeout string, providerKey string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "llm.json")
	body := fmt.Sprintf(`{
		"request_timeout":%q,
		"providers":{
			%q:{
				"type":"openai",
				"api_key":"sk-test"
			}
		},
		"agents":[
			{
				"name":"Otogi",
				"aliases":["Oto"],
				"description":"Assistant",
				"provider":%q,
				"model":"gpt-5-mini",
				"system_prompt_template":"You are {{.AgentName}}",
				"request_timeout":"30s"
			}
		]
	}`, requestTimeout, providerKey, providerKey)

	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write llm config file: %v", err)
	}

	return path
}

func loadRuntimeServices(t *testing.T, configFile string, providerKey string) (llmconfig.Config, ai.LLMProviderRegistry) {
	t.Helper()

	cfg, err := llmconfig.LoadFile(configFile)
	if err != nil {
		t.Fatalf("load runtime config: %v", err)
	}
	registry, err := llm.NewRegistry(map[string]ai.LLMProvider{providerKey: &providerStub{}})
	if err != nil {
		t.Fatalf("create provider registry: %v", err)
	}
	return cfg, registry
}

type registrationRuntimeStub struct {
	registry  core.ServiceRegistry
	configs   core.ConfigRegistry
	subscribe func(
		ctx context.Context,
		interest core.InterestSet,
		spec core.SubscriptionSpec,
		handler core.EventHandler,
	) (core.Subscription, error)
}

func (s registrationRuntimeStub) Services() core.ServiceRegistry {
	return s.registry
}

func (s registrationRuntimeStub) Config() core.ConfigRegistry {
	return s.configs
}

func (s registrationRuntimeStub) Subscribe(
	ctx context.Context,
	interest core.InterestSet,
	spec core.SubscriptionSpec,
	handler core.EventHandler,
) (core.Subscription, error) {
	if s.subscribe != nil {
		return s.subscribe(ctx, interest, spec, handler)
	}

	return registrationSubscriptionStub{name: spec.Name}, nil
}

type registrationSubscriptionStub struct {
	name string
}

func (s registrationSubscriptionStub) Name() string {
	return s.name
}

func (registrationSubscriptionStub) Close(context.Context) error {
	return nil
}

type recordingServiceRegistry struct {
	values map[string]any
}

func newRecordingServiceRegistry(values map[string]any) *recordingServiceRegistry {
	cloned := make(map[string]any, len(values))
	for name, service := range values {
		cloned[name] = service
	}

	return &recordingServiceRegistry{values: cloned}
}

func (r *recordingServiceRegistry) Register(name string, service any) error {
	if name == "" {
		return fmt.Errorf("register service: empty name")
	}
	if service == nil {
		return fmt.Errorf("register service %s: nil service", name)
	}
	if _, exists := r.values[name]; exists {
		return fmt.Errorf("register service %s: %w", name, core.ErrServiceAlreadyRegistered)
	}

	r.values[name] = service

	return nil
}

func (r *recordingServiceRegistry) Resolve(name string) (any, error) {
	service, exists := r.values[name]
	if !exists {
		return nil, core.ErrServiceNotFound
	}

	return service, nil
}

type configRegistryStub struct {
	configs map[string]json.RawMessage
}

func newConfigRegistryStub() *configRegistryStub {
	return &configRegistryStub{
		configs: make(map[string]json.RawMessage),
	}
}

func (r *configRegistryStub) Register(moduleName string, raw json.RawMessage) error {
	if moduleName == "" {
		return fmt.Errorf("register module config: empty module name")
	}
	if len(raw) == 0 {
		return fmt.Errorf("register module config %s: empty config", moduleName)
	}
	if _, exists := r.configs[moduleName]; exists {
		return fmt.Errorf("register module config %s: %w", moduleName, core.ErrConfigAlreadyRegistered)
	}

	r.configs[moduleName] = append(json.RawMessage(nil), raw...)

	return nil
}

func (r *configRegistryStub) Resolve(moduleName string) (json.RawMessage, error) {
	raw, exists := r.configs[moduleName]
	if !exists {
		return nil, fmt.Errorf("resolve module config %s: %w", moduleName, core.ErrConfigNotFound)
	}

	return append(json.RawMessage(nil), raw...), nil
}
