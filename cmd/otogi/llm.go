package main

import (
	"fmt"
	"log/slog"
	"net/url"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/gemini"
	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// LLMConfig configures the language model providers modules can use. Each
// provider is optional; a module names the one it uses.
type LLMConfig struct {
	Gemini *GeminiConfig `yaml:"gemini"`
}

// GeminiConfig configures the Google Gemini provider.
type GeminiConfig struct {
	// APIKey is the Gemini API key.
	APIKey string `yaml:"api_key"`
	// BaseURL is the API's address; empty means
	// https://generativelanguage.googleapis.com. A proxy or gateway that
	// speaks the Gemini API works too.
	BaseURL string `yaml:"base_url"`
}

func (c LLMConfig) validate() []error {
	var errs []error
	if g := c.Gemini; g != nil {
		if g.APIKey == "" {
			errs = append(errs, fmt.Errorf("llm.gemini.api_key is required"))
		}
		if g.BaseURL != "" {
			if u, err := url.Parse(g.BaseURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				errs = append(errs, fmt.Errorf("llm.gemini.base_url %q: want an absolute http(s) URL", g.BaseURL))
			}
		}
	}
	return errs
}

// buildProviders constructs the configured providers, by name. It does no
// I/O.
func buildProviders(cfg LLMConfig, recorder otogi.Recorder, log *slog.Logger) (map[string]llm.Provider, error) {
	providers := map[string]llm.Provider{}
	if g := cfg.Gemini; g != nil {
		p, err := gemini.New(gemini.Config{APIKey: g.APIKey, BaseURL: g.BaseURL, Recorder: recorder, Logger: log.With("component", "llm:"+gemini.Name)})
		if err != nil {
			return nil, fmt.Errorf("llm.gemini: %w", err)
		}
		providers[gemini.Name] = p
	}
	return providers, nil
}

// providerLookup finds a provider a module's config names.
func providerLookup(providers map[string]llm.Provider) func(name string) (llm.Provider, error) {
	return func(name string) (llm.Provider, error) {
		p, ok := providers[name]
		if !ok {
			return nil, fmt.Errorf("provider %q is not configured under llm: %w", name, otogi.ErrInvalid)
		}
		return p, nil
	}
}
