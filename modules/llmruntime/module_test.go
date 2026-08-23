package llmruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"ex-otogi/pkg/llm"
	llmconfig "ex-otogi/pkg/llm/config"
	"ex-otogi/pkg/otogi/ai"
	"ex-otogi/pkg/otogi/core"
)

func TestOnRegisterPublishesSharedRuntimeServices(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "llm.json")
	data := []byte(`{"request_timeout":"30s","providers":{"openai-main":{"type":"openai","api_key":"test","embedding_model":"text-embedding-3-small"}},"agents":[{"name":"Otogi","description":"Assistant","provider":"openai-main","model":"gpt-5-mini","system_prompt_template":"You are {{.AgentName}}","request_timeout":"30s"}]}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	configRaw, err := json.Marshal(fileConfig{ConfigFile: path})
	if err != nil {
		t.Fatalf("marshal module config: %v", err)
	}
	services := &testServices{values: map[string]any{}}
	configs := &testConfigs{values: map[string]json.RawMessage{"llmruntime": configRaw}}
	module := New()
	if err := module.OnRegister(context.Background(), testRuntime{services: services, configs: configs}); err != nil {
		t.Fatalf("OnRegister() error = %v", err)
	}
	if _, ok := services.values[llm.ServiceRuntimeConfig].(llmconfig.Config); !ok {
		t.Fatalf("runtime config service type = %T", services.values[llm.ServiceRuntimeConfig])
	}
	providers, ok := services.values[ai.ServiceLLMProviderRegistry].(ai.LLMProviderRegistry)
	if !ok {
		t.Fatalf("provider registry service type = %T", services.values[ai.ServiceLLMProviderRegistry])
	}
	if _, err := providers.Resolve("openai-main"); err != nil {
		t.Fatalf("resolve LLM provider: %v", err)
	}
	embeddings, ok := services.values[ai.ServiceEmbeddingProviderRegistry].(ai.EmbeddingProviderRegistry)
	if !ok {
		t.Fatalf("embedding registry service type = %T", services.values[ai.ServiceEmbeddingProviderRegistry])
	}
	if _, err := embeddings.Resolve("openai-main"); err != nil {
		t.Fatalf("resolve embedding provider: %v", err)
	}
}

type testRuntime struct {
	services core.ServiceRegistry
	configs  core.ConfigRegistry
}

func (r testRuntime) Services() core.ServiceRegistry { return r.services }
func (r testRuntime) Config() core.ConfigRegistry    { return r.configs }
func (testRuntime) Subscribe(context.Context, core.InterestSet, core.SubscriptionSpec, core.EventHandler) (core.Subscription, error) {
	return nil, fmt.Errorf("unexpected subscribe")
}

type testServices struct{ values map[string]any }

func (s *testServices) Register(name string, service any) error {
	if _, exists := s.values[name]; exists {
		return core.ErrServiceAlreadyRegistered
	}
	s.values[name] = service
	return nil
}

func (s *testServices) Resolve(name string) (any, error) {
	service, exists := s.values[name]
	if !exists {
		return nil, core.ErrServiceNotFound
	}
	return service, nil
}

type testConfigs struct{ values map[string]json.RawMessage }

func (c *testConfigs) Register(name string, raw json.RawMessage) error {
	c.values[name] = raw
	return nil
}

func (c *testConfigs) Resolve(name string) (json.RawMessage, error) {
	raw, exists := c.values[name]
	if !exists {
		return nil, core.ErrConfigNotFound
	}
	return raw, nil
}
