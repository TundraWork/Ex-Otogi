package openai

import (
	"context"
	"fmt"
	"math"
	"net/url"
	"strings"

	"ex-otogi/pkg/otogi/ai"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
)

const (
	defaultEmbeddingModel      = "text-embedding-3-small"
	defaultEmbeddingDimensions = 512
)

// EmbeddingProviderConfig configures one OpenAI-backed embedding provider
// instance.
type EmbeddingProviderConfig struct {
	// APIKey is the credential used to authenticate requests.
	APIKey string
	// BaseURL optionally overrides the OpenAI endpoint.
	BaseURL string
	// Organization optionally sets the OpenAI organization header.
	Organization string
	// Project optionally sets the OpenAI project header.
	Project string
	// MaxRetries optionally overrides the SDK retry count.
	//
	// Nil keeps the SDK default behavior.
	MaxRetries *int
	// DefaultModel is the fallback model used when requests omit Model.
	DefaultModel string
	// DefaultDimensions is the fallback dimension count used when requests omit
	// Dimensions.
	DefaultDimensions int
}

// EmbeddingProvider is an otogi embedding provider backed by OpenAI
// embeddings.
type EmbeddingProvider struct {
	embeddings openAIEmbeddingsClient
	defaults   embeddingDefaults
}

type openAIEmbeddingsClient interface {
	New(ctx context.Context, body openai.EmbeddingNewParams, opts ...option.RequestOption) (*openai.CreateEmbeddingResponse, error)
}

type embeddingDefaults struct {
	model      string
	dimensions int
}

// NewEmbeddingProvider builds one OpenAI embeddings provider instance.
func NewEmbeddingProvider(cfg EmbeddingProviderConfig) (*EmbeddingProvider, error) {
	normalized, err := normalizeEmbeddingProviderConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("new openai embedding provider: %w", err)
	}

	options := make([]option.RequestOption, 0, 6)
	options = append(options, option.WithAPIKey(normalized.APIKey))
	if normalized.BaseURL != "" {
		options = append(options, option.WithBaseURL(normalized.BaseURL))
	}
	if normalized.Organization != "" {
		options = append(options, option.WithOrganization(normalized.Organization))
	}
	if normalized.Project != "" {
		options = append(options, option.WithProject(normalized.Project))
	}
	if normalized.MaxRetries != nil {
		options = append(options, option.WithMaxRetries(*normalized.MaxRetries))
	}

	client := openai.NewClient(options...)
	embeddings := client.Embeddings

	return &EmbeddingProvider{
		embeddings: &embeddings,
		defaults: embeddingDefaults{
			model:      normalized.DefaultModel,
			dimensions: normalized.DefaultDimensions,
		},
	}, nil
}

// Embed generates one embedding vector per input text.
func (p *EmbeddingProvider) Embed(ctx context.Context, req ai.EmbeddingRequest) (ai.EmbeddingResponse, error) {
	if p == nil {
		return ai.EmbeddingResponse{}, fmt.Errorf("openai embed: nil provider")
	}
	if ctx == nil {
		return ai.EmbeddingResponse{}, fmt.Errorf("openai embed: nil context")
	}
	if p.embeddings == nil {
		return ai.EmbeddingResponse{}, fmt.Errorf("openai embed: embeddings client is nil")
	}

	effective, err := p.resolveRequest(req)
	if err != nil {
		return ai.EmbeddingResponse{}, fmt.Errorf("openai embed validate request: %w", err)
	}

	params := openai.EmbeddingNewParams{
		Input: openai.EmbeddingNewParamsInputUnion{
			OfArrayOfStrings: append([]string(nil), effective.Texts...),
		},
		Model: effective.Model,
	}
	if effective.Dimensions > 0 {
		params.Dimensions = param.NewOpt(int64(effective.Dimensions))
	}

	response, err := p.embeddings.New(ctx, params)
	if err != nil {
		return ai.EmbeddingResponse{}, fmt.Errorf("openai embed request: %w", err)
	}
	if response == nil {
		return ai.EmbeddingResponse{}, fmt.Errorf("openai embed request: nil response")
	}
	if len(response.Data) != len(effective.Texts) {
		return ai.EmbeddingResponse{}, fmt.Errorf(
			"openai embed request: response vector count %d does not match texts %d",
			len(response.Data),
			len(effective.Texts),
		)
	}

	vectors := make([][]float32, 0, len(response.Data))
	for index, item := range response.Data {
		vector, convertErr := convertOpenAIEmbedding(item.Embedding)
		if convertErr != nil {
			return ai.EmbeddingResponse{}, fmt.Errorf("openai embed response data[%d]: %w", index, convertErr)
		}
		vectors = append(vectors, vector)
	}

	return ai.EmbeddingResponse{Vectors: vectors}, nil
}

