package main

import (
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/url"
	"regexp"
	"slices"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/gemini"
	"github.com/TundraWork/Ex-Otogi/internal/adapter/openai"
	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// llmSettings is the language model providers modules can use, the
// system:llm section. Each provider is optional; a module names the one it
// uses.
type llmSettings struct {
	Gemini *geminiSettings           `json:"gemini,omitempty" doc:"Google Gemini, which modules name gemini."`
	OpenAI map[string]openAISettings `json:"openai,omitempty" doc:"Services that speak the OpenAI Chat Completions API, such as DeepSeek, each under the provider name modules use: lowercase letters, digits, - and _."`
}

type geminiSettings struct {
	APIKey  otogi.Secret `json:"api_key" doc:"The Gemini API key."`
	BaseURL string       `json:"base_url,omitempty" default:"https://generativelanguage.googleapis.com/v1beta" doc:"The API endpoint, with or without its version. A proxy or gateway that speaks the Gemini Interactions API works too."`
}

type openAISettings struct {
	BaseURL string       `json:"base_url" doc:"The API's base, without /chat/completions, for example https://api.deepseek.com or https://api.openai.com/v1."`
	APIKey  otogi.Secret `json:"api_key" doc:"The key that authenticates requests."`
}

// providerName is what an OpenAI-compatible service may be called.
var providerName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// validate checks what the schema cannot say: the names and the addresses.
func (c llmSettings) validate() []error {
	var errs []error
	if g := c.Gemini; g != nil && g.BaseURL != "" && !httpURL(g.BaseURL) {
		errs = append(errs, &otogi.SettingError{Path: "gemini.base_url", Err: fmt.Errorf("%q: want an absolute http(s) URL", g.BaseURL)})
	}
	for _, name := range slices.Sorted(maps.Keys(c.OpenAI)) {
		switch {
		case !providerName.MatchString(name):
			errs = append(errs, &otogi.SettingError{Path: "openai." + name, Err: errors.New("want a name of lowercase letters, digits, - and _")})
		case name == gemini.Name:
			errs = append(errs, &otogi.SettingError{Path: "openai." + name, Err: errors.New("the name is the Gemini provider's")})
		}
		if o := c.OpenAI[name]; !httpURL(o.BaseURL) {
			errs = append(errs, &otogi.SettingError{Path: "openai." + name + ".base_url", Err: fmt.Errorf("%q: want an absolute http(s) URL", o.BaseURL)})
		}
	}
	return errs
}

// httpURL reports an absolute http or https URL.
func httpURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// buildProviders constructs the providers of the settings, by name, with
// the keys secret holds. It does no I/O.
func buildProviders(c llmSettings, secret func(name string) (string, bool), recorder otogi.Recorder, log *slog.Logger) (map[string]llm.Provider, error) {
	providers := map[string]llm.Provider{}
	if g := c.Gemini; g != nil {
		key, err := g.APIKey.Resolve(secret)
		if err != nil {
			return nil, &otogi.SettingError{Path: "gemini.api_key", Err: err}
		}
		p, err := gemini.New(gemini.Config{APIKey: key, BaseURL: g.BaseURL, Recorder: recorder, Logger: log.With("component", "llm:"+gemini.Name)})
		if err != nil {
			return nil, fmt.Errorf("gemini: %w", err)
		}
		providers[gemini.Name] = p
	}
	for _, name := range slices.Sorted(maps.Keys(c.OpenAI)) {
		o := c.OpenAI[name]
		key, err := o.APIKey.Resolve(secret)
		if err != nil {
			return nil, &otogi.SettingError{Path: "openai." + name + ".api_key", Err: err}
		}
		p, err := openai.New(openai.Config{Name: name, BaseURL: o.BaseURL, APIKey: key, Recorder: recorder, Logger: log.With("component", "llm:"+name)})
		if err != nil {
			return nil, fmt.Errorf("openai.%s: %w", name, err)
		}
		providers[name] = p
	}
	return providers, nil
}

// applyLLM builds the providers of the settings and puts them behind
// provider. Modules hold the providers they were built with, so the caller
// rebuilds those; undo puts the providers there were back.
func (y *system) applyLLM(c llmSettings, secret func(name string) (string, bool)) (undo func(), err error) {
	if err := errors.Join(c.validate()...); err != nil {
		return nil, err
	}
	providers, err := buildProviders(c, secret, y.recorder, y.log)
	if err != nil {
		return nil, err
	}
	old := y.providers.Swap(&providers)
	return func() { y.providers.Store(old) }, nil
}

// provider finds a provider a module's settings name. The module refuses
// one that is not set at the field that names it.
func (y *system) provider(name string) (llm.Provider, error) {
	p, ok := (*y.providers.Load())[name]
	if !ok {
		return nil, fmt.Errorf("provider %q is not among the language model providers set in the panel", name)
	}
	return p, nil
}

// The language model providers as otogi.yaml had them, before they moved to
// the panel: read once, to import them (see loadSections).

// LLMConfig is the file's llm key.
type LLMConfig struct {
	Gemini *GeminiConfig           `yaml:"gemini"`
	OpenAI map[string]OpenAIConfig `yaml:"openai"`
}

// GeminiConfig is the file's llm.gemini.
type GeminiConfig struct {
	APIKey  string `yaml:"api_key"`
	BaseURL string `yaml:"base_url"`
}

// OpenAIConfig is one service under the file's llm.openai.
type OpenAIConfig struct {
	BaseURL string `yaml:"base_url"`
	APIKey  string `yaml:"api_key"`
}

// settings is the file's providers as settings; key makes the reference a
// provider's key is stored as.
func (c LLMConfig) settings(key func(provider, written string) otogi.Secret) llmSettings {
	var out llmSettings
	if g := c.Gemini; g != nil {
		out.Gemini = &geminiSettings{APIKey: key(gemini.Name, g.APIKey), BaseURL: g.BaseURL}
	}
	for name, o := range c.OpenAI {
		if out.OpenAI == nil {
			out.OpenAI = map[string]openAISettings{}
		}
		out.OpenAI[name] = openAISettings{BaseURL: o.BaseURL, APIKey: key(name, o.APIKey)}
	}
	return out
}

// apiKey is a provider's key as the file has it.
func (c LLMConfig) apiKey(provider string) string {
	if provider == gemini.Name && c.Gemini != nil {
		return c.Gemini.APIKey
	}
	return c.OpenAI[provider].APIKey
}

func (c LLMConfig) validate() []error {
	var errs []error
	if c.Gemini != nil && c.Gemini.APIKey == "" {
		errs = append(errs, errors.New("llm.gemini.api_key is required"))
	}
	for _, name := range slices.Sorted(maps.Keys(c.OpenAI)) {
		if c.OpenAI[name].APIKey == "" {
			errs = append(errs, fmt.Errorf("llm.openai.%s.api_key is required", name))
		}
	}
	for _, err := range c.settings(func(_, written string) otogi.Secret { return otogi.Secret(written) }).validate() {
		errs = append(errs, fmt.Errorf("llm.%w", err))
	}
	return errs
}
