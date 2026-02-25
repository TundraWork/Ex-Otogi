package llmchat

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"text/template"
	"time"
)

const (
	defaultRequestTimeout = 90 * time.Second
	providerTypeOpenAI    = "openai"
)

// Config configures llmchat module behavior.
type Config struct {
	// RequestTimeout bounds one LLM request lifecycle.
	RequestTimeout time.Duration
	// Providers is the configured map of provider profiles keyed by profile name.
	Providers map[string]ProviderProfile
	// Agents is the list of configured triggerable agents.
	Agents []Agent
}

// ProviderProfile describes one provider profile referenced by agents.
type ProviderProfile struct {
	// Type identifies provider implementation kind (for example: openai).
	Type string
	// APIKey is the provider credential.
	APIKey string
	// BaseURL optionally overrides provider API endpoint.
	BaseURL string
	// Organization optionally sets provider organization scope.
	Organization string
	// Project optionally sets provider project scope.
	Project string
	// Timeout optionally sets per-request timeout for this profile.
	Timeout *time.Duration
	// MaxRetries optionally sets provider retry count.
	MaxRetries *int
}

// Agent describes one configured chat persona and provider binding.
type Agent struct {
	// Name is the trigger keyword for this agent.
	Name string
	// Description is a short operator-facing explanation for this agent.
	Description string
	// Provider identifies which LLM provider profile to resolve.
	Provider string
	// Model identifies which provider model name to call.
	Model string
	// SystemPromptTemplate is the system prompt template for this agent.
	SystemPromptTemplate string
	// TemplateVariables are additional template variables injected at render time.
	TemplateVariables map[string]string
	// MaxOutputTokens optionally limits generated token count.
	MaxOutputTokens int
	// Temperature optionally controls output randomness.
	Temperature float64
}

type fileConfig struct {
	RequestTimeout string                       `json:"request_timeout"`
	Providers      map[string]fileProviderEntry `json:"providers"`
	Agents         []fileAgent                  `json:"agents"`
}

type fileProviderEntry struct {
	Type         string `json:"type"`
	APIKey       string `json:"api_key"`
	BaseURL      string `json:"base_url"`
	Organization string `json:"organization"`
	Project      string `json:"project"`
	Timeout      string `json:"timeout"`
	MaxRetries   *int   `json:"max_retries"`
}

type fileAgent struct {
	Name                 string            `json:"name"`
	Description          string            `json:"description"`
	Provider             string            `json:"provider"`
	Model                string            `json:"model"`
	SystemPromptTemplate string            `json:"system_prompt_template"`
	TemplateVariables    map[string]string `json:"template_variables"`
	MaxOutputTokens      int               `json:"max_output_tokens"`
	Temperature          float64           `json:"temperature"`
}

