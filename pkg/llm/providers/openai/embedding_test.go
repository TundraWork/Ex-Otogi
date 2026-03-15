package openai

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"ex-otogi/pkg/otogi/ai"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

func TestNewOpenAIEmbeddingProviderConfigValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		cfg              EmbeddingProviderConfig
		wantErrSubstring string
	}{
		{
			name: "valid config",
			cfg: EmbeddingProviderConfig{
				APIKey:            "sk-test",
				BaseURL:           "https://api.openai.com/v1",
				MaxRetries:        ptrInt(1),
				DefaultModel:      "text-embedding-3-small",
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
				APIKey:  "sk-test",
				BaseURL: "not a url",
			},
			wantErrSubstring: "parse base_url",
		},
		{
			name: "negative retries",
			cfg: EmbeddingProviderConfig{
				APIKey:     "sk-test",
				MaxRetries: ptrInt(-1),
			},
			wantErrSubstring: "max_retries",
		},
		{
			name: "negative default dimensions",
			cfg: EmbeddingProviderConfig{
				APIKey:            "sk-test",
				DefaultDimensions: -1,
			},
			wantErrSubstring: "default_dimensions",
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			provider, err := NewEmbeddingProvider(testCase.cfg)
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
				t.Fatalf("NewEmbeddingProvider failed: %v", err)
			}
			if provider == nil {
				t.Fatal("expected provider instance")
			}
		})
	}
}

