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
							"request_timeout":"30s",
							"request_metadata":{"trace_id":"main"}
						},
						{
							"name":"OtogiGemini",
							"description":"Gemini agent",
							"provider":"gemini-main",
							"model":"gemini-2.5-flash",
							"system_prompt_template":"You are {{.AgentName}}",
							"request_timeout":"20s",
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
				if geminiProfile.Gemini.RequestDefaults.ThinkingBudget != nil {
					t.Fatalf(
						"gemini thinking budget = %v, want nil",
						geminiProfile.Gemini.RequestDefaults.ThinkingBudget,
					)
				}
				if geminiProfile.Gemini.RequestDefaults.ThinkingLevel != "medium" {
					t.Fatalf(
						"gemini thinking level = %q, want medium",
						geminiProfile.Gemini.RequestDefaults.ThinkingLevel,
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
				if cfg.Agents[0].RequestTimeout != 30*time.Second {
					t.Fatalf("agent[0] request_timeout = %s, want 30s", cfg.Agents[0].RequestTimeout)
				}
				if cfg.Agents[1].RequestTimeout != 20*time.Second {
					t.Fatalf("agent[1] request_timeout = %s, want 20s", cfg.Agents[1].RequestTimeout)
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
						"system_prompt_template":"ok","request_timeout":"10s"
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
						"system_prompt_template":"ok","request_timeout":"10s"
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
						"system_prompt_template":"ok","request_timeout":"10s"
					}
				]
			}`,
			wantErrSubstring: "unsupported response_mime_type",
		},
		{
			name: "conflicting gemini provider thinking options",
			fileBody: `{
					"providers":{
						"gemini-main":{
							"type":"gemini",
							"api_key":"gm",
							"gemini":{
								"thinking_budget":64,
								"thinking_level":"high"
							}
						}
					},
					"agents":[
						{
							"name":"Otogi",
							"description":"d",
							"provider":"gemini-main",
							"model":"m",
							"system_prompt_template":"ok","request_timeout":"10s"
						}
					]
				}`,
			wantErrSubstring: "mutually exclusive",
		},
		{
			name: "conflicting gemini agent metadata thinking options",
			fileBody: `{
					"providers":{
						"gemini-main":{
							"type":"gemini",
							"api_key":"gm"
						}
					},
					"agents":[
						{
							"name":"Otogi",
							"description":"d",
							"provider":"gemini-main",
							"model":"m",
							"system_prompt_template":"ok","request_timeout":"10s",
							"request_metadata":{
								"gemini.thinking_budget":"32",
								"gemini.thinking_level":"high"
							}
						}
					]
				}`,
			wantErrSubstring: "sets both gemini.thinking_budget and gemini.thinking_level",
		},
		{
			name: "conflicting gemini defaults and metadata thinking options",
			fileBody: `{
					"providers":{
						"gemini-main":{
							"type":"gemini",
							"api_key":"gm",
							"gemini":{
								"thinking_budget":64
							}
						}
					},
					"agents":[
						{
							"name":"Otogi",
							"description":"d",
							"provider":"gemini-main",
							"model":"m",
							"system_prompt_template":"ok","request_timeout":"10s",
							"request_metadata":{
								"gemini.thinking_level":"high"
							}
						}
					]
				}`,
			wantErrSubstring: "sets both gemini.thinking_budget and gemini.thinking_level",
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
						"system_prompt_template":"ok","request_timeout":"10s"
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
						"system_prompt_template":"ok","request_timeout":"10s"
					}
				]
			}`,
			wantErrSubstring: "max_retries must be >= 0",
		},
		{
			name: "provider timeout field is rejected",
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
						"system_prompt_template":"ok","request_timeout":"10s"
					}
				]
			}`,
			wantErrSubstring: "unknown field \"timeout\"",
		},
		{
			name: "missing agent request timeout",
			fileBody: `{
					"providers":{"openai-main":{"type":"openai","api_key":"sk"}},
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
			wantErrSubstring: "missing request_timeout",
		},
		{
			name: "invalid agent request timeout",
			fileBody: `{
					"providers":{"openai-main":{"type":"openai","api_key":"sk"}},
					"agents":[
						{
							"name":"Otogi",
							"description":"d",
							"provider":"openai-main",
							"model":"m",
							"system_prompt_template":"ok",
							"request_timeout":"bad"
						}
					]
				}`,
			wantErrSubstring: "parse request_timeout",
		},
		{
			name: "agent request timeout cannot exceed global",
			fileBody: `{
					"request_timeout":"5s",
					"providers":{"openai-main":{"type":"openai","api_key":"sk"}},
					"agents":[
						{
							"name":"Otogi",
							"description":"d",
							"provider":"openai-main",
							"model":"m",
							"system_prompt_template":"ok",
							"request_timeout":"6s"
						}
					]
				}`,
			wantErrSubstring: "exceeds global request_timeout",
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
						"system_prompt_template":"ok","request_timeout":"10s"
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
						"system_prompt_template":"ok","request_timeout":"10s"
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
						"system_prompt_template":"ok","request_timeout":"10s"
					},
					{
						"name":"otogi",
						"description":"d2",
						"provider":"openai-main",
						"model":"m2",
						"system_prompt_template":"ok","request_timeout":"10s"
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
						"system_prompt_template":"ok","request_timeout":"10s",
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
						"system_prompt_template":"ok","request_timeout":"10s"
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
