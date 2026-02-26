package llmchat

import (
	"strings"
	"testing"
	"time"
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
				})
			},
			wantErrSubstring: "duplicate agent name",
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

func TestCloneConfigDeepCopiesMaps(t *testing.T) {
	t.Parallel()

	original := validModuleConfig()
	cloned := cloneConfig(original)

	cloned.Agents[0].TemplateVariables["locale"] = "ja-JP"
	cloned.Agents[0].RequestMetadata["gemini.google_search"] = "false"

	if original.Agents[0].TemplateVariables["locale"] != "en-US" {
		t.Fatalf("original template_variables mutated: %+v", original.Agents[0].TemplateVariables)
	}
	if original.Agents[0].RequestMetadata["gemini.google_search"] != "true" {
		t.Fatalf("original request_metadata mutated: %+v", original.Agents[0].RequestMetadata)
	}
}

func validModuleConfig() Config {
	return Config{
		RequestTimeout: time.Second,
		Agents: []Agent{
			{
				Name:                 "Otogi",
				Description:          "primary assistant",
				Provider:             "provider-main",
				Model:                "model-main",
				SystemPromptTemplate: "You are {{.AgentName}}",
				TemplateVariables: map[string]string{
					"locale": "en-US",
				},
				MaxOutputTokens: 256,
				Temperature:     0.2,
				RequestMetadata: map[string]string{
					"gemini.google_search": "true",
				},
			},
		},
	}
}
