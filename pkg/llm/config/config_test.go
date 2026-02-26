package config

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

func TestLoadFile(t *testing.T) {
	tests := []struct {
		name             string
		fileBody         string
		wantErrSubstring string
		assert           func(*testing.T, Config)
	}{
		{
			name: "valid openai and gemini config",
			fileBody: `{
				"request_timeout":"45s",
				"providers":{
					"openai-main":{
						"type":"openai",
						"api_key":"sk-test",
						"base_url":"https://api.openai.com/v1",
						"timeout":"20s",
						"openai":{
							"organization":"org-test",
							"project":"project-test",
							"max_retries":3
						}
					},
					"gemini-main":{
						"type":"gemini",
						"api_key":"gm-test",
						"gemini":{
							"api_version":"v1beta",
							"google_search":true,
							"url_context":false,
							"thinking_budget":128,
							"include_thoughts":false,
							"thinking_level":"medium",
							"response_mime_type":"application/json"
						}
					}
				},
				"agents":[
					{
						"name":"Otogi",
						"description":"OpenAI agent",
						"provider":"openai-main",
						"model":"gpt-5-mini",
						"system_prompt_template":"You are {{.AgentName}}",
						"request_metadata":{"trace_id":"main"}
					},
					{
						"name":"OtogiGemini",
						"description":"Gemini agent",
						"provider":"gemini-main",
						"model":"gemini-2.5-flash",
						"system_prompt_template":"You are {{.AgentName}}",
						"request_metadata":{"gemini.thinking_level":"high"}
					}
				]
			}`,
			assert: func(t *testing.T, cfg Config) {
				t.Helper()

				if cfg.RequestTimeout != 45*time.Second {
					t.Fatalf("request timeout = %s, want 45s", cfg.RequestTimeout)
				}
				if len(cfg.Providers) != 2 {
					t.Fatalf("providers len = %d, want 2", len(cfg.Providers))
				}

				openaiProfile := cfg.Providers["openai-main"]
				if openaiProfile.Type != providerTypeOpenAI {
					t.Fatalf("openai type = %q, want %q", openaiProfile.Type, providerTypeOpenAI)
				}
				if openaiProfile.OpenAI == nil {
					t.Fatal("expected openai options")
				}
				if openaiProfile.OpenAI.MaxRetries == nil || *openaiProfile.OpenAI.MaxRetries != 3 {
					t.Fatalf("openai max retries = %v, want 3", openaiProfile.OpenAI.MaxRetries)
				}
				if openaiProfile.Timeout == nil || *openaiProfile.Timeout != 20*time.Second {
					t.Fatalf("openai timeout = %v, want 20s", openaiProfile.Timeout)
				}

				geminiProfile := cfg.Providers["gemini-main"]
				if geminiProfile.Type != providerTypeGemini {
					t.Fatalf("gemini type = %q, want %q", geminiProfile.Type, providerTypeGemini)
				}
				if geminiProfile.Gemini == nil {
					t.Fatal("expected gemini options")
				}
				if geminiProfile.Gemini.APIVersion != "v1beta" {
					t.Fatalf("gemini api version = %q, want v1beta", geminiProfile.Gemini.APIVersion)
				}
				if geminiProfile.Gemini.RequestDefaults.ThinkingBudget == nil ||
					*geminiProfile.Gemini.RequestDefaults.ThinkingBudget != 128 {
					t.Fatalf(
						"gemini thinking budget = %v, want 128",
						geminiProfile.Gemini.RequestDefaults.ThinkingBudget,
					)
				}
				if geminiProfile.Gemini.RequestDefaults.ResponseMIMEType != "application/json" {
					t.Fatalf(
						"gemini response_mime_type = %q, want application/json",
						geminiProfile.Gemini.RequestDefaults.ResponseMIMEType,
					)
				}

				if len(cfg.Agents) != 2 {
					t.Fatalf("agents len = %d, want 2", len(cfg.Agents))
				}
				if cfg.Agents[1].RequestMetadata["gemini.thinking_level"] != "high" {
					t.Fatalf(
						"agent request_metadata gemini.thinking_level = %q, want high",
						cfg.Agents[1].RequestMetadata["gemini.thinking_level"],
					)
				}
			},
		},
		{
			name: "unsupported provider type",
			fileBody: `{
				"providers":{"anthropic-main":{"type":"anthropic","api_key":"x"}},
				"agents":[
					{
						"name":"Otogi",
						"description":"d",
						"provider":"anthropic-main",
						"model":"m",
						"system_prompt_template":"ok"
					}
				]
			}`,
			wantErrSubstring: "unsupported type",
		},
		{
			name: "invalid gemini thinking level",
			fileBody: `{
				"providers":{
					"gemini-main":{
						"type":"gemini",
						"api_key":"gm",
						"gemini":{"thinking_level":"minimal"}
					}
				},
				"agents":[
					{
						"name":"Otogi",
						"description":"d",
						"provider":"gemini-main",
						"model":"m",
						"system_prompt_template":"ok"
					}
				]
			}`,
			wantErrSubstring: "unsupported thinking_level",
		},
		{
			name: "invalid gemini response mime type",
			fileBody: `{
				"providers":{
					"gemini-main":{
						"type":"gemini",
						"api_key":"gm",
						"gemini":{"response_mime_type":"application/xml"}
					}
				},
				"agents":[
					{
						"name":"Otogi",
						"description":"d",
						"provider":"gemini-main",
						"model":"m",
						"system_prompt_template":"ok"
					}
				]
			}`,
			wantErrSubstring: "unsupported response_mime_type",
		},
		{
			name: "invalid gemini api version",
			fileBody: `{
				"providers":{
					"gemini-main":{
						"type":"gemini",
						"api_key":"gm",
						"gemini":{"api_version":"v1 beta"}
					}
				},
				"agents":[
					{
						"name":"Otogi",
						"description":"d",
						"provider":"gemini-main",
						"model":"m",
						"system_prompt_template":"ok"
					}
				]
			}`,
			wantErrSubstring: "invalid api_version",
		},
		{
			name: "invalid openai max retries",
			fileBody: `{
				"providers":{
					"openai-main":{
						"type":"openai",
						"api_key":"sk",
						"openai":{"max_retries":-1}
					}
				},
				"agents":[
					{
						"name":"Otogi",
						"description":"d",
						"provider":"openai-main",
						"model":"m",
						"system_prompt_template":"ok"
					}
				]
			}`,
			wantErrSubstring: "max_retries must be >= 0",
		},
		{
			name: "invalid provider timeout",
			fileBody: `{
				"providers":{
					"openai-main":{"type":"openai","api_key":"sk","timeout":"bad"}
				},
				"agents":[
					{
						"name":"Otogi",
						"description":"d",
						"provider":"openai-main",
						"model":"m",
						"system_prompt_template":"ok"
					}
				]
			}`,
			wantErrSubstring: "parse timeout",
		},
		{
			name: "unknown provider referenced by agent",
			fileBody: `{
				"providers":{"openai-main":{"type":"openai","api_key":"sk"}},
				"agents":[
					{
						"name":"Otogi",
						"description":"d",
						"provider":"missing",
						"model":"m",
						"system_prompt_template":"ok"
					}
				]
			}`,
			wantErrSubstring: "provider missing is not configured",
		},
		{
			name: "duplicate provider keys rejected",
			fileBody: `{
				"providers":{
					"openai-main":{"type":"openai","api_key":"sk-1"},
					"openai-main":{"type":"openai","api_key":"sk-2"}
				},
				"agents":[
					{
						"name":"Otogi",
						"description":"d",
						"provider":"openai-main",
						"model":"m",
						"system_prompt_template":"ok"
					}
				]
			}`,
			wantErrSubstring: "duplicate provider key",
		},
		{
			name: "duplicate agent names rejected",
			fileBody: `{
				"providers":{"openai-main":{"type":"openai","api_key":"sk"}},
				"agents":[
					{
						"name":"Otogi",
						"description":"d",
						"provider":"openai-main",
						"model":"m",
						"system_prompt_template":"ok"
					},
					{
						"name":"otogi",
						"description":"d2",
						"provider":"openai-main",
						"model":"m2",
						"system_prompt_template":"ok"
					}
				]
			}`,
			wantErrSubstring: "duplicate agent name",
		},
		{
			name: "strict unknown field rejects agents gemini block",
			fileBody: `{
				"providers":{"openai-main":{"type":"openai","api_key":"sk"}},
				"agents":[
					{
						"name":"Otogi",
						"description":"d",
						"provider":"openai-main",
						"model":"m",
						"system_prompt_template":"ok",
						"gemini":{"thinking_level":"high"}
					}
				]
			}`,
			wantErrSubstring: "unknown field \"gemini\"",
		},
		{
			name: "strict unknown field rejects old openai fields at provider root",
			fileBody: `{
				"providers":{
					"openai-main":{
						"type":"openai",
						"api_key":"sk",
						"organization":"org-legacy"
					}
				},
				"agents":[
					{
						"name":"Otogi",
						"description":"d",
						"provider":"openai-main",
						"model":"m",
						"system_prompt_template":"ok"
					}
				]
			}`,
			wantErrSubstring: "unknown field \"organization\"",
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			path := writeLLMConfigFile(t, testCase.fileBody)
			cfg, err := LoadFile(path)
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
				t.Fatalf("LoadFile failed: %v", err)
			}
			if testCase.assert != nil {
				testCase.assert(t, cfg)
			}
		})
	}
}
