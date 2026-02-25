package openai

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"ex-otogi/pkg/otogi"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
)

const (
	openAIEventOutputTextDelta = "response.output_text.delta"
	openAIEventCompleted       = "response.completed"
	openAIEventFailed          = "response.failed"
	openAIEventError           = "error"
)

// ProviderConfig configures one OpenAI-backed provider instance.
type ProviderConfig struct {
	// APIKey is the credential used to authenticate requests.
	APIKey string
	// BaseURL optionally overrides the OpenAI endpoint.
	BaseURL string
	// Organization optionally sets the OpenAI organization header.
	Organization string
	// Project optionally sets the OpenAI project header.
	Project string
	// Timeout optionally limits each request attempt.
	//
	// Zero keeps the SDK default behavior.
	Timeout time.Duration
	// MaxRetries optionally overrides the SDK retry count.
	//
	// Nil keeps the SDK default behavior.
	MaxRetries *int
}

// Provider is an otogi LLM provider backed by OpenAI Responses streaming.
type Provider struct {
	responses openAIResponsesClient
}

type openAIResponsesClient interface {
	NewStreaming(ctx context.Context, body responses.ResponseNewParams, opts ...option.RequestOption) openAIResponseStream
}

type openAIResponseServiceAdapter struct {
	service responses.ResponseService
}

func (a openAIResponseServiceAdapter) NewStreaming(
	ctx context.Context,
	body responses.ResponseNewParams,
	opts ...option.RequestOption,
) openAIResponseStream {
	return a.service.NewStreaming(ctx, body, opts...)
}

// New builds one OpenAI Responses API provider instance.
func New(cfg ProviderConfig) (*Provider, error) {
	normalized, err := normalizeProviderConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("new openai provider: %w", err)
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
	if normalized.Timeout > 0 {
		options = append(options, option.WithRequestTimeout(normalized.Timeout))
	}
	if normalized.MaxRetries != nil {
		options = append(options, option.WithMaxRetries(*normalized.MaxRetries))
	}

	client := openai.NewClient(options...)

	return &Provider{
		responses: openAIResponseServiceAdapter{service: client.Responses},
	}, nil
}

// GenerateStream starts one OpenAI Responses streaming request.
func (p *Provider) GenerateStream(
	ctx context.Context,
	req otogi.LLMGenerateRequest,
) (otogi.LLMStream, error) {
	if p == nil {
		return nil, fmt.Errorf("openai generate stream: nil provider")
	}
	if ctx == nil {
		return nil, fmt.Errorf("openai generate stream: nil context")
	}
	if p.responses == nil {
		return nil, fmt.Errorf("openai generate stream: responses client is nil")
	}
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("openai generate stream validate request: %w", err)
	}

	params, err := mapGenerateRequest(req)
	if err != nil {
		return nil, fmt.Errorf("openai generate stream map request: %w", err)
	}

	stream := p.responses.NewStreaming(ctx, params)
	if stream == nil {
		return nil, fmt.Errorf("openai generate stream: openai stream is nil")
	}

	return newOpenAIStream(stream), nil
}

func mapGenerateRequest(req otogi.LLMGenerateRequest) (responses.ResponseNewParams, error) {
	items := make(responses.ResponseInputParam, 0, len(req.Messages))
	for index, message := range req.Messages {
		role, err := mapMessageRole(message.Role)
		if err != nil {
			return responses.ResponseNewParams{}, fmt.Errorf("messages[%d] role: %w", index, err)
		}
		items = append(items, responses.ResponseInputItemParamOfMessage(message.Content, role))
	}

	params := responses.ResponseNewParams{
		Model: strings.TrimSpace(req.Model),
		Input: responses.ResponseNewParamsInputUnion{
			OfInputItemList: items,
		},
	}

	if req.Temperature > 0 {
		params.Temperature = openai.Float(req.Temperature)
	}
	if req.MaxOutputTokens > 0 {
		params.MaxOutputTokens = openai.Int(int64(req.MaxOutputTokens))
	}
	if len(req.Metadata) > 0 {
		metadata := make(shared.Metadata, len(req.Metadata))
		for key, value := range req.Metadata {
			metadata[key] = value
		}
		params.Metadata = metadata
	}

	return params, nil
}

func mapMessageRole(role otogi.LLMMessageRole) (responses.EasyInputMessageRole, error) {
	switch role {
	case otogi.LLMMessageRoleSystem:
		return responses.EasyInputMessageRoleSystem, nil
	case otogi.LLMMessageRoleUser:
		return responses.EasyInputMessageRoleUser, nil
	case otogi.LLMMessageRoleAssistant:
		return responses.EasyInputMessageRoleAssistant, nil
	default:
		return "", fmt.Errorf("unsupported role %q", role)
	}
}

func normalizeProviderConfig(cfg ProviderConfig) (ProviderConfig, error) {
	cfg.APIKey = strings.TrimSpace(cfg.APIKey)
	cfg.BaseURL = strings.TrimSpace(cfg.BaseURL)
	cfg.Organization = strings.TrimSpace(cfg.Organization)
	cfg.Project = strings.TrimSpace(cfg.Project)

	if cfg.APIKey == "" {
		return ProviderConfig{}, fmt.Errorf("missing api_key")
	}
	if cfg.BaseURL != "" {
		parsed, err := url.Parse(cfg.BaseURL)
		if err != nil {
			return ProviderConfig{}, fmt.Errorf("parse base_url: %w", err)
		}
		if parsed.Scheme == "" || parsed.Host == "" {
			return ProviderConfig{}, fmt.Errorf("parse base_url: must include scheme and host")
		}
	}
	if cfg.Timeout < 0 {
		return ProviderConfig{}, fmt.Errorf("timeout must be >= 0")
	}
	if cfg.MaxRetries != nil && *cfg.MaxRetries < 0 {
		return ProviderConfig{}, fmt.Errorf("max_retries must be >= 0")
	}

	return cfg, nil
}

var _ otogi.LLMProvider = (*Provider)(nil)
