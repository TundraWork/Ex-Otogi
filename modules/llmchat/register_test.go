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

	"ex-otogi/pkg/otogi"
)

func TestOnRegisterLoadsConfigBuildsProvidersAndSubscribes(t *testing.T) {
	t.Parallel()

	llmConfigPath := writeRuntimeLLMConfigFile(t, "45s", "openai-main")
	services := newRecordingServiceRegistry(map[string]any{
		serviceLogger:                slog.Default(),
		otogi.ServiceSinkDispatcher:  &sinkDispatcherStub{},
		otogi.ServiceMemory:          &memoryStub{},
		otogi.ServiceMarkdownParser:  markdownParserStub{},
		otogi.ServiceMediaDownloader: &mediaDownloaderStub{},
	})

	var (
		gotInterest otogi.InterestSet
		gotSpec     otogi.SubscriptionSpec
	)
	runtime := registrationRuntimeStub{
		registry: services,
		configs:  testLLMChatConfigRegistry(t, llmConfigPath),
		subscribe: func(
			_ context.Context,
			interest otogi.InterestSet,
			spec otogi.SubscriptionSpec,
			handler otogi.EventHandler,
		) (otogi.Subscription, error) {
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
	if len(gotInterest.Kinds) != 1 || gotInterest.Kinds[0] != otogi.EventKindArticleCreated {
		t.Fatalf("interest kinds = %v, want [%s]", gotInterest.Kinds, otogi.EventKindArticleCreated)
	}

	resolved, err := services.Resolve(otogi.ServiceLLMProviderRegistry)
	if err != nil {
		t.Fatalf("resolve provider registry failed: %v", err)
	}
	registry, ok := resolved.(otogi.LLMProviderRegistry)
	if !ok {
		t.Fatalf("resolved provider registry type = %T, want otogi.LLMProviderRegistry", resolved)
	}
	if _, err := registry.Resolve("openai-main"); err != nil {
		t.Fatalf("resolve openai-main failed: %v", err)
	}
}

func TestOnRegisterUsesLLMConfigEnvOverride(t *testing.T) {
	llmConfigPath := writeRuntimeLLMConfigFile(t, "75s", "openai-env")
	t.Setenv("OTOGI_LLM_CONFIG_FILE", llmConfigPath)

	services := newRecordingServiceRegistry(map[string]any{
		serviceLogger:               slog.Default(),
		otogi.ServiceSinkDispatcher: &sinkDispatcherStub{},
		otogi.ServiceMemory:         &memoryStub{},
		otogi.ServiceMarkdownParser: markdownParserStub{},
	})
	runtime := registrationRuntimeStub{
		registry: services,
		configs: testLLMChatConfigRegistry(
			t,
			filepath.Join(t.TempDir(), "ignored-by-env.json"),
		),
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

func testLLMChatConfigRegistry(t *testing.T, configFile string) otogi.ConfigRegistry {
	t.Helper()

	raw, err := json.Marshal(fileModuleConfig{ConfigFile: configFile})
	if err != nil {
		t.Fatalf("marshal llmchat module config: %v", err)
	}

	registry := newConfigRegistryStub()
	if err := registry.Register("llmchat", raw); err != nil {
		t.Fatalf("register llmchat module config: %v", err)
	}

	return registry
}

type registrationRuntimeStub struct {
	registry  otogi.ServiceRegistry
	configs   otogi.ConfigRegistry
	subscribe func(
		ctx context.Context,
		interest otogi.InterestSet,
		spec otogi.SubscriptionSpec,
		handler otogi.EventHandler,
	) (otogi.Subscription, error)
}

func (s registrationRuntimeStub) Services() otogi.ServiceRegistry {
	return s.registry
}

func (s registrationRuntimeStub) Config() otogi.ConfigRegistry {
	return s.configs
}

func (s registrationRuntimeStub) Subscribe(
	ctx context.Context,
	interest otogi.InterestSet,
	spec otogi.SubscriptionSpec,
	handler otogi.EventHandler,
) (otogi.Subscription, error) {
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
		return fmt.Errorf("register service %s: %w", name, otogi.ErrServiceAlreadyRegistered)
	}

	r.values[name] = service

	return nil
}

func (r *recordingServiceRegistry) Resolve(name string) (any, error) {
	service, exists := r.values[name]
	if !exists {
		return nil, otogi.ErrServiceNotFound
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
		return fmt.Errorf("register module config %s: %w", moduleName, otogi.ErrConfigAlreadyRegistered)
	}

	r.configs[moduleName] = append(json.RawMessage(nil), raw...)

	return nil
}

func (r *configRegistryStub) Resolve(moduleName string) (json.RawMessage, error) {
	raw, exists := r.configs[moduleName]
	if !exists {
		return nil, fmt.Errorf("resolve module config %s: %w", moduleName, otogi.ErrConfigNotFound)
	}

	return append(json.RawMessage(nil), raw...), nil
}