func (p *EmbeddingProvider) resolveRequest(req ai.EmbeddingRequest) (ai.EmbeddingRequest, error) {
	effective := req
	if strings.TrimSpace(effective.Model) == "" {
		effective.Model = p.defaults.model
	}
	if effective.Dimensions == 0 {
		effective.Dimensions = p.defaults.dimensions
	}
	if err := effective.Validate(); err != nil {
		return ai.EmbeddingRequest{}, fmt.Errorf("validate embedding request: %w", err)
	}

	return effective, nil
}

func normalizeEmbeddingProviderConfig(cfg EmbeddingProviderConfig) (EmbeddingProviderConfig, error) {
	cfg.APIKey = strings.TrimSpace(cfg.APIKey)
	cfg.BaseURL = strings.TrimSpace(cfg.BaseURL)
	cfg.Organization = strings.TrimSpace(cfg.Organization)
	cfg.Project = strings.TrimSpace(cfg.Project)
	cfg.DefaultModel = strings.TrimSpace(cfg.DefaultModel)

	if cfg.APIKey == "" {
		return EmbeddingProviderConfig{}, fmt.Errorf("missing api_key")
	}
	if cfg.BaseURL != "" {
		parsed, err := url.Parse(cfg.BaseURL)
		if err != nil {
			return EmbeddingProviderConfig{}, fmt.Errorf("parse base_url: %w", err)
		}
		if parsed.Scheme == "" || parsed.Host == "" {
			return EmbeddingProviderConfig{}, fmt.Errorf("parse base_url: must include scheme and host")
		}
	}
	if cfg.MaxRetries != nil && *cfg.MaxRetries < 0 {
		return EmbeddingProviderConfig{}, fmt.Errorf("max_retries must be >= 0")
	}
	if cfg.DefaultModel == "" {
		cfg.DefaultModel = defaultEmbeddingModel
	}
	if cfg.DefaultDimensions == 0 {
		cfg.DefaultDimensions = defaultEmbeddingDimensions
	}
	if cfg.DefaultDimensions < 0 {
		return EmbeddingProviderConfig{}, fmt.Errorf("default_dimensions must be >= 0")
	}

	return cfg, nil
}

func convertOpenAIEmbedding(vector []float64) ([]float32, error) {
	if len(vector) == 0 {
		return nil, fmt.Errorf("missing embedding values")
	}

	converted := make([]float32, len(vector))
	for index, value := range vector {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return nil, fmt.Errorf("embedding[%d] is not finite", index)
		}
		converted[index] = float32(value)
	}

	return normalizeEmbeddingVector32(converted)
}

func normalizeEmbeddingVector32(vector []float32) ([]float32, error) {
	if len(vector) == 0 {
		return nil, fmt.Errorf("missing embedding values")
	}

	var sumSquares float64
	normalized := make([]float32, len(vector))
	for index, value := range vector {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return nil, fmt.Errorf("embedding[%d] is not finite", index)
		}
		normalized[index] = value
		sumSquares += float64(value) * float64(value)
	}
	if sumSquares == 0 {
		return nil, fmt.Errorf("embedding has zero magnitude")
	}

	scale := 1 / math.Sqrt(sumSquares)
	for index := range normalized {
		normalized[index] = float32(float64(normalized[index]) * scale)
	}

	return normalized, nil
}

var _ ai.EmbeddingProvider = (*EmbeddingProvider)(nil)
