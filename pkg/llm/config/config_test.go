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
				"natural_memory":{
					"enabled":true,
					"extraction_provider":"openai-main",
					"extraction_model":"gpt-4.1-mini",
					"embedding_provider":"openai-main",
					"extraction_timeout":"25s",
					"extraction_max_input_runes":5000,
					"context_window_size":7,
					"synthesis_match_limit":6,
					"consolidation_interval":"2h",
					"consolidation_provider":"gemini-main",
					"consolidation_model":"gemini-2.5-flash",
					"consolidation_timeout":"75s",
					"max_memories_per_scope":250,
					"decay_factor":0.99,
					"min_importance":4,
					"duplicate_similarity_threshold":0.9,
					"reflection_min_source_memories":10,
					"reflection_source_limit":24,
					"reflection_max_generated":4,
					"retrieval_planning_enabled":false,
					"retrieval_planning_timeout":"12s"
				},
					"providers":{
						"openai-main":{
							"type":"openai",
							"api_key":"sk-test",
							"base_url":"https://api.openai.com/v1",
							"embedding_model":"text-embedding-3-small",
							"embedding_dimensions":512,
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
							"embedding_provider":"openai-main",
							"model":"gpt-5-mini",
							"system_prompt_template":"You are {{.AgentName}}",
							"request_timeout":"30s",
							"request_metadata":{"trace_id":"main"},
							"semantic_memory":{
								"enabled":true,
								"max_retrieved_memories":4,
								"min_memory_similarity":0.45,
								"max_memory_runes":1500
							},
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
				if cfg.NaturalMemory == nil {
					t.Fatal("natural_memory = nil, want populated config")
				}
				if !cfg.NaturalMemory.Enabled {
					t.Fatal("natural_memory.enabled = false, want true")
				}
				if cfg.NaturalMemory.ExtractionProvider != "openai-main" {
					t.Fatalf(
						"natural_memory extraction_provider = %q, want openai-main",
						cfg.NaturalMemory.ExtractionProvider,
					)
				}
				if cfg.NaturalMemory.ExtractionModel != "gpt-4.1-mini" {
					t.Fatalf(
						"natural_memory extraction_model = %q, want gpt-4.1-mini",
						cfg.NaturalMemory.ExtractionModel,
					)
				}
				if cfg.NaturalMemory.EmbeddingProvider != "openai-main" {
					t.Fatalf(
						"natural_memory embedding_provider = %q, want openai-main",
						cfg.NaturalMemory.EmbeddingProvider,
					)
				}
				if cfg.NaturalMemory.ExtractionTimeout != 25*time.Second {
					t.Fatalf(
						"natural_memory extraction_timeout = %s, want 25s",
						cfg.NaturalMemory.ExtractionTimeout,
					)
				}
				if cfg.NaturalMemory.ExtractionMaxInputRunes != 5000 {
					t.Fatalf(
						"natural_memory extraction_max_input_runes = %d, want 5000",
						cfg.NaturalMemory.ExtractionMaxInputRunes,
					)
				}
				if cfg.NaturalMemory.ContextWindowSize != 7 {
					t.Fatalf(
						"natural_memory context_window_size = %d, want 7",
						cfg.NaturalMemory.ContextWindowSize,
					)
				}
				if cfg.NaturalMemory.SynthesisMatchLimit != 6 {
					t.Fatalf(
						"natural_memory synthesis_match_limit = %d, want 6",
						cfg.NaturalMemory.SynthesisMatchLimit,
					)
				}
				if cfg.NaturalMemory.ConsolidationInterval != 2*time.Hour {
					t.Fatalf(
						"natural_memory consolidation_interval = %s, want 2h",
						cfg.NaturalMemory.ConsolidationInterval,
					)
				}
				if cfg.NaturalMemory.ConsolidationProvider != "gemini-main" {
					t.Fatalf(
						"natural_memory consolidation_provider = %q, want gemini-main",
						cfg.NaturalMemory.ConsolidationProvider,
					)
				}
				if cfg.NaturalMemory.ConsolidationModel != "gemini-2.5-flash" {
					t.Fatalf(
						"natural_memory consolidation_model = %q, want gemini-2.5-flash",
						cfg.NaturalMemory.ConsolidationModel,
					)
				}
				if cfg.NaturalMemory.ConsolidationTimeout != 75*time.Second {
					t.Fatalf(
						"natural_memory consolidation_timeout = %s, want 75s",
						cfg.NaturalMemory.ConsolidationTimeout,
					)
				}
				if cfg.NaturalMemory.MaxMemoriesPerScope != 250 {
					t.Fatalf(
						"natural_memory max_memories_per_scope = %d, want 250",
						cfg.NaturalMemory.MaxMemoriesPerScope,
					)
				}
				if cfg.NaturalMemory.DecayFactor != 0.99 {
					t.Fatalf("natural_memory decay_factor = %f, want 0.99", cfg.NaturalMemory.DecayFactor)
				}
				if cfg.NaturalMemory.MinImportance != 4 {
					t.Fatalf("natural_memory min_importance = %d, want 4", cfg.NaturalMemory.MinImportance)
				}
				if cfg.NaturalMemory.DuplicateSimilarityThreshold != 0.9 {
					t.Fatalf(
						"natural_memory duplicate_similarity_threshold = %f, want 0.9",
						cfg.NaturalMemory.DuplicateSimilarityThreshold,
					)
				}
				if cfg.NaturalMemory.ReflectionMinSourceMemories != 10 {
					t.Fatalf(
						"natural_memory reflection_min_source_memories = %d, want 10",
						cfg.NaturalMemory.ReflectionMinSourceMemories,
					)
				}
				if cfg.NaturalMemory.ReflectionSourceLimit != 24 {
					t.Fatalf(
						"natural_memory reflection_source_limit = %d, want 24",
						cfg.NaturalMemory.ReflectionSourceLimit,
					)
				}
				if cfg.NaturalMemory.ReflectionMaxGenerated != 4 {
					t.Fatalf(
						"natural_memory reflection_max_generated = %d, want 4",
						cfg.NaturalMemory.ReflectionMaxGenerated,
					)
				}
				if cfg.NaturalMemory.RetrievalPlanningEnabled {
					t.Fatal("natural_memory retrieval_planning_enabled = true, want false")
				}
				if cfg.NaturalMemory.RetrievalPlanningTimeout != 12*time.Second {
					t.Fatalf(
						"natural_memory retrieval_planning_timeout = %s, want 12s",
						cfg.NaturalMemory.RetrievalPlanningTimeout,
					)
				}

				openaiProfile := cfg.Providers["openai-main"]
				if openaiProfile.Type != providerTypeOpenAI {
					t.Fatalf("openai type = %q, want %q", openaiProfile.Type, providerTypeOpenAI)
				}
				if openaiProfile.OpenAI == nil {
					t.Fatal("expected openai options")
				}
				if openaiProfile.EmbeddingModel != "text-embedding-3-small" {
					t.Fatalf("openai embedding_model = %q, want text-embedding-3-small", openaiProfile.EmbeddingModel)
				}
				if openaiProfile.EmbeddingDimensions != 512 {
					t.Fatalf("openai embedding_dimensions = %d, want 512", openaiProfile.EmbeddingDimensions)
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
				if cfg.Agents[0].EmbeddingProvider != "openai-main" {
					t.Fatalf("agent[0] embedding_provider = %q, want openai-main", cfg.Agents[0].EmbeddingProvider)
				}
				if cfg.Agents[0].SemanticMemory == nil || !cfg.Agents[0].SemanticMemory.Enabled {
					t.Fatal("agent[0] semantic_memory = nil/disabled, want enabled")
				}
				if cfg.Agents[0].SemanticMemory.MaxRetrievedMemories != 4 {
					t.Fatalf(
						"agent[0] max_retrieved_memories = %d, want 4",
						cfg.Agents[0].SemanticMemory.MaxRetrievedMemories,
					)
				}
				if cfg.Agents[0].SemanticMemory.MinMemorySimilarity != 0.45 {
					t.Fatalf(
						"agent[0] min_memory_similarity = %f, want 0.45",
						cfg.Agents[0].SemanticMemory.MinMemorySimilarity,
					)
				}
				if cfg.Agents[0].SemanticMemory.MaxMemoryRunes != 1500 {
					t.Fatalf(
						"agent[0] max_memory_runes = %d, want 1500",
						cfg.Agents[0].SemanticMemory.MaxMemoryRunes,
					)
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
			name: "natural memory defaults are applied",
			fileBody: `{
				"providers":{
					"openai-main":{"type":"openai","api_key":"sk-test"},
					"gemini-main":{"type":"gemini","api_key":"gm-test"}
				},
				"natural_memory":{
					"enabled":true,
					"extraction_provider":"openai-main",
					"extraction_model":"gpt-4.1-mini",
					"embedding_provider":"openai-main",
					"consolidation_provider":"gemini-main",
					"consolidation_model":"gemini-2.5-flash"
				},
				"agents":[
					{
						"name":"Otogi",
						"description":"d",
						"provider":"openai-main",
						"model":"m",
						"system_prompt_template":"ok",
						"request_timeout":"10s"
					}
				]
			}`,
			assert: func(t *testing.T, cfg Config) {
				t.Helper()

				if cfg.NaturalMemory == nil {
					t.Fatal("natural_memory = nil, want defaults")
				}
				if cfg.NaturalMemory.ExtractionTimeout != defaultNaturalMemoryExtractionTimeout {
					t.Fatalf(
						"extraction_timeout = %s, want %s",
						cfg.NaturalMemory.ExtractionTimeout,
						defaultNaturalMemoryExtractionTimeout,
					)
				}
				if cfg.NaturalMemory.ExtractionMaxInputRunes != defaultNaturalMemoryExtractionMaxInputRunes {
					t.Fatalf(
						"extraction_max_input_runes = %d, want %d",
						cfg.NaturalMemory.ExtractionMaxInputRunes,
						defaultNaturalMemoryExtractionMaxInputRunes,
					)
				}
				if cfg.NaturalMemory.ContextWindowSize != defaultNaturalMemoryContextWindowSize {
					t.Fatalf(
						"context_window_size = %d, want %d",
						cfg.NaturalMemory.ContextWindowSize,
						defaultNaturalMemoryContextWindowSize,
					)
				}
				if cfg.NaturalMemory.SynthesisMatchLimit != defaultNaturalMemorySynthesisMatchLimit {
					t.Fatalf(
						"synthesis_match_limit = %d, want %d",
						cfg.NaturalMemory.SynthesisMatchLimit,
						defaultNaturalMemorySynthesisMatchLimit,
					)
				}
				if cfg.NaturalMemory.ConsolidationInterval != defaultNaturalMemoryConsolidationInterval {
					t.Fatalf(
						"consolidation_interval = %s, want %s",
						cfg.NaturalMemory.ConsolidationInterval,
						defaultNaturalMemoryConsolidationInterval,
					)
				}
				if cfg.NaturalMemory.ConsolidationTimeout != defaultNaturalMemoryConsolidationTimeout {
					t.Fatalf(
						"consolidation_timeout = %s, want %s",
						cfg.NaturalMemory.ConsolidationTimeout,
						defaultNaturalMemoryConsolidationTimeout,
					)
				}
				if cfg.NaturalMemory.MaxMemoriesPerScope != defaultNaturalMemoryMaxMemoriesPerScope {
					t.Fatalf(
						"max_memories_per_scope = %d, want %d",
						cfg.NaturalMemory.MaxMemoriesPerScope,
						defaultNaturalMemoryMaxMemoriesPerScope,
					)
				}
				if cfg.NaturalMemory.DecayFactor != defaultNaturalMemoryDecayFactor {
					t.Fatalf("decay_factor = %f, want %f", cfg.NaturalMemory.DecayFactor, defaultNaturalMemoryDecayFactor)
				}
				if cfg.NaturalMemory.MinImportance != defaultNaturalMemoryMinImportance {
					t.Fatalf("min_importance = %d, want %d", cfg.NaturalMemory.MinImportance, defaultNaturalMemoryMinImportance)
				}
				if cfg.NaturalMemory.DuplicateSimilarityThreshold != defaultNaturalMemoryDuplicateSimilarityThreshold {
					t.Fatalf(
						"duplicate_similarity_threshold = %f, want %f",
						cfg.NaturalMemory.DuplicateSimilarityThreshold,
						defaultNaturalMemoryDuplicateSimilarityThreshold,
					)
				}
				if cfg.NaturalMemory.ReflectionMinSourceMemories != defaultNaturalMemoryReflectionMinSourceMemories {
					t.Fatalf(
						"reflection_min_source_memories = %d, want %d",
						cfg.NaturalMemory.ReflectionMinSourceMemories,
						defaultNaturalMemoryReflectionMinSourceMemories,
					)
				}
				if cfg.NaturalMemory.ReflectionSourceLimit != defaultNaturalMemoryReflectionSourceLimit {
					t.Fatalf(
						"reflection_source_limit = %d, want %d",
						cfg.NaturalMemory.ReflectionSourceLimit,
						defaultNaturalMemoryReflectionSourceLimit,
					)
				}
				if cfg.NaturalMemory.ReflectionMaxGenerated != defaultNaturalMemoryReflectionMaxGenerated {
					t.Fatalf(
						"reflection_max_generated = %d, want %d",
						cfg.NaturalMemory.ReflectionMaxGenerated,
						defaultNaturalMemoryReflectionMaxGenerated,
					)
				}
				if cfg.NaturalMemory.RetrievalPlanningEnabled != defaultNaturalMemoryRetrievalPlanningEnabled {
					t.Fatalf(
						"retrieval_planning_enabled = %t, want %t",
						cfg.NaturalMemory.RetrievalPlanningEnabled,
						defaultNaturalMemoryRetrievalPlanningEnabled,
					)
				}
				if cfg.NaturalMemory.RetrievalPlanningTimeout != defaultNaturalMemoryRetrievalPlanningTimeout {
					t.Fatalf(
						"retrieval_planning_timeout = %s, want %s",
						cfg.NaturalMemory.RetrievalPlanningTimeout,
						defaultNaturalMemoryRetrievalPlanningTimeout,
					)
				}
			},
		},
		{
			name: "natural memory bad extraction timeout",
			fileBody: `{
				"providers":{"openai-main":{"type":"openai","api_key":"sk-test"}},
				"natural_memory":{
					"enabled":true,
					"extraction_provider":"openai-main",
					"extraction_model":"gpt-4.1-mini",
					"embedding_provider":"openai-main",
					"extraction_timeout":"soon"
				},
				"agents":[
					{
						"name":"Otogi",
						"description":"d",
						"provider":"openai-main",
						"model":"m",
						"system_prompt_template":"ok",
						"request_timeout":"10s"
					}
				]
			}`,
			wantErrSubstring: "natural_memory: parse extraction_timeout",
		},
		{
			name: "natural memory unknown extraction provider",
			fileBody: `{
				"providers":{"openai-main":{"type":"openai","api_key":"sk-test"}},
				"natural_memory":{
					"enabled":true,
					"extraction_provider":"missing",
					"extraction_model":"gpt-4.1-mini",
					"embedding_provider":"openai-main",
					"consolidation_interval":"0s"
				},
				"agents":[
					{
						"name":"Otogi",
						"description":"d",
						"provider":"openai-main",
						"model":"m",
						"system_prompt_template":"ok",
						"request_timeout":"10s"
					}
				]
			}`,
			wantErrSubstring: "natural_memory: extraction_provider missing is not configured",
		},
		{
			name: "natural memory unknown consolidation provider",
			fileBody: `{
				"providers":{"openai-main":{"type":"openai","api_key":"sk-test"}},
				"natural_memory":{
					"enabled":true,
					"extraction_provider":"openai-main",
					"extraction_model":"gpt-4.1-mini",
					"embedding_provider":"openai-main",
					"consolidation_provider":"missing",
					"consolidation_model":"gpt-4.1-mini"
				},
				"agents":[
					{
						"name":"Otogi",
						"description":"d",
						"provider":"openai-main",
						"model":"m",
						"system_prompt_template":"ok",
						"request_timeout":"10s"
					}
				]
			}`,
			wantErrSubstring: "natural_memory: consolidation_provider missing is not configured",
		},
		{
			name: "natural memory bad retrieval planning timeout",
			fileBody: `{
				"providers":{"openai-main":{"type":"openai","api_key":"sk-test"}},
				"natural_memory":{
					"enabled":true,
					"extraction_provider":"openai-main",
					"extraction_model":"gpt-4.1-mini",
					"embedding_provider":"openai-main",
					"consolidation_interval":"0s",
					"retrieval_planning_timeout":"never"
				},
				"agents":[
					{
						"name":"Otogi",
						"description":"d",
						"provider":"openai-main",
						"model":"m",
						"system_prompt_template":"ok",
						"request_timeout":"10s"
					}
				]
			}`,
			wantErrSubstring: "natural_memory: parse retrieval_planning_timeout",
		},
		{
			name: "semantic memory requires embedding provider",
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
						"semantic_memory":{"enabled":true}
					}
				]
			}`,
			wantErrSubstring: "embedding_provider is required when semantic_memory.enabled=true",
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
		{
			name: "valid sub-agent config",
			fileBody: `{
				"providers":{"gemini-main":{"type":"gemini","api_key":"gm"}},
				"agents":[
					{
						"name":"Otogi",
						"description":"d",
						"provider":"gemini-main",
						"model":"gemini-2.5-flash",
						"system_prompt_template":"You are {{.AgentName}}",
						"request_timeout":"30s",
						"sub_agents":[
							{
								"name":"web_search",
								"description":"Search the web",
								"provider":"gemini-main",
								"model":"gemini-2.5-flash",
								"system_prompt":"You are a search assistant.",
								"max_output_tokens":4096,
								"temperature":0.3,
								"request_metadata":{"gemini.google_search":"true"},
								"parameters":{"type":"object","properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties":false},
								"prompt_template":"{{.query}}"
							},
							{
								"name":"read_url",
								"description":"Read a URL",
								"provider":"gemini-main",
								"model":"gemini-2.5-flash",
								"system_prompt":"You are a URL reader.",
								"request_metadata":{"gemini.url_context":"true"},
								"parameters":{"type":"object","properties":{"url":{"type":"string"}},"required":["url"],"additionalProperties":false},
								"prompt_template":"{{.url}}"
							}
						]
					}
				]
			}`,
			assert: func(t *testing.T, cfg Config) {
				t.Helper()

				if len(cfg.Agents[0].SubAgents) != 2 {
					t.Fatalf("sub_agents len = %d, want 2", len(cfg.Agents[0].SubAgents))
				}
				sa := cfg.Agents[0].SubAgents[0]
				if sa.Name != "web_search" {
					t.Fatalf("sub_agent[0] name = %q, want web_search", sa.Name)
				}
				if sa.Provider != "gemini-main" {
					t.Fatalf("sub_agent[0] provider = %q, want gemini-main", sa.Provider)
				}
				if sa.MaxOutputTokens != 4096 {
					t.Fatalf("sub_agent[0] max_output_tokens = %d, want 4096", sa.MaxOutputTokens)
				}
				if sa.Temperature != 0.3 {
					t.Fatalf("sub_agent[0] temperature = %f, want 0.3", sa.Temperature)
				}
				if sa.RequestMetadata["gemini.google_search"] != "true" {
					t.Fatalf("sub_agent[0] metadata = %v, want google_search=true", sa.RequestMetadata)
				}
				if sa.PromptTemplate != "{{.query}}" {
					t.Fatalf("sub_agent[0] prompt_template = %q, want {{.query}}", sa.PromptTemplate)
				}

				sa1 := cfg.Agents[0].SubAgents[1]
				if sa1.Name != "read_url" {
					t.Fatalf("sub_agent[1] name = %q, want read_url", sa1.Name)
				}
			},
		},
		{
			name: "sub-agent missing name",
			fileBody: `{
				"providers":{"gemini-main":{"type":"gemini","api_key":"gm"}},
				"agents":[
					{
						"name":"Otogi",
						"description":"d",
						"provider":"gemini-main",
						"model":"m",
						"system_prompt_template":"ok",
						"request_timeout":"10s",
						"sub_agents":[
							{
								"description":"d",
								"provider":"gemini-main",
								"model":"m",
								"system_prompt":"s",
								"parameters":{"type":"object"},
								"prompt_template":"t"
							}
						]
					}
				]
			}`,
			wantErrSubstring: "invalid name",
		},
		{
			name: "sub-agent remember name is allowed",
			fileBody: `{
				"providers":{"gemini-main":{"type":"gemini","api_key":"gm"}},
				"agents":[
					{
						"name":"Otogi",
						"description":"d",
						"provider":"gemini-main",
						"model":"m",
						"system_prompt_template":"ok",
						"request_timeout":"10s",
						"sub_agents":[
							{
								"name":"remember",
								"description":"d",
								"provider":"gemini-main",
								"model":"m",
								"system_prompt":"s",
								"parameters":{"type":"object"},
								"prompt_template":"t"
							}
						]
					}
				]
			}`,
			assert: func(t *testing.T, cfg Config) {
				t.Helper()

				if len(cfg.Agents) != 1 || len(cfg.Agents[0].SubAgents) != 1 {
					t.Fatalf("sub_agents = %+v, want one remember sub-agent", cfg.Agents)
				}
				if cfg.Agents[0].SubAgents[0].Name != "remember" {
					t.Fatalf("sub_agent name = %q, want remember", cfg.Agents[0].SubAgents[0].Name)
				}
			},
		},
		{
			name: "sub-agent duplicate names",
			fileBody: `{
				"providers":{"gemini-main":{"type":"gemini","api_key":"gm"}},
				"agents":[
					{
						"name":"Otogi",
						"description":"d",
						"provider":"gemini-main",
						"model":"m",
						"system_prompt_template":"ok",
						"request_timeout":"10s",
						"sub_agents":[
							{
								"name":"web_search",
								"description":"d1",
								"provider":"gemini-main",
								"model":"m",
								"system_prompt":"s",
								"parameters":{"type":"object"},
								"prompt_template":"t"
							},
							{
								"name":"Web_Search",
								"description":"d2",
								"provider":"gemini-main",
								"model":"m",
								"system_prompt":"s",
								"parameters":{"type":"object"},
								"prompt_template":"t"
							}
						]
					}
				]
			}`,
			wantErrSubstring: "duplicate sub-agent name",
		},
		{
			name: "sub-agent unknown provider",
			fileBody: `{
				"providers":{"gemini-main":{"type":"gemini","api_key":"gm"}},
				"agents":[
					{
						"name":"Otogi",
						"description":"d",
						"provider":"gemini-main",
						"model":"m",
						"system_prompt_template":"ok",
						"request_timeout":"10s",
						"sub_agents":[
							{
								"name":"web_search",
								"description":"d",
								"provider":"missing",
								"model":"m",
								"system_prompt":"s",
								"parameters":{"type":"object"},
								"prompt_template":"t"
							}
						]
					}
				]
			}`,
			wantErrSubstring: "provider missing is not configured",
		},
		{
			name: "sub-agent parameters not an object",
			fileBody: `{
				"providers":{"gemini-main":{"type":"gemini","api_key":"gm"}},
				"agents":[
					{
						"name":"Otogi",
						"description":"d",
						"provider":"gemini-main",
						"model":"m",
						"system_prompt_template":"ok",
						"request_timeout":"10s",
						"sub_agents":[
							{
								"name":"web_search",
								"description":"d",
								"provider":"gemini-main",
								"model":"m",
								"system_prompt":"s",
								"parameters":"not-json-object",
								"prompt_template":"t"
							}
						]
					}
				]
			}`,
			wantErrSubstring: "parameters must be a json object",
		},
		{
			name: "sub-agent invalid prompt template",
			fileBody: `{
				"providers":{"gemini-main":{"type":"gemini","api_key":"gm"}},
				"agents":[
					{
						"name":"Otogi",
						"description":"d",
						"provider":"gemini-main",
						"model":"m",
						"system_prompt_template":"ok",
						"request_timeout":"10s",
						"sub_agents":[
							{
								"name":"web_search",
								"description":"d",
								"provider":"gemini-main",
								"model":"m",
								"system_prompt":"s",
								"parameters":{"type":"object"},
								"prompt_template":"{{.bad"
							}
						]
					}
				]
			}`,
			wantErrSubstring: "invalid prompt_template",
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