func TestOpenAIEmbeddingProviderEmbed(t *testing.T) {
	t.Parallel()

	t.Run("successful single text", func(t *testing.T) {
		t.Parallel()

		client := &openAIEmbeddingsClientStub{
			response: &openai.CreateEmbeddingResponse{
				Data: []openai.Embedding{
					{Embedding: []float64{3, 4}},
				},
			},
		}
		provider := &EmbeddingProvider{
			embeddings: client,
			defaults: embeddingDefaults{
				model:      defaultEmbeddingModel,
				dimensions: defaultEmbeddingDimensions,
			},
		}

		resp, err := provider.Embed(context.Background(), ai.EmbeddingRequest{
			Model:      "text-embedding-3-small",
			Texts:      []string{"hello"},
			Dimensions: 256,
			TaskType:   ai.EmbeddingTaskTypeQuery,
		})
		if err != nil {
			t.Fatalf("Embed failed: %v", err)
		}
		if len(resp.Vectors) != 1 {
			t.Fatalf("vector count = %d, want 1", len(resp.Vectors))
		}
		assertFloat32Approx(t, resp.Vectors[0][0], 0.6)
		assertFloat32Approx(t, resp.Vectors[0][1], 0.8)

		if len(client.params) != 1 {
			t.Fatalf("request count = %d, want 1", len(client.params))
		}
		if client.params[0].Model != openai.EmbeddingModel("text-embedding-3-small") {
			t.Fatalf("model = %q, want text-embedding-3-small", client.params[0].Model)
		}
		if !client.params[0].Dimensions.Valid() || client.params[0].Dimensions.Value != 256 {
			t.Fatalf("dimensions = %+v, want 256", client.params[0].Dimensions)
		}
		if len(client.params[0].Input.OfArrayOfStrings) != 1 || client.params[0].Input.OfArrayOfStrings[0] != "hello" {
			t.Fatalf("input = %+v, want [hello]", client.params[0].Input.OfArrayOfStrings)
		}
	})

	t.Run("successful multiple texts", func(t *testing.T) {
		t.Parallel()

		client := &openAIEmbeddingsClientStub{
			response: &openai.CreateEmbeddingResponse{
				Data: []openai.Embedding{
					{Embedding: []float64{1, 0}},
					{Embedding: []float64{0, 2}},
				},
			},
		}
		provider := &EmbeddingProvider{
			embeddings: client,
			defaults: embeddingDefaults{
				model:      defaultEmbeddingModel,
				dimensions: defaultEmbeddingDimensions,
			},
		}

		resp, err := provider.Embed(context.Background(), ai.EmbeddingRequest{
			Model: "text-embedding-3-small",
			Texts: []string{"hello", "world"},
		})
		if err != nil {
			t.Fatalf("Embed failed: %v", err)
		}
		if len(resp.Vectors) != 2 {
			t.Fatalf("vector count = %d, want 2", len(resp.Vectors))
		}
		assertFloat32Approx(t, resp.Vectors[0][0], 1.0)
		assertFloat32Approx(t, resp.Vectors[1][1], 1.0)
	})

	t.Run("empty model falls back to default", func(t *testing.T) {
		t.Parallel()

		client := &openAIEmbeddingsClientStub{
			response: &openai.CreateEmbeddingResponse{
				Data: []openai.Embedding{
					{Embedding: []float64{1, 0}},
				},
			},
		}
		provider := &EmbeddingProvider{
			embeddings: client,
			defaults: embeddingDefaults{
				model:      "custom-default-model",
				dimensions: 384,
			},
		}

		_, err := provider.Embed(context.Background(), ai.EmbeddingRequest{
			Texts: []string{"hello"},
		})
		if err != nil {
			t.Fatalf("Embed failed: %v", err)
		}
		if got := client.params[0].Model; got != openai.EmbeddingModel("custom-default-model") {
			t.Fatalf("model = %q, want custom-default-model", got)
		}
		if !client.params[0].Dimensions.Valid() || client.params[0].Dimensions.Value != 384 {
			t.Fatalf("dimensions = %+v, want 384", client.params[0].Dimensions)
		}
	})

	t.Run("api error propagation", func(t *testing.T) {
		t.Parallel()

		provider := &EmbeddingProvider{
			embeddings: &openAIEmbeddingsClientStub{
				err: errors.New("boom"),
			},
			defaults: embeddingDefaults{
				model:      defaultEmbeddingModel,
				dimensions: defaultEmbeddingDimensions,
			},
		}

		_, err := provider.Embed(context.Background(), ai.EmbeddingRequest{
			Model: "text-embedding-3-small",
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
			embeddings: &openAIEmbeddingsClientStub{
				response: &openai.CreateEmbeddingResponse{
					Data: []openai.Embedding{{Embedding: []float64{1, 0}}},
				},
			},
			defaults: embeddingDefaults{
				model:      defaultEmbeddingModel,
				dimensions: defaultEmbeddingDimensions,
			},
		}

		_, err := provider.Embed(context.Background(), ai.EmbeddingRequest{
			Model: "text-embedding-3-small",
			Texts: []string{"hello", "world"},
		})
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "vector count") {
			t.Fatalf("error = %v, want vector count mismatch", err)
		}
	})

	t.Run("float64 to float32 conversion correctness", func(t *testing.T) {
		t.Parallel()

		provider := &EmbeddingProvider{
			embeddings: &openAIEmbeddingsClientStub{
				response: &openai.CreateEmbeddingResponse{
					Data: []openai.Embedding{
						{Embedding: []float64{1.5, 2.5}},
					},
				},
			},
			defaults: embeddingDefaults{
				model:      defaultEmbeddingModel,
				dimensions: defaultEmbeddingDimensions,
			},
		}

		resp, err := provider.Embed(context.Background(), ai.EmbeddingRequest{
			Model: "text-embedding-3-small",
			Texts: []string{"hello"},
		})
		if err != nil {
			t.Fatalf("Embed failed: %v", err)
		}
		if len(resp.Vectors[0]) != 2 {
			t.Fatalf("vector length = %d, want 2", len(resp.Vectors[0]))
		}
		if math.IsNaN(float64(resp.Vectors[0][0])) {
			t.Fatal("vector contains NaN")
		}
	})
}

type openAIEmbeddingsClientStub struct {
	params   []openai.EmbeddingNewParams
	response *openai.CreateEmbeddingResponse
	err      error
}

func (s *openAIEmbeddingsClientStub) New(
	_ context.Context,
	body openai.EmbeddingNewParams,
	_ ...option.RequestOption,
) (*openai.CreateEmbeddingResponse, error) {
	s.params = append(s.params, body)
	if s.err != nil {
		return nil, s.err
	}
	return s.response, nil
}

func assertFloat32Approx(t *testing.T, got, want float32) {
	t.Helper()

	if math.Abs(float64(got-want)) > 1e-4 {
		t.Fatalf("value = %f, want %f", got, want)
	}
}
