// Package llmruntime owns shared LLM configuration and provider construction.
package llmruntime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"ex-otogi/pkg/llm"
	llmconfig "ex-otogi/pkg/llm/config"
	"ex-otogi/pkg/llm/providers/gemini"
	"ex-otogi/pkg/llm/providers/openai"
	"ex-otogi/pkg/otogi/ai"
	"ex-otogi/pkg/otogi/core"
)

const serviceLogger = "logger"

type fileConfig struct {
	ConfigFile string `json:"config_file"`
}

// Module loads one validated LLM configuration and publishes shared provider
// registries before policy modules register.
type Module struct {
	logger *slog.Logger
}

// New creates the shared LLM runtime module.
func New() *Module { return &Module{logger: slog.Default()} }

// Name returns the stable module identifier.
func (*Module) Name() string { return "llmruntime" }

// Spec declares no event capabilities; this module only publishes services.
func (*Module) Spec() core.ModuleSpec { return core.ModuleSpec{} }

// OnRegister loads configuration, constructs providers, and registers the
// immutable runtime services consumed by memory and chat.
func (m *Module) OnRegister(ctx context.Context, runtime core.ModuleRuntime) error {
	logger, err := core.ResolveAs[*slog.Logger](runtime.Services(), serviceLogger)
	switch {
	case err == nil:
		m.logger = logger
	case errors.Is(err, core.ErrServiceNotFound):
	default:
		return fmt.Errorf("llmruntime resolve logger: %w", err)
	}
	moduleCfg, err := core.ParseModuleConfig[fileConfig](runtime.Config(), m.Name())
	if err != nil {
		return fmt.Errorf("llmruntime parse config: %w", err)
	}
	configFile := strings.TrimSpace(moduleCfg.ConfigFile)
	if envPath := strings.TrimSpace(os.Getenv("OTOGI_LLM_CONFIG_FILE")); envPath != "" {
		configFile = envPath
	}
	if configFile == "" {
		return fmt.Errorf("llmruntime config_file is required")
	}
	cfg, err := llmconfig.LoadFile(configFile)
	if err != nil {
		return fmt.Errorf("llmruntime load config file %s: %w", configFile, err)
	}
	providers, err := buildProviderRegistry(ctx, cfg, m.logger)
	if err != nil {
		return err
	}
	embeddings, err := buildEmbeddingProviderRegistry(ctx, cfg, m.logger)
	if err != nil {
		return err
	}
	if err := runtime.Services().Register(llm.ServiceRuntimeConfig, cfg); err != nil {
		return fmt.Errorf("llmruntime register config service: %w", err)
	}
	if err := runtime.Services().Register(ai.ServiceLLMProviderRegistry, providers); err != nil {
		return fmt.Errorf("llmruntime register provider registry: %w", err)
	}
	if err := runtime.Services().Register(ai.ServiceEmbeddingProviderRegistry, embeddings); err != nil {
		return fmt.Errorf("llmruntime register embedding registry: %w", err)
	}
	return nil
}

// OnStart has no work because providers are ready during registration.
func (*Module) OnStart(context.Context) error { return nil }

// OnShutdown has no owned background work.
func (*Module) OnShutdown(context.Context) error { return nil }

func buildProviderRegistry(ctx context.Context, cfg llmconfig.Config, logger *slog.Logger) (ai.LLMProviderRegistry, error) {
	providers := make(map[string]ai.LLMProvider, len(cfg.Providers))
	for key, profile := range cfg.Providers {
		switch strings.ToLower(strings.TrimSpace(profile.Type)) {
		case "openai":
			providerCfg := openai.ProviderConfig{APIKey: profile.APIKey, BaseURL: profile.BaseURL}
			if profile.OpenAI != nil {
				providerCfg.Organization, providerCfg.Project = profile.OpenAI.Organization, profile.OpenAI.Project
			}
			provider, err := openai.New(providerCfg)
			if err != nil {
				return nil, fmt.Errorf("llmruntime provider %s: %w", key, err)
			}
			providers[key] = provider
		case "gemini":
			providerCfg := gemini.ProviderConfig{APIKey: profile.APIKey, BaseURL: profile.BaseURL, Logger: logger}
			if profile.Gemini != nil {
				providerCfg.APIVersion = profile.Gemini.APIVersion
				providerCfg.GoogleSearch = cloneBool(profile.Gemini.RequestDefaults.GoogleSearch)
				providerCfg.URLContext = cloneBool(profile.Gemini.RequestDefaults.URLContext)
				providerCfg.ThinkingBudget = cloneInt(profile.Gemini.RequestDefaults.ThinkingBudget)
				providerCfg.IncludeThoughts = cloneBool(profile.Gemini.RequestDefaults.IncludeThoughts)
				providerCfg.ThinkingLevel = profile.Gemini.RequestDefaults.ThinkingLevel
				providerCfg.ResponseMIMEType = profile.Gemini.RequestDefaults.ResponseMIMEType
				providerCfg.SafetyFilterOff = cloneBool(profile.Gemini.RequestDefaults.SafetyFilterOff)
			}
			provider, err := gemini.New(ctx, providerCfg)
			if err != nil {
				return nil, fmt.Errorf("llmruntime provider %s: %w", key, err)
			}
			providers[key] = provider
		default:
			return nil, fmt.Errorf("llmruntime provider %s: unsupported type %q", key, profile.Type)
		}
	}
	registry, err := llm.NewRegistry(providers)
	if err != nil {
		return nil, fmt.Errorf("llmruntime create provider registry: %w", err)
	}
	return registry, nil
}

func buildEmbeddingProviderRegistry(ctx context.Context, cfg llmconfig.Config, logger *slog.Logger) (ai.EmbeddingProviderRegistry, error) {
	providers := make(map[string]ai.EmbeddingProvider, len(cfg.Providers))
	for key, profile := range cfg.Providers {
		switch strings.ToLower(strings.TrimSpace(profile.Type)) {
		case "openai":
			providerCfg := openai.EmbeddingProviderConfig{APIKey: profile.APIKey, BaseURL: profile.BaseURL, DefaultModel: profile.EmbeddingModel, DefaultDimensions: profile.EmbeddingDimensions}
			if profile.OpenAI != nil {
				providerCfg.Organization, providerCfg.Project = profile.OpenAI.Organization, profile.OpenAI.Project
				providerCfg.MaxRetries = cloneInt(profile.OpenAI.EmbeddingMaxRetries)
			}
			provider, err := openai.NewEmbeddingProvider(providerCfg)
			if err != nil {
				return nil, fmt.Errorf("llmruntime embedding provider %s: %w", key, err)
			}
			providers[key] = provider
		case "gemini":
			providerCfg := gemini.EmbeddingProviderConfig{APIKey: profile.APIKey, BaseURL: profile.BaseURL, DefaultModel: profile.EmbeddingModel, DefaultDimensions: profile.EmbeddingDimensions, Logger: logger}
			if profile.Gemini != nil {
				providerCfg.APIVersion = profile.Gemini.APIVersion
			}
			provider, err := gemini.NewEmbeddingProvider(ctx, providerCfg)
			if err != nil {
				return nil, fmt.Errorf("llmruntime embedding provider %s: %w", key, err)
			}
			providers[key] = provider
		default:
			return nil, fmt.Errorf("llmruntime embedding provider %s: unsupported type %q", key, profile.Type)
		}
	}
	registry, err := llm.NewEmbeddingRegistry(providers)
	if err != nil {
		return nil, fmt.Errorf("llmruntime create embedding registry: %w", err)
	}
	return registry, nil
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

var _ core.ModuleRegistrar = (*Module)(nil)
