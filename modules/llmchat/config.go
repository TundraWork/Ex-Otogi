package llmchat

import (
	"fmt"
	"strings"
	"text/template"
	"time"

	"ex-otogi/pkg/otogi"
)

const (
	metadataKeyAgent          = "agent"
	metadataKeyProvider       = "provider"
	metadataKeyConversationID = "conversation_id"

	defaultReplyChainMaxMessages   = 12
	defaultLeadingContextMessages  = 4
	defaultLeadingContextMaxAge    = 15 * time.Minute
	defaultMaxContextRunes         = 12000
	defaultMaxMessageRunes         = 1600
	defaultQuoteReplyDepth         = 2
	defaultImageInputMaxImages     = 3
	defaultImageInputMaxBytes      = 10 << 20
	defaultImageInputMaxTotalBytes = 20 << 20
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
	// RequestTimeout bounds one LLM request lifecycle for this agent.
	RequestTimeout time.Duration
	// RequestMetadata carries provider-agnostic request metadata overrides.
	RequestMetadata map[string]string
	// ContextPolicy controls how llmchat reconstructs and trims conversation
	// context before sending it to one provider.
	ContextPolicy ContextPolicy
	// ImageInputs controls whether llmchat downloads current-event images and
	// includes them as multimodal user input.
	//
	// v1 reads images from the live request context only: the current event,
	// selected reply-thread entries, and selected leading-context entries.
	// Historical memory-only lookups remain out of scope until memory identity
	// carries Source.ID.
	ImageInputs ImageInputPolicy
}

// ContextPolicy controls how one agent builds structured conversation context.
type ContextPolicy struct {
	// ReplyChainMaxMessages caps how many reply-chain entries can participate in
	// one request, including the current trigger message.
	ReplyChainMaxMessages int
	// LeadingContextMessages caps how many messages immediately preceding the
	// thread root can be included as background context.
	LeadingContextMessages int
	// LeadingContextMaxAge bounds how old background messages can be relative to
	// the thread root.
	LeadingContextMaxAge time.Duration
	// MaxContextRunes caps the approximate size of serialized contextual payloads
	// that llmchat adds before the current message.
	MaxContextRunes int
	// MaxMessageRunes caps the serialized size of any single article included in
	// context.
	MaxMessageRunes int
	// QuoteReplyDepth controls how many levels of reply_to references are
	// resolved and inlined as quoted context when the referenced message is not
	// already present in the conversation context. 0 disables quoting.
	QuoteReplyDepth int
}

