package gemini

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"net/url"
	"strings"

	"ex-otogi/pkg/otogi/ai"

	"google.golang.org/genai"
)

const (
	defaultEmbeddingModel      = "gemini-embedding-001"
	defaultEmbeddingDimensions = 512
	geminiTaskTypeDocument     = "RETRIEVAL_DOCUMENT"
	geminiTaskTypeQuery        = "RETRIEVAL_QUERY"
	geminiTaskTypeSimilarity   = "SEMANTIC_SIMILARITY"
)

// EmbeddingProviderConfig configures one Gemini-backed embedding provider
// instance.
type EmbeddingProviderConfig struct {
	// APIKey is the credential used to authenticate requests.
	APIKey string
	// BaseURL optionally overrides the Gemini endpoint.
	BaseURL string
	// APIVersion optionally overrides Gemini API version.
	//
	// Zero defaults to v1beta.
	APIVersion string
	// DefaultModel is the fallback model used when requests omit Model.
	DefaultModel string
	// DefaultDimensions is the fallback dimension count used when requests omit
	// Dimensions.
	DefaultDimensions int
	// Logger receives provider-level diagnostics for Gemini API failures.
	//
	// Nil defaults to slog.Default().
	Logger *slog.Logger
}

// EmbeddingProvider is an otogi embedding provider backed by Google Gemini
// embeddings.
type EmbeddingProvider struct {
	models   geminiModelsEmbeddingClient
	defaults embeddingDefaults
	logger   *slog.Logger
}

type geminiModelsEmbeddingClient interface {
	EmbedContent(
		ctx context.Context,
		model string,
		contents []*genai.Content,
		config *genai.EmbedContentConfig,
	) (*genai.EmbedContentResponse, error)
}

type embeddingDefaults struct {
	model      string
	dimensions int
}

// NewEmbeddingProvider builds one Gemini embeddings provider instance.
func NewEmbeddingProvider(ctx context.Context, cfg EmbeddingProviderConfig) (*EmbeddingProvider, error) {
	if ctx == nil {
		return nil, fmt.Errorf("new gemini embedding provider: nil context")
	}

	normalized, err := normalizeEmbeddingProviderConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("new gemini embedding provider: %w", err)
	}

	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  normalized.APIKey,
		Backend: genai.BackendGeminiAPI,
		HTTPOptions: genai.HTTPOptions{
			BaseURL:    normalized.BaseURL,
			APIVersion: normalized.APIVersion,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("new gemini client: %w", err)
	}
	if client == nil || client.Models == nil {
		return nil, fmt.Errorf("new gemini client: models client is nil")
	}

	return &EmbeddingProvider{
		models: client.Models,
		defaults: embeddingDefaults{
			model:      normalized.DefaultModel,
			dimensions: normalized.DefaultDimensions,
		},
		logger: resolveLogger(cfg.Logger),
	}, nil
}

// Embed generates one embedding vector per input text.
func (p *EmbeddingProvider) Embed(ctx context.Context, req ai.EmbeddingRequest) (ai.EmbeddingResponse, error) {
	if p == nil {
		return ai.EmbeddingResponse{}, fmt.Errorf("gemini embed: nil provider")
	}
	if ctx == nil {
		return ai.EmbeddingResponse{}, fmt.Errorf("gemini embed: nil context")
	}
	if p.models == nil {
		return ai.EmbeddingResponse{}, fmt.Errorf("gemini embed: models client is nil")
	}

	effective, err := p.resolveRequest(req)
	if err != nil {
		return ai.EmbeddingResponse{}, fmt.Errorf("gemini embed validate request: %w", err)
	}

	contents := make([]*genai.Content, 0, len(effective.Texts))
	for _, text := range effective.Texts {
		contents = append(contents, genai.NewContentFromText(text, genai.RoleUser))
	}
	dimensions, err := safeInt32(effective.Dimensions)
	if err != nil {
		return ai.EmbeddingResponse{}, fmt.Errorf("gemini embed validate request: %w", err)
	}
	config := &genai.EmbedContentConfig{
		TaskType:             mapEmbeddingTaskType(effective.TaskType),
		OutputDimensionality: &dimensions,
	}

	response, err := p.models.EmbedContent(ctx, strings.TrimSpace(effective.Model), contents, config)
	if err != nil {
		return ai.EmbeddingResponse{}, fmt.Errorf("gemini embed request: %w", err)
	}
	if response == nil {
		return ai.EmbeddingResponse{}, fmt.Errorf("gemini embed request: nil response")
	}
	if len(response.Embeddings) != len(effective.Texts) {
		return ai.EmbeddingResponse{}, fmt.Errorf(
			"gemini embed request: response vector count %d does not match texts %d",
			len(response.Embeddings),
			len(effective.Texts),
		)
	}

	vectors := make([][]float32, 0, len(response.Embeddings))
	for index, item := range response.Embeddings {
		if item == nil {
			return ai.EmbeddingResponse{}, fmt.Errorf("gemini embed response embeddings[%d]: missing embedding", index)
		}
		vector, normalizeErr := normalizeEmbeddingVector32(item.Values)
		if normalizeErr != nil {
			return ai.EmbeddingResponse{}, fmt.Errorf("gemini embed response embeddings[%d]: %w", index, normalizeErr)
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

func safeInt32(value int) (int32, error) {
	if value < math.MinInt32 || value > math.MaxInt32 {
		return 0, fmt.Errorf("dimensions exceed int32 range")
	}

	return int32(value), nil
}

func normalizeEmbeddingProviderConfig(cfg EmbeddingProviderConfig) (EmbeddingProviderConfig, error) {
	cfg.APIKey = strings.TrimSpace(cfg.APIKey)
	cfg.BaseURL = strings.TrimSpace(cfg.BaseURL)
	cfg.APIVersion = strings.TrimSpace(cfg.APIVersion)
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
	if cfg.APIVersion == "" {
		cfg.APIVersion = defaultAPIVersion
	}
	if !isValidAPIVersion(cfg.APIVersion) {
		return EmbeddingProviderConfig{}, fmt.Errorf("invalid api_version %q", cfg.APIVersion)
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

func mapEmbeddingTaskType(taskType ai.EmbeddingTaskType) string {
	switch taskType {
	case ai.EmbeddingTaskTypeDocument:
		return geminiTaskTypeDocument
	case ai.EmbeddingTaskTypeQuery:
		return geminiTaskTypeQuery
	default:
		return geminiTaskTypeSimilarity
	}
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
