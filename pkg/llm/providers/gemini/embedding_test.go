package gemini

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"ex-otogi/pkg/otogi/ai"

	"google.golang.org/genai"
)

func TestNewGeminiEmbeddingProviderConfigValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		cfg              EmbeddingProviderConfig
		wantErrSubstring string
	}{
		{
			name: "valid config",
			cfg: EmbeddingProviderConfig{
				APIKey:            "gm-test",
				BaseURL:           "https://generativelanguage.googleapis.com/",
				APIVersion:        "v1beta",
				DefaultModel:      "gemini-embedding-001",
				DefaultDimensions: 512,
			},
		},
		{
			name:             "missing api key",
			cfg:              EmbeddingProviderConfig{},
			wantErrSubstring: "missing api_key",
		},
		{
			name: "invalid base url",
			cfg: EmbeddingProviderConfig{
				APIKey:  "gm-test",
				BaseURL: "not a url",
			},
			wantErrSubstring: "parse base_url",
		},
		{
			name: "invalid api version",
			cfg: EmbeddingProviderConfig{
				APIKey:     "gm-test",
				APIVersion: "v1 beta",
			},
			wantErrSubstring: "invalid api_version",
		},
		{
			name: "negative default dimensions",
			cfg: EmbeddingProviderConfig{
				APIKey:            "gm-test",
				DefaultDimensions: -1,
			},
			wantErrSubstring: "default_dimensions",
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			provider, err := normalizeEmbeddingProviderConfig(testCase.cfg)
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
				t.Fatalf("normalizeEmbeddingProviderConfig failed: %v", err)
			}
			if provider.APIKey == "" {
				t.Fatal("expected normalized config")
			}
		})
	}
}

