package llmchat

import (
	"strings"
	"testing"
	"time"

	"ex-otogi/pkg/otogi/ai"
)

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name             string
		mutate           func(*Config)
		wantErrSubstring string
	}{
		{
			name: "valid config",
		},
		{
			name: "request timeout must be positive",
			mutate: func(cfg *Config) {
				cfg.RequestTimeout = 0
			},
			wantErrSubstring: "request_timeout must be > 0",
		},
		{
			name: "at least one agent required",
			mutate: func(cfg *Config) {
				cfg.Agents = nil
			},
			wantErrSubstring: "at least one agent is required",
		},
		{
			name: "duplicate agent names are rejected",
			mutate: func(cfg *Config) {
				cfg.Agents = append(cfg.Agents, Agent{
					Name:                 "otogi",
					Description:          "secondary",
					Provider:             "p2",
					Model:                "m2",
					SystemPromptTemplate: "You are {{.AgentName}}",
					RequestTimeout:       500 * time.Millisecond,
				})
			},
			wantErrSubstring: "duplicate agent name",
		},
		{
			name: "duplicate agent alias is rejected",
			mutate: func(cfg *Config) {
				cfg.Agents = append(cfg.Agents, Agent{
					Name:                 "Gemini",
					Aliases:              []string{"otogi"},
					Description:          "secondary",
					Provider:             "p2",
					Model:                "m2",
					SystemPromptTemplate: "You are {{.AgentName}}",
					RequestTimeout:       500 * time.Millisecond,
				})
			},
			wantErrSubstring: "duplicate agent name",
		},
		{
			name: "empty alias fails",
			mutate: func(cfg *Config) {
				cfg.Agents[0].Aliases = []string{" "}
			},
			wantErrSubstring: "aliases[0]: empty value",
		},
		{
			name: "agent request timeout cannot exceed global",
			mutate: func(cfg *Config) {
				cfg.Agents[0].RequestTimeout = 2 * time.Second
			},
			wantErrSubstring: "exceeds global request_timeout",
		},
		{
			name: "missing provider fails",
			mutate: func(cfg *Config) {
				cfg.Agents[0].Provider = ""
			},
			wantErrSubstring: "missing provider",
		},
		{
			name: "invalid system prompt template fails",
			mutate: func(cfg *Config) {
				cfg.Agents[0].SystemPromptTemplate = "{{.Missing"
			},
			wantErrSubstring: "invalid system_prompt_template",
		},
		{
			name: "negative max output tokens fails",
			mutate: func(cfg *Config) {
				cfg.Agents[0].MaxOutputTokens = -1
			},
			wantErrSubstring: "max_output_tokens must be >= 0",
		},
		{
			name: "negative temperature fails",
			mutate: func(cfg *Config) {
				cfg.Agents[0].Temperature = -0.1
			},
			wantErrSubstring: "temperature must be >= 0",
		},
		{
			name: "request metadata empty key fails",
			mutate: func(cfg *Config) {
				cfg.Agents[0].RequestMetadata = map[string]string{"": "true"}
			},
			wantErrSubstring: "request_metadata contains empty key",
		},
		{
			name: "request metadata empty value fails",
			mutate: func(cfg *Config) {
				cfg.Agents[0].RequestMetadata = map[string]string{"gemini.google_search": " "}
			},
			wantErrSubstring: "empty value",
		},
		{
			name: "request metadata reserved key agent fails",
			mutate: func(cfg *Config) {
				cfg.Agents[0].RequestMetadata = map[string]string{"agent": "override"}
			},
			wantErrSubstring: "reserved key",
		},
		{
			name: "request metadata reserved key provider fails",
			mutate: func(cfg *Config) {
				cfg.Agents[0].RequestMetadata = map[string]string{"provider": "override"}
			},
			wantErrSubstring: "reserved key",
		},
		{
			name: "request metadata reserved key conversation id fails",
			mutate: func(cfg *Config) {
				cfg.Agents[0].RequestMetadata = map[string]string{"conversation_id": "override"}
			},
			wantErrSubstring: "reserved key",
		},
		{
			name: "negative leading context messages fails",
			mutate: func(cfg *Config) {
				cfg.Agents[0].ContextPolicy.LeadingContextMessages = -1
			},
			wantErrSubstring: "leading_context_messages must be >= 0",
		},
		{
			name: "max message runes cannot exceed context budget",
			mutate: func(cfg *Config) {
				cfg.Agents[0].ContextPolicy.MaxContextRunes = 100
				cfg.Agents[0].ContextPolicy.MaxMessageRunes = 101
			},
			wantErrSubstring: "max_message_runes must be <= max_context_runes",
		},
		{
			name: "image inputs defaults are valid when enabled",
			mutate: func(cfg *Config) {
				cfg.Agents[0].ImageInputs.Enabled = true
			},
		},
		{
			name: "image inputs detail invalid",
			mutate: func(cfg *Config) {
				cfg.Agents[0].ImageInputs = ImageInputPolicy{
					Enabled: true,
					Detail:  ai.LLMInputImageDetail("extreme"),
				}
			},
			wantErrSubstring: "image_inputs: detail",
		},
		{
			name: "image inputs require enabled",
			mutate: func(cfg *Config) {
				cfg.Agents[0].ImageInputs.MaxImages = 1
			},
			wantErrSubstring: "image_inputs: max_images requires enabled=true",
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			cfg := validModuleConfig()
			if testCase.mutate != nil {
				testCase.mutate(&cfg)
			}

			err := cfg.Validate()
			if testCase.wantErrSubstring != "" {
				if err == nil {
					t.Fatal("expected error")
				}
				if !strings.Contains(err.Error(), testCase.wantErrSubstring) {
					t.Fatalf("error = %v, want substring %q", err, testCase.wantErrSubstring)
				}
				return
			}
			if err != nil {
				t.Fatalf("Validate failed: %v", err)
			}
		})
	}
}

