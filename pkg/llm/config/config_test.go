package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ex-otogi/pkg/otogi/ai"
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
								"response_mime_type":"application/json",
								"safety_filter_off":true
							}
					}
				},
				"agents":[
						{
							"name":"Otogi",
							"aliases":["Oto"],
							"description":"OpenAI agent",
							"provider":"openai-main",
							"model":"gpt-5-mini",
							"system_prompt_template":"You are {{.AgentName}}",
							"request_timeout":"30s",
							"request_metadata":{"trace_id":"main"},
							"context":{
								"reply_chain_max_messages":8,
								"leading_context_messages":2,
								"leading_context_max_age":"5m",
								"max_context_runes":6000,
								"max_message_runes":1200
							},
							"image_inputs":{
								"enabled":true,
								"max_images":2,
								"max_image_bytes":1048576,
								"max_total_bytes":2097152,
								"detail":"high"
							}
						},
						{
							"name":"OtogiGemini",
							"aliases":["OtoGemini"],
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
				if geminiProfile.Gemini.RequestDefaults.SafetyFilterOff == nil ||
					!*geminiProfile.Gemini.RequestDefaults.SafetyFilterOff {
					t.Fatalf(
						"gemini safety_filter_off = %v, want true",
						geminiProfile.Gemini.RequestDefaults.SafetyFilterOff,
					)
				}

				if len(cfg.Agents) != 2 {
					t.Fatalf("agents len = %d, want 2", len(cfg.Agents))
				}
				if len(cfg.Agents[0].Aliases) != 1 || cfg.Agents[0].Aliases[0] != "Oto" {
					t.Fatalf("agent[0] aliases = %+v, want [Oto]", cfg.Agents[0].Aliases)
				}
				if cfg.Agents[0].RequestTimeout != 30*time.Second {
					t.Fatalf("agent[0] request_timeout = %s, want 30s", cfg.Agents[0].RequestTimeout)
				}
				if cfg.Agents[0].ContextPolicy.ReplyChainMaxMessages != 8 {
					t.Fatalf(
						"agent[0] reply_chain_max_messages = %d, want 8",
						cfg.Agents[0].ContextPolicy.ReplyChainMaxMessages,
					)
				}
				if cfg.Agents[0].ContextPolicy.LeadingContextMessages != 2 {
					t.Fatalf(
						"agent[0] leading_context_messages = %d, want 2",
						cfg.Agents[0].ContextPolicy.LeadingContextMessages,
					)
				}
				if cfg.Agents[0].ContextPolicy.LeadingContextMaxAge != 5*time.Minute {
					t.Fatalf(
						"agent[0] leading_context_max_age = %s, want 5m",
						cfg.Agents[0].ContextPolicy.LeadingContextMaxAge,
					)
				}
				if cfg.Agents[0].ContextPolicy.MaxContextRunes != 6000 {
					t.Fatalf(
						"agent[0] max_context_runes = %d, want 6000",
						cfg.Agents[0].ContextPolicy.MaxContextRunes,
					)
				}
				if cfg.Agents[0].ContextPolicy.MaxMessageRunes != 1200 {
					t.Fatalf(
						"agent[0] max_message_runes = %d, want 1200",
						cfg.Agents[0].ContextPolicy.MaxMessageRunes,
					)
				}
				if !cfg.Agents[0].ImageInputs.Enabled {
					t.Fatal("agent[0] image_inputs.enabled = false, want true")
				}
				if cfg.Agents[0].ImageInputs.MaxImages != 2 {
					t.Fatalf("agent[0] max_images = %d, want 2", cfg.Agents[0].ImageInputs.MaxImages)
				}
				if cfg.Agents[0].ImageInputs.MaxImageBytes != 1048576 {
					t.Fatalf(
						"agent[0] max_image_bytes = %d, want 1048576",
						cfg.Agents[0].ImageInputs.MaxImageBytes,
					)
				}
				if cfg.Agents[0].ImageInputs.MaxTotalBytes != 2097152 {
					t.Fatalf(
						"agent[0] max_total_bytes = %d, want 2097152",
						cfg.Agents[0].ImageInputs.MaxTotalBytes,
					)
				}
				if cfg.Agents[0].ImageInputs.Detail != ai.LLMInputImageDetailHigh {
					t.Fatalf(
						"agent[0] detail = %q, want %q",
						cfg.Agents[0].ImageInputs.Detail,
						ai.LLMInputImageDetailHigh,
					)
				}
				if cfg.Agents[1].RequestTimeout != 20*time.Second {
					t.Fatalf("agent[1] request_timeout = %s, want 20s", cfg.Agents[1].RequestTimeout)
				}
				if len(cfg.Agents[1].Aliases) != 1 || cfg.Agents[1].Aliases[0] != "OtoGemini" {
					t.Fatalf("agent[1] aliases = %+v, want [OtoGemini]", cfg.Agents[1].Aliases)
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
			name: "invalid context max age",
			fileBody: `{
				"providers":{"openai-main":{"type":"openai","api_key":"sk-test"}},
				"agents":[
					{
						"name":"Otogi",
						"description":"d",
						"provider":"openai-main",
						"model":"m",
						"system_prompt_template":"ok",
						"request_timeout":"10s",
						"context":{"leading_context_max_age":"soon"}
					}
				]
			}`,
			wantErrSubstring: "parse context: parse leading_context_max_age",
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
			name: "invalid image input detail",
			fileBody: `{
				"providers":{"openai-main":{"type":"openai","api_key":"sk-test"}},
				"agents":[
					{
						"name":"Otogi",
						"description":"d",
						"provider":"openai-main",
						"model":"m",
						"system_prompt_template":"ok",
						"request_timeout":"10s",
						"image_inputs":{"enabled":true,"detail":"ultra"}
					}
				]
			}`,
			wantErrSubstring: "image_inputs: detail",
		},
		{
			name: "image input fields require enablement",
			fileBody: `{
				"providers":{"openai-main":{"type":"openai","api_key":"sk-test"}},
				"agents":[
					{
						"name":"Otogi",
						"description":"d",
						"provider":"openai-main",
						"model":"m",
						"system_prompt_template":"ok",
						"request_timeout":"10s",
						"image_inputs":{"max_images":1}
					}
				]
			}`,
			wantErrSubstring: "image_inputs: max_images requires enabled=true",
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
			name: "duplicate agent alias rejected",
			fileBody: `{
				"providers":{"openai-main":{"type":"openai","api_key":"sk"}},
				"agents":[
					{
						"name":"Otogi",
						"aliases":["Oto"],
						"description":"d",
						"provider":"openai-main",
						"model":"m",
						"system_prompt_template":"ok","request_timeout":"10s"
					},
					{
						"name":"Gemini",
						"aliases":["oto"],
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