// LoadConfigFile reads and validates llmchat module configuration from path.
func LoadConfigFile(path string) (Config, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return Config{}, fmt.Errorf("load llmchat config: empty path")
	}

	data, err := os.ReadFile(trimmed)
	if err != nil {
		return Config{}, fmt.Errorf("load llmchat config read %s: %w", trimmed, err)
	}

	var parsed fileConfig
	if err := json.Unmarshal(data, &parsed); err != nil {
		return Config{}, fmt.Errorf("load llmchat config parse %s: %w", trimmed, err)
	}

	cfg := Config{
		RequestTimeout: defaultRequestTimeout,
		Providers:      make(map[string]ProviderProfile, len(parsed.Providers)),
		Agents:         make([]Agent, 0, len(parsed.Agents)),
	}

	if rawTimeout := strings.TrimSpace(parsed.RequestTimeout); rawTimeout != "" {
		timeout, err := time.ParseDuration(rawTimeout)
		if err != nil {
			return Config{}, fmt.Errorf("load llmchat config parse request_timeout: %w", err)
		}
		if timeout <= 0 {
			return Config{}, fmt.Errorf("load llmchat config parse request_timeout: must be > 0")
		}
		cfg.RequestTimeout = timeout
	}

	for key, rawProvider := range parsed.Providers {
		providerKey := strings.TrimSpace(key)
		if providerKey == "" {
			return Config{}, fmt.Errorf("load llmchat config providers: empty provider key")
		}
		if _, exists := cfg.Providers[providerKey]; exists {
			return Config{}, fmt.Errorf("load llmchat config providers: duplicate provider key %s", providerKey)
		}

		provider, err := parseProviderProfile(rawProvider)
		if err != nil {
			return Config{}, fmt.Errorf("load llmchat config providers[%s]: %w", providerKey, err)
		}
		if err := validateProviderProfile(providerKey, provider); err != nil {
			return Config{}, fmt.Errorf("load llmchat config providers[%s]: %w", providerKey, err)
		}

		cfg.Providers[providerKey] = provider
	}

	for index, rawAgent := range parsed.Agents {
		agent := Agent{
			Name:                 strings.TrimSpace(rawAgent.Name),
			Description:          strings.TrimSpace(rawAgent.Description),
			Provider:             strings.TrimSpace(rawAgent.Provider),
			Model:                strings.TrimSpace(rawAgent.Model),
			SystemPromptTemplate: strings.TrimSpace(rawAgent.SystemPromptTemplate),
			TemplateVariables:    cloneStringMap(rawAgent.TemplateVariables),
			MaxOutputTokens:      rawAgent.MaxOutputTokens,
			Temperature:          rawAgent.Temperature,
		}
		if err := validateAgent(agent); err != nil {
			return Config{}, fmt.Errorf("load llmchat config agents[%d]: %w", index, err)
		}
		cfg.Agents = append(cfg.Agents, agent)
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

// Validate checks llmchat config coherence.
func (cfg Config) Validate() error {
	if cfg.RequestTimeout <= 0 {
		return fmt.Errorf("validate llmchat config: request_timeout must be > 0")
	}
	if len(cfg.Providers) == 0 {
		return fmt.Errorf("validate llmchat config: providers is required")
	}
	if len(cfg.Agents) == 0 {
		return fmt.Errorf("validate llmchat config: at least one agent is required")
	}

	seenProviders := make(map[string]struct{}, len(cfg.Providers))
	for profileKey, profile := range cfg.Providers {
		trimmedKey := strings.TrimSpace(profileKey)
		if trimmedKey == "" {
			return fmt.Errorf("validate llmchat config providers: empty provider key")
		}
		if _, exists := seenProviders[trimmedKey]; exists {
			return fmt.Errorf("validate llmchat config providers: duplicate provider key %s", trimmedKey)
		}
		seenProviders[trimmedKey] = struct{}{}

		if err := validateProviderProfile(trimmedKey, profile); err != nil {
			return fmt.Errorf("validate llmchat config providers[%s]: %w", trimmedKey, err)
		}
	}

	seenNames := make(map[string]struct{}, len(cfg.Agents))
	for index, agent := range cfg.Agents {
		if err := validateAgent(agent); err != nil {
			return fmt.Errorf("validate llmchat config agents[%d]: %w", index, err)
		}

		normalized := normalizeAgentName(agent.Name)
		if _, exists := seenNames[normalized]; exists {
			return fmt.Errorf("validate llmchat config: duplicate agent name %q", agent.Name)
		}
		seenNames[normalized] = struct{}{}

		providerKey := strings.TrimSpace(agent.Provider)
		if _, exists := cfg.Providers[providerKey]; !exists {
			return fmt.Errorf("validate llmchat config agents[%d]: provider %s is not configured", index, providerKey)
		}
	}

	return nil
}

func validateProviderProfile(profileKey string, profile ProviderProfile) error {
	if strings.TrimSpace(profileKey) == "" {
		return fmt.Errorf("empty provider key")
	}

	providerType := strings.ToLower(strings.TrimSpace(profile.Type))
	if providerType == "" {
		return fmt.Errorf("missing type")
	}

	switch providerType {
	case providerTypeOpenAI:
		if strings.TrimSpace(profile.APIKey) == "" {
			return fmt.Errorf("missing api_key")
		}
	default:
		return fmt.Errorf("unsupported type %q", profile.Type)
	}

	if rawBaseURL := strings.TrimSpace(profile.BaseURL); rawBaseURL != "" {
		parsed, err := url.Parse(rawBaseURL)
		if err != nil {
			return fmt.Errorf("invalid base_url: %w", err)
		}
		if parsed.Scheme == "" || parsed.Host == "" {
			return fmt.Errorf("invalid base_url: must include scheme and host")
		}
	}
	if profile.Timeout != nil && *profile.Timeout <= 0 {
		return fmt.Errorf("timeout must be > 0")
	}
	if profile.MaxRetries != nil && *profile.MaxRetries < 0 {
		return fmt.Errorf("max_retries must be >= 0")
	}

	return nil
}

func parseProviderProfile(raw fileProviderEntry) (ProviderProfile, error) {
	profile := ProviderProfile{
		Type:         strings.ToLower(strings.TrimSpace(raw.Type)),
		APIKey:       strings.TrimSpace(raw.APIKey),
		BaseURL:      strings.TrimSpace(raw.BaseURL),
		Organization: strings.TrimSpace(raw.Organization),
		Project:      strings.TrimSpace(raw.Project),
		MaxRetries:   cloneIntPointer(raw.MaxRetries),
	}

	if rawTimeout := strings.TrimSpace(raw.Timeout); rawTimeout != "" {
		timeout, err := time.ParseDuration(rawTimeout)
		if err != nil {
			return ProviderProfile{}, fmt.Errorf("parse timeout: %w", err)
		}
		profile.Timeout = cloneDurationPointer(&timeout)
	}

	return profile, nil
}

func validateAgent(agent Agent) error {
	if strings.TrimSpace(agent.Name) == "" {
		return fmt.Errorf("missing name")
	}
	if strings.TrimSpace(agent.Description) == "" {
		return fmt.Errorf("missing description")
	}
	if strings.TrimSpace(agent.Provider) == "" {
		return fmt.Errorf("missing provider")
	}
	if strings.TrimSpace(agent.Model) == "" {
		return fmt.Errorf("missing model")
	}
	if strings.TrimSpace(agent.SystemPromptTemplate) == "" {
		return fmt.Errorf("missing system_prompt_template")
	}
	if agent.MaxOutputTokens < 0 {
		return fmt.Errorf("max_output_tokens must be >= 0")
	}
	if agent.Temperature < 0 {
		return fmt.Errorf("temperature must be >= 0")
	}
	if _, err := template.New("system-prompt").Option("missingkey=error").Parse(agent.SystemPromptTemplate); err != nil {
		return fmt.Errorf("invalid system_prompt_template: %w", err)
	}

	return nil
}

func cloneConfig(cfg Config) Config {
	cloned := cfg
	if cfg.Providers != nil {
		cloned.Providers = make(map[string]ProviderProfile, len(cfg.Providers))
		for key, provider := range cfg.Providers {
			cloned.Providers[key] = cloneProviderProfile(provider)
		}
	}
	if cfg.Agents != nil {
		cloned.Agents = make([]Agent, 0, len(cfg.Agents))
		for _, agent := range cfg.Agents {
			cloned.Agents = append(cloned.Agents, Agent{
				Name:                 agent.Name,
				Description:          agent.Description,
				Provider:             agent.Provider,
				Model:                agent.Model,
				SystemPromptTemplate: agent.SystemPromptTemplate,
				TemplateVariables:    cloneStringMap(agent.TemplateVariables),
				MaxOutputTokens:      agent.MaxOutputTokens,
				Temperature:          agent.Temperature,
			})
		}
	}

	return cloned
}

func cloneProviderProfile(profile ProviderProfile) ProviderProfile {
	cloned := profile
	cloned.Timeout = cloneDurationPointer(profile.Timeout)
	cloned.MaxRetries = cloneIntPointer(profile.MaxRetries)

	return cloned
}

func normalizeAgentName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}

	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}

	return cloned
}

func cloneDurationPointer(value *time.Duration) *time.Duration {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneIntPointer(value *int) *int {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