func TestCloneConfigDeepCopiesMapsAndAliases(t *testing.T) {
	t.Parallel()

	original := validModuleConfig()
	cloned := cloneConfig(original)

	cloned.Agents[0].Aliases[0] = "Assistant"
	cloned.Agents[0].TemplateVariables["locale"] = "ja-JP"
	cloned.Agents[0].RequestMetadata["gemini.google_search"] = "false"

	if original.Agents[0].Aliases[0] != "Oto" {
		t.Fatalf("original aliases mutated: %+v", original.Agents[0].Aliases)
	}
	if original.Agents[0].TemplateVariables["locale"] != "en-US" {
		t.Fatalf("original template_variables mutated: %+v", original.Agents[0].TemplateVariables)
	}
	if original.Agents[0].RequestMetadata["gemini.google_search"] != "true" {
		t.Fatalf("original request_metadata mutated: %+v", original.Agents[0].RequestMetadata)
	}
}

func TestResolveContextPolicyDefaults(t *testing.T) {
	t.Parallel()

	policy := resolveContextPolicy(ContextPolicy{})

	if policy.ReplyChainMaxMessages != defaultReplyChainMaxMessages {
		t.Fatalf("reply_chain_max_messages = %d, want %d", policy.ReplyChainMaxMessages, defaultReplyChainMaxMessages)
	}
	if policy.LeadingContextMessages != defaultLeadingContextMessages {
		t.Fatalf(
			"leading_context_messages = %d, want %d",
			policy.LeadingContextMessages,
			defaultLeadingContextMessages,
		)
	}
	if policy.LeadingContextMaxAge != defaultLeadingContextMaxAge {
		t.Fatalf("leading_context_max_age = %s, want %s", policy.LeadingContextMaxAge, defaultLeadingContextMaxAge)
	}
	if policy.MaxContextRunes != defaultMaxContextRunes {
		t.Fatalf("max_context_runes = %d, want %d", policy.MaxContextRunes, defaultMaxContextRunes)
	}
	if policy.MaxMessageRunes != defaultMaxMessageRunes {
		t.Fatalf("max_message_runes = %d, want %d", policy.MaxMessageRunes, defaultMaxMessageRunes)
	}
}

func TestResolveImageInputPolicyDefaults(t *testing.T) {
	t.Parallel()

	policy := resolveImageInputPolicy(ImageInputPolicy{Enabled: true})

	if policy.MaxImages != defaultImageInputMaxImages {
		t.Fatalf("max_images = %d, want %d", policy.MaxImages, defaultImageInputMaxImages)
	}
	if policy.MaxImageBytes != defaultImageInputMaxBytes {
		t.Fatalf("max_image_bytes = %d, want %d", policy.MaxImageBytes, defaultImageInputMaxBytes)
	}
	if policy.MaxTotalBytes != defaultImageInputMaxTotalBytes {
		t.Fatalf("max_total_bytes = %d, want %d", policy.MaxTotalBytes, defaultImageInputMaxTotalBytes)
	}
	if policy.Detail != ai.LLMInputImageDetailAuto {
		t.Fatalf("detail = %q, want %q", policy.Detail, ai.LLMInputImageDetailAuto)
	}
}

func validModuleConfig() Config {
	return Config{
		RequestTimeout: time.Second,
		Agents: []Agent{
			{
				Name:                 "Otogi",
				Aliases:              []string{"Oto"},
				Description:          "primary assistant",
				Provider:             "provider-main",
				Model:                "model-main",
				SystemPromptTemplate: "You are {{.AgentName}}",
				TemplateVariables: map[string]string{
					"locale": "en-US",
				},
				MaxOutputTokens: 256,
				Temperature:     0.2,
				RequestTimeout:  900 * time.Millisecond,
				RequestMetadata: map[string]string{
					"gemini.google_search": "true",
				},
			},
		},
	}
}
