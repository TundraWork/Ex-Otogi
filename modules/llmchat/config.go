package llmchat

import (
	"fmt"
	"strings"
	"text/template"
	"time"
)

const (
	metadataKeyAgent          = "agent"
	metadataKeyProvider       = "provider"
	metadataKeyConversationID = "conversation_id"
)

// Config configures llmchat module behavior.
type Config struct {
	// RequestTimeout bounds one LLM request lifecycle.
	RequestTimeout time.Duration
	// Agents is the list of configured triggerable agents.
	Agents []Agent
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
	// RequestMetadata carries provider-agnostic request metadata overrides.
	RequestMetadata map[string]string
}

// Validate checks llmchat config coherence.
func (cfg Config) Validate() error {
	if cfg.RequestTimeout <= 0 {
		return fmt.Errorf("validate llmchat config: request_timeout must be > 0")
	}
	if len(cfg.Agents) == 0 {
		return fmt.Errorf("validate llmchat config: at least one agent is required")
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
	}

	return nil
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
	return validateRequestMetadata(agent.RequestMetadata)
}

func validateRequestMetadata(metadata map[string]string) error {
	for key, value := range metadata {
		if err := validateRequestMetadataEntry(key, value); err != nil {
			return err
		}
	}

	return nil
}

func validateRequestMetadataEntry(key string, value string) error {
	trimmedKey := strings.TrimSpace(key)
	if trimmedKey == "" {
		return fmt.Errorf("request_metadata contains empty key")
	}

	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return fmt.Errorf("request_metadata[%s]: empty value", trimmedKey)
	}

	if isReservedMetadataKey(trimmedKey) {
		return fmt.Errorf("request_metadata[%s]: reserved key", trimmedKey)
	}

	return nil
}

func isReservedMetadataKey(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case metadataKeyAgent, metadataKeyProvider, metadataKeyConversationID:
		return true
	default:
		return false
	}
}

func cloneConfig(cfg Config) Config {
	cloned := cfg
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
				RequestMetadata:      cloneStringMap(agent.RequestMetadata),
			})
		}
	}

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