func TestNewGeminiEmbeddingProviderNilContext(t *testing.T) {
	t.Parallel()

	var nilCtx context.Context
	_, err := NewEmbeddingProvider(nilCtx, EmbeddingProviderConfig{APIKey: "gm-test"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "nil context") {
		t.Fatalf("error = %v, want nil context error", err)
	}
}

func TestGeminiEmbeddingProviderEmbed(t *testing.T) {
	t.Parallel()

	t.Run("successful single text", func(t *testing.T) {
		t.Parallel()

		client := &geminiModelsEmbeddingClientStub{
			response: &genai.EmbedContentResponse{
				Embeddings: []*genai.ContentEmbedding{
					{Values: []float32{3, 4}},
				},
			},
		}
		provider := &EmbeddingProvider{
			models: client,
			defaults: embeddingDefaults{
				model:      defaultEmbeddingModel,
				dimensions: defaultEmbeddingDimensions,
			},
		}

		resp, err := provider.Embed(context.Background(), ai.EmbeddingRequest{
			Model:      "gemini-embedding-001",
			Texts:      []string{"hello"},
			Dimensions: 256,
			TaskType:   ai.EmbeddingTaskTypeDocument,
		})
		if err != nil {
			t.Fatalf("Embed failed: %v", err)
		}
		if len(resp.Vectors) != 1 {
			t.Fatalf("vector count = %d, want 1", len(resp.Vectors))
		}
		assertGeminiFloat32Approx(t, resp.Vectors[0][0], 0.6)
		assertGeminiFloat32Approx(t, resp.Vectors[0][1], 0.8)

		if len(client.calls) != 1 {
			t.Fatalf("call count = %d, want 1", len(client.calls))
		}
		call := client.calls[0]
		if call.model != "gemini-embedding-001" {
			t.Fatalf("model = %q, want gemini-embedding-001", call.model)
		}
		if len(call.contents) != 1 || len(call.contents[0].Parts) != 1 || call.contents[0].Parts[0].Text != "hello" {
			t.Fatalf("contents = %+v, want hello text content", call.contents)
		}
		if call.config == nil {
			t.Fatal("expected embed config")
		}
		if call.config.TaskType != geminiTaskTypeDocument {
			t.Fatalf("task type = %q, want %q", call.config.TaskType, geminiTaskTypeDocument)
		}
		if call.config.OutputDimensionality == nil || *call.config.OutputDimensionality != 256 {
			t.Fatalf("dimensions = %v, want 256", call.config.OutputDimensionality)
		}
	})

	t.Run("successful multiple texts", func(t *testing.T) {
		t.Parallel()

		provider := &EmbeddingProvider{
			models: &geminiModelsEmbeddingClientStub{
				response: &genai.EmbedContentResponse{
					Embeddings: []*genai.ContentEmbedding{
						{Values: []float32{1, 0}},
						{Values: []float32{0, 2}},
					},
				},
			},
			defaults: embeddingDefaults{
				model:      defaultEmbeddingModel,
				dimensions: defaultEmbeddingDimensions,
			},
		}

		resp, err := provider.Embed(context.Background(), ai.EmbeddingRequest{
			Model: "gemini-embedding-001",
			Texts: []string{"hello", "world"},
		})
		if err != nil {
			t.Fatalf("Embed failed: %v", err)
		}
		if len(resp.Vectors) != 2 {
			t.Fatalf("vector count = %d, want 2", len(resp.Vectors))
		}
		assertGeminiFloat32Approx(t, resp.Vectors[0][0], 1.0)
		assertGeminiFloat32Approx(t, resp.Vectors[1][1], 1.0)
	})

	t.Run("empty model falls back to default", func(t *testing.T) {
		t.Parallel()

		client := &geminiModelsEmbeddingClientStub{
			response: &genai.EmbedContentResponse{
				Embeddings: []*genai.ContentEmbedding{
					{Values: []float32{1, 0}},
				},
			},
		}
		provider := &EmbeddingProvider{
			models: client,
			defaults: embeddingDefaults{
				model:      "custom-default-model",
				dimensions: 384,
			},
		}

		_, err := provider.Embed(context.Background(), ai.EmbeddingRequest{
			Texts:    []string{"hello"},
			TaskType: ai.EmbeddingTaskTypeQuery,
		})
		if err != nil {
			t.Fatalf("Embed failed: %v", err)
		}
		call := client.calls[0]
		if call.model != "custom-default-model" {
			t.Fatalf("model = %q, want custom-default-model", call.model)
		}
		if call.config.OutputDimensionality == nil || *call.config.OutputDimensionality != 384 {
			t.Fatalf("dimensions = %v, want 384", call.config.OutputDimensionality)
		}
		if call.config.TaskType != geminiTaskTypeQuery {
			t.Fatalf("task type = %q, want %q", call.config.TaskType, geminiTaskTypeQuery)
		}
	})

	t.Run("api error propagation", func(t *testing.T) {
		t.Parallel()

		provider := &EmbeddingProvider{
			models: &geminiModelsEmbeddingClientStub{
				err: errors.New("boom"),
			},
			defaults: embeddingDefaults{
				model:      defaultEmbeddingModel,
				dimensions: defaultEmbeddingDimensions,
			},
		}

		_, err := provider.Embed(context.Background(), ai.EmbeddingRequest{
			Model: "gemini-embedding-001",
			Texts: []string{"hello"},
		})
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "boom") {
			t.Fatalf("error = %v, want propagated API error", err)
		}
	})

	t.Run("vector count mismatch detection", func(t *testing.T) {
		t.Parallel()

		provider := &EmbeddingProvider{
			models: &geminiModelsEmbeddingClientStub{
				response: &genai.EmbedContentResponse{
					Embeddings: []*genai.ContentEmbedding{{Values: []float32{1, 0}}},
				},
			},
			defaults: embeddingDefaults{
				model:      defaultEmbeddingModel,
				dimensions: defaultEmbeddingDimensions,
			},
		}

		_, err := provider.Embed(context.Background(), ai.EmbeddingRequest{
			Model: "gemini-embedding-001",
			Texts: []string{"hello", "world"},
		})
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "vector count") {
			t.Fatalf("error = %v, want vector count mismatch", err)
		}
	})
}

type geminiEmbeddingCall struct {
	model    string
	contents []*genai.Content
	config   *genai.EmbedContentConfig
}

type geminiModelsEmbeddingClientStub struct {
	calls    []geminiEmbeddingCall
	response *genai.EmbedContentResponse
	err      error
}

func (s *geminiModelsEmbeddingClientStub) EmbedContent(
	_ context.Context,
	model string,
	contents []*genai.Content,
	config *genai.EmbedContentConfig,
) (*genai.EmbedContentResponse, error) {
	s.calls = append(s.calls, geminiEmbeddingCall{
		model:    model,
		contents: contents,
		config:   config,
	})
	if s.err != nil {
		return nil, s.err
	}
	return s.response, nil
}

func assertGeminiFloat32Approx(t *testing.T, got, want float32) {
	t.Helper()

	if math.Abs(float64(got-want)) > 1e-4 {
		t.Fatalf("value = %f, want %f", got, want)
	}
}
