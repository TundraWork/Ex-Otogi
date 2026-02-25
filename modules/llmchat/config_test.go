package llmchat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeLLMConfigFile(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "llm.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write llm config file failed: %v", err)
	}

	return path
}

func TestLoadConfigFile(t *testing.T) {
	tests := []struct {
		name             string
		fileBody         string
		wantErrSubstring string
		assert           func(*testing.T, Config)
	}{
		{
			name: "valid typed providers config",
			fileBody: `{
				"request_timeout":"45s",
				"providers":{
					"openai-main":{
						"type":"openai",
						"api_key":"sk-test",
						"base_url":"https://api.openai.com/v1",
						"organization":"org-test",
						"project":"project-test",
						"timeout":"20s",
						"max_retries":3
					}
				},
				"agents":[
					{
						"name":"Otogi",
						"description":"General chat",
						"provider":"openai-main",
						"model":"gpt-5-mini",
						"system_prompt_template":"You are {{.AgentName}} at {{.DateTimeUTC}}",
						"template_variables":{"language":"en"},
						"max_output_tokens":256,
						"temperature":0.2
					}
				]
			}`,
			assert: func(t *testing.T, cfg Config) {
				t.Helper()

				if cfg.RequestTimeout != 45*time.Second {
					t.Fatalf("request timeout = %s, want 45s", cfg.RequestTimeout)
				}
				if len(cfg.Providers) != 1 {
					t.Fatalf("providers len = %d, want 1", len(cfg.Providers))
				}
				provider, exists := cfg.Providers["openai-main"]
				if !exists {
					t.Fatal("expected openai-main provider")
				}
				if provider.Type != "openai" {
					t.Fatalf("provider type = %q, want openai", provider.Type)
				}
				if provider.APIKey != "sk-test" {
					t.Fatalf("provider api key = %q, want sk-test", provider.APIKey)
				}
				if provider.Timeout == nil || *provider.Timeout != 20*time.Second {
					t.Fatalf("provider timeout = %v, want 20s", provider.Timeout)
				}
				if provider.MaxRetries == nil || *provider.MaxRetries != 3 {
					t.Fatalf("provider max retries = %v, want 3", provider.MaxRetries)
				}

				if len(cfg.Agents) != 1 {
					t.Fatalf("agents len = %d, want 1", len(cfg.Agents))
				}
				agent := cfg.Agents[0]
				if agent.Provider != "openai-main" {
					t.Fatalf("agent provider = %q, want openai-main", agent.Provider)
				}
			},
		},
		{
			name: "missing providers fails",
			fileBody: `{
				"agents":[
					{"name":"Otogi","description":"d","provider":"openai-main","model":"m","system_prompt_template":"ok"}
				]
			}`,
			wantErrSubstring: "providers is required",
		},
		{
			name: "agent references unknown provider",
			fileBody: `{
				"providers":{
					"openai-main":{"type":"openai","api_key":"sk-test"}
				},
				"agents":[
					{"name":"Otogi","description":"d","provider":"missing","model":"m","system_prompt_template":"ok"}
				]
			}`,
			wantErrSubstring: "provider missing is not configured",
		},
		{
			name: "unsupported provider type",
			fileBody: `{
				"providers":{
					"anthropic-main":{"type":"anthropic","api_key":"x"}
				},
				"agents":[
					{"name":"Otogi","description":"d","provider":"anthropic-main","model":"m","system_prompt_template":"ok"}
				]
			}`,
			wantErrSubstring: "unsupported type",
		},
		{
			name: "missing openai api key",
			fileBody: `{
				"providers":{
					"openai-main":{"type":"openai"}
				},
				"agents":[
					{"name":"Otogi","description":"d","provider":"openai-main","model":"m","system_prompt_template":"ok"}
				]
			}`,
			wantErrSubstring: "missing api_key",
		},
		{
			name: "invalid provider timeout",
			fileBody: `{
				"providers":{
					"openai-main":{"type":"openai","api_key":"x","timeout":"bad"}
				},
				"agents":[
					{"name":"Otogi","description":"d","provider":"openai-main","model":"m","system_prompt_template":"ok"}
				]
			}`,
			wantErrSubstring: "parse timeout",
		},
		{
			name: "invalid provider max retries",
			fileBody: `{
				"providers":{
					"openai-main":{"type":"openai","api_key":"x","max_retries":-1}
				},
				"agents":[
					{"name":"Otogi","description":"d","provider":"openai-main","model":"m","system_prompt_template":"ok"}
				]
			}`,
			wantErrSubstring: "max_retries must be >= 0",
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			path := writeLLMConfigFile(t, testCase.fileBody)
			cfg, err := LoadConfigFile(path)
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
				t.Fatalf("LoadConfigFile failed: %v", err)
			}
			if testCase.assert != nil {
				testCase.assert(t, cfg)
			}
		})
	}
}