// ImageInputPolicy controls how one agent reads current-event image attachments.
type ImageInputPolicy struct {
	// Enabled turns on current-event image download and multimodal input.
	Enabled bool
	// MaxImages caps how many images from the current event can be attached.
	MaxImages int
	// MaxImageBytes caps any one downloaded image size in bytes.
	MaxImageBytes int64
	// MaxTotalBytes caps total downloaded image bytes across one request.
	MaxTotalBytes int64
	// Detail hints desired provider-side visual fidelity when supported.
	Detail otogi.LLMInputImageDetail
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
		if agent.RequestTimeout > cfg.RequestTimeout {
			return fmt.Errorf(
				"validate llmchat config agents[%d]: request_timeout %s exceeds global request_timeout %s",
				index,
				agent.RequestTimeout,
				cfg.RequestTimeout,
			)
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
	if agent.RequestTimeout <= 0 {
		return fmt.Errorf("request_timeout must be > 0")
	}
	if _, err := template.New("system-prompt").Option("missingkey=error").Parse(agent.SystemPromptTemplate); err != nil {
		return fmt.Errorf("invalid system_prompt_template: %w", err)
	}
	if err := validateContextPolicy(resolveContextPolicy(agent.ContextPolicy)); err != nil {
		return fmt.Errorf("context_policy: %w", err)
	}
	if err := validateImageInputPolicy(resolveImageInputPolicy(agent.ImageInputs)); err != nil {
		return fmt.Errorf("image_inputs: %w", err)
	}
	return validateRequestMetadata(agent.RequestMetadata)
}

func resolveContextPolicy(policy ContextPolicy) ContextPolicy {
	resolved := policy
	if resolved.ReplyChainMaxMessages == 0 {
		resolved.ReplyChainMaxMessages = defaultReplyChainMaxMessages
	}
	if resolved.LeadingContextMessages == 0 {
		resolved.LeadingContextMessages = defaultLeadingContextMessages
	}
	if resolved.LeadingContextMaxAge == 0 {
		resolved.LeadingContextMaxAge = defaultLeadingContextMaxAge
	}
	if resolved.MaxContextRunes == 0 {
		resolved.MaxContextRunes = defaultMaxContextRunes
	}
	if resolved.MaxMessageRunes == 0 {
		resolved.MaxMessageRunes = defaultMaxMessageRunes
	}

	return resolved
}

func validateContextPolicy(policy ContextPolicy) error {
	if policy.ReplyChainMaxMessages <= 0 {
		return fmt.Errorf("reply_chain_max_messages must be > 0")
	}
	if policy.LeadingContextMessages < 0 {
		return fmt.Errorf("leading_context_messages must be >= 0")
	}
	if policy.LeadingContextMaxAge <= 0 {
		return fmt.Errorf("leading_context_max_age must be > 0")
	}
	if policy.MaxContextRunes <= 0 {
		return fmt.Errorf("max_context_runes must be > 0")
	}
	if policy.MaxMessageRunes <= 0 {
		return fmt.Errorf("max_message_runes must be > 0")
	}
	if policy.MaxMessageRunes > policy.MaxContextRunes {
		return fmt.Errorf("max_message_runes must be <= max_context_runes")
	}
	if policy.QuoteReplyDepth < 0 {
		return fmt.Errorf("quote_reply_depth must be >= 0")
	}

	return nil
}

func resolveImageInputPolicy(policy ImageInputPolicy) ImageInputPolicy {
	resolved := policy
	if !resolved.Enabled {
		return resolved
	}
	if resolved.MaxImages == 0 {
		resolved.MaxImages = defaultImageInputMaxImages
	}
	if resolved.MaxImageBytes == 0 {
		resolved.MaxImageBytes = defaultImageInputMaxBytes
	}
	if resolved.MaxTotalBytes == 0 {
		resolved.MaxTotalBytes = defaultImageInputMaxTotalBytes
	}
	if resolved.Detail == "" {
		resolved.Detail = otogi.LLMInputImageDetailAuto
	}

	return resolved
}

func validateImageInputPolicy(policy ImageInputPolicy) error {
	if !policy.Enabled {
		if policy.MaxImages != 0 {
			return fmt.Errorf("max_images requires enabled=true")
		}
		if policy.MaxImageBytes != 0 {
			return fmt.Errorf("max_image_bytes requires enabled=true")
		}
		if policy.MaxTotalBytes != 0 {
			return fmt.Errorf("max_total_bytes requires enabled=true")
		}
		if policy.Detail != "" {
			return fmt.Errorf("detail requires enabled=true")
		}

		return nil
	}
	if policy.MaxImages <= 0 {
		return fmt.Errorf("max_images must be > 0")
	}
	if policy.MaxImageBytes <= 0 {
		return fmt.Errorf("max_image_bytes must be > 0")
	}
	if policy.MaxTotalBytes <= 0 {
		return fmt.Errorf("max_total_bytes must be > 0")
	}
	if policy.MaxTotalBytes < policy.MaxImageBytes {
		return fmt.Errorf("max_total_bytes must be >= max_image_bytes")
	}
	if err := policy.Detail.Validate(); err != nil {
		return fmt.Errorf("detail: %w", err)
	}

	return nil
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
				RequestTimeout:       agent.RequestTimeout,
				RequestMetadata:      cloneStringMap(agent.RequestMetadata),
				ContextPolicy:        resolveContextPolicy(agent.ContextPolicy),
				ImageInputs:          resolveImageInputPolicy(agent.ImageInputs),
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
