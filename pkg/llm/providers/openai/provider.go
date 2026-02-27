package openai

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"ex-otogi/pkg/otogi"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
)

const (
	openAIEventOutputTextDelta           = "response.output_text.delta"
	openAIEventReasoningSummaryTextDelta = "response.reasoning_summary_text.delta"
	openAIEventReasoningTextDelta        = "response.reasoning_text.delta"
	openAIEventCompleted                 = "response.completed"
	openAIEventFailed                    = "response.failed"
	openAIEventError                     = "error"

	metadataOpenAIReasoningSummary = "openai.reasoning_summary"
	metadataOpenAIReasoningEffort  = "openai.reasoning_effort"
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
	reasoningOptions, metadata, err := parseOpenAIRequestMetadata(req.Metadata)
	if err != nil {
		return responses.ResponseNewParams{}, err
	}
	if reasoningOptions.enabled() {
		reasoning := shared.ReasoningParam{}
		if reasoningOptions.summary != "" {
			reasoning.Summary = reasoningOptions.summary
		}
		if reasoningOptions.effort != "" {
			reasoning.Effort = reasoningOptions.effort
		}
		params.Reasoning = reasoning
	}

	if req.Temperature > 0 {
		params.Temperature = openai.Float(req.Temperature)
	}
	if req.MaxOutputTokens > 0 {
		params.MaxOutputTokens = openai.Int(int64(req.MaxOutputTokens))
	}
	if len(metadata) > 0 {
		requestMetadata := make(shared.Metadata, len(metadata))
		for key, value := range metadata {
			requestMetadata[key] = value
		}
		params.Metadata = requestMetadata
	}

	return params, nil
}

type openAIReasoningOptions struct {
	summary shared.ReasoningSummary
	effort  shared.ReasoningEffort
}

func (o openAIReasoningOptions) enabled() bool {
	return o.summary != "" || o.effort != ""
}

func parseOpenAIRequestMetadata(metadata map[string]string) (openAIReasoningOptions, map[string]string, error) {
	if len(metadata) == 0 {
		return openAIReasoningOptions{}, nil, nil
	}

	options := openAIReasoningOptions{}
	filtered := make(map[string]string, len(metadata))
	for key, value := range metadata {
		switch strings.ToLower(strings.TrimSpace(key)) {
		case metadataOpenAIReasoningSummary:
			summary, err := normalizeOpenAIReasoningSummary(value)
			if err != nil {
				return openAIReasoningOptions{}, nil, fmt.Errorf("%s: %w", metadataOpenAIReasoningSummary, err)
			}
			options.summary = summary
		case metadataOpenAIReasoningEffort:
			effort, err := normalizeOpenAIReasoningEffort(value)
			if err != nil {
				return openAIReasoningOptions{}, nil, fmt.Errorf("%s: %w", metadataOpenAIReasoningEffort, err)
			}
			options.effort = effort
		default:
			filtered[key] = value
		}
	}

	if len(filtered) == 0 {
		return options, nil, nil
	}

	return options, filtered, nil
}

func normalizeOpenAIReasoningSummary(raw string) (shared.ReasoningSummary, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(shared.ReasoningSummaryAuto):
		return shared.ReasoningSummaryAuto, nil
	case string(shared.ReasoningSummaryConcise):
		return shared.ReasoningSummaryConcise, nil
	case string(shared.ReasoningSummaryDetailed):
		return shared.ReasoningSummaryDetailed, nil
	default:
		return "", fmt.Errorf("unsupported value %q", raw)
	}
}

func normalizeOpenAIReasoningEffort(raw string) (shared.ReasoningEffort, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(shared.ReasoningEffortNone):
		return shared.ReasoningEffortNone, nil
	case string(shared.ReasoningEffortMinimal):
		return shared.ReasoningEffortMinimal, nil
	case string(shared.ReasoningEffortLow):
		return shared.ReasoningEffortLow, nil
	case string(shared.ReasoningEffortMedium):
		return shared.ReasoningEffortMedium, nil
	case string(shared.ReasoningEffortHigh):
		return shared.ReasoningEffortHigh, nil
	case string(shared.ReasoningEffortXhigh):
		return shared.ReasoningEffortXhigh, nil
	default:
		return "", fmt.Errorf("unsupported value %q", raw)
	}
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
	if cfg.MaxRetries != nil && *cfg.MaxRetries < 0 {
		return ProviderConfig{}, fmt.Errorf("max_retries must be >= 0")
	}

	return cfg, nil
}

var _ otogi.LLMProvider = (*Provider)(nil)
