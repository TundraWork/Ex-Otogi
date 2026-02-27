package gemini

import (
	"context"
	"fmt"
	"iter"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"ex-otogi/pkg/otogi"

	"google.golang.org/genai"
)

const (
	defaultAPIVersion = "v1beta"

	metadataGoogleSearch    = "gemini.google_search"
	metadataURLContext      = "gemini.url_context"
	metadataThinkingBudget  = "gemini.thinking_budget"
	metadataIncludeThoughts = "gemini.include_thoughts"
	metadataThinkingLevel   = "gemini.thinking_level"
	metadataResponseMIME    = "gemini.response_mime_type"

	thinkingLevelLow    = "low"
	thinkingLevelMedium = "medium"
	thinkingLevelHigh   = "high"

	responseMIMEText = "text/plain"
	responseMIMEJSON = "application/json"
)

// ProviderConfig configures one Gemini-backed provider instance.
type ProviderConfig struct {
	// APIKey is the credential used to authenticate requests.
	APIKey string
	// BaseURL optionally overrides the Gemini endpoint.
	BaseURL string
	// APIVersion optionally overrides Gemini API version.
	//
	// Zero defaults to v1beta.
	APIVersion string
	// GoogleSearch optionally enables Google Search tool for all requests.
	GoogleSearch *bool
	// URLContext optionally enables URL Context tool for all requests.
	URLContext *bool
	// ThinkingBudget optionally sets thinking token budget.
	//
	// ThinkingBudget and ThinkingLevel are mutually exclusive.
	ThinkingBudget *int
	// IncludeThoughts optionally asks models to include thought parts.
	IncludeThoughts *bool
	// ThinkingLevel optionally sets thinking level (low|medium|high).
	//
	// ThinkingLevel and ThinkingBudget are mutually exclusive.
	ThinkingLevel string
	// ResponseMIMEType optionally sets response MIME type.
	//
	// Supported values: text/plain, application/json.
	ResponseMIMEType string
}

// Provider is an otogi LLM provider backed by Google Gemini streaming API.
type Provider struct {
	models   geminiModelsClient
	defaults requestOptions
}

type geminiModelsClient interface {
	GenerateContentStream(
		ctx context.Context,
		model string,
		contents []*genai.Content,
		config *genai.GenerateContentConfig,
	) iter.Seq2[*genai.GenerateContentResponse, error]
}

type normalizedProviderConfig struct {
	apiKey     string
	baseURL    string
	apiVersion string
	defaults   requestOptions
}

type requestOptions struct {
	googleSearch    *bool
	urlContext      *bool
	thinkingBudget  *int32
	includeThoughts *bool
	thinkingLevel   genai.ThinkingLevel
	responseMIME    string
}

// New builds one Gemini API provider instance.
func New(cfg ProviderConfig) (*Provider, error) {
	normalized, err := normalizeProviderConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("new gemini provider: %w", err)
	}

	clientConfig := &genai.ClientConfig{
		APIKey:  normalized.apiKey,
		Backend: genai.BackendGeminiAPI,
		HTTPOptions: genai.HTTPOptions{
			BaseURL:    normalized.baseURL,
			APIVersion: normalized.apiVersion,
		},
	}

	client, err := genai.NewClient(context.Background(), clientConfig)
	if err != nil {
		return nil, fmt.Errorf("new gemini client: %w", err)
	}
	if client == nil || client.Models == nil {
		return nil, fmt.Errorf("new gemini client: models client is nil")
	}

	return &Provider{
		models:   client.Models,
		defaults: normalized.defaults,
	}, nil
}

// GenerateStream starts one Gemini streaming request.
func (p *Provider) GenerateStream(
	ctx context.Context,
	req otogi.LLMGenerateRequest,
) (otogi.LLMStream, error) {
	if p == nil {
		return nil, fmt.Errorf("gemini generate stream: nil provider")
	}
	if ctx == nil {
		return nil, fmt.Errorf("gemini generate stream: nil context")
	}
	if p.models == nil {
		return nil, fmt.Errorf("gemini generate stream: models client is nil")
	}
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("gemini generate stream validate request: %w", err)
	}

	contents, config, effective, err := mapGenerateRequest(req, p.defaults)
	if err != nil {
		return nil, fmt.Errorf("gemini generate stream map request: %w", err)
	}
	// Force SDK request timeout off for streams so caller context is the only deadline authority.
	streamTimeout := time.Duration(0)
	config.HTTPOptions = &genai.HTTPOptions{Timeout: &streamTimeout}

	stream := p.models.GenerateContentStream(ctx, strings.TrimSpace(req.Model), contents, config)
	if stream == nil {
		return nil, fmt.Errorf("gemini generate stream: stream is nil")
	}

	return newGeminiStream(stream, effective.includeThoughtsEnabled()), nil
}

func mapGenerateRequest(
	req otogi.LLMGenerateRequest,
	defaults requestOptions,
) ([]*genai.Content, *genai.GenerateContentConfig, requestOptions, error) {
	overrides, err := parseMetadataOverrides(req.Metadata)
	if err != nil {
		return nil, nil, requestOptions{}, err
	}
	effective := mergeRequestOptions(defaults, overrides)
	if err := effective.validateThinkingSelection(); err != nil {
		return nil, nil, requestOptions{}, fmt.Errorf("thinking options: %w", err)
	}

	systemParts := make([]string, 0, len(req.Messages))
	contents := make([]*genai.Content, 0, len(req.Messages))
	for index, message := range req.Messages {
		switch message.Role {
		case otogi.LLMMessageRoleSystem:
			systemParts = append(systemParts, message.Content)
		case otogi.LLMMessageRoleUser, otogi.LLMMessageRoleAssistant:
			role, roleErr := mapMessageRole(message.Role)
			if roleErr != nil {
				return nil, nil, requestOptions{}, fmt.Errorf("messages[%d] role: %w", index, roleErr)
			}
			contents = append(contents, &genai.Content{
				Role: role,
				Parts: []*genai.Part{
					{Text: message.Content},
				},
			})
		default:
			return nil, nil, requestOptions{}, fmt.Errorf("messages[%d] role: unsupported role %q", index, message.Role)
		}
	}
	if len(contents) == 0 {
		return nil, nil, requestOptions{}, fmt.Errorf("missing non-system messages")
	}

	config := &genai.GenerateContentConfig{}
	if len(systemParts) > 0 {
		config.SystemInstruction = &genai.Content{
			Parts: []*genai.Part{
				{Text: strings.Join(systemParts, "\n\n")},
			},
		}
	}
	if req.Temperature > 0 {
		temperature := float32(req.Temperature)
		config.Temperature = &temperature
	}
	if req.MaxOutputTokens > 0 {
		if req.MaxOutputTokens > math.MaxInt32 {
			return nil, nil, requestOptions{}, fmt.Errorf("max_output_tokens exceeds int32 range")
		}
		config.MaxOutputTokens = int32(req.MaxOutputTokens)
	}

	tools := make([]*genai.Tool, 0, 2)
	if isTrue(effective.googleSearch) {
		tools = append(tools, &genai.Tool{GoogleSearch: &genai.GoogleSearch{}})
	}
	if isTrue(effective.urlContext) {
		tools = append(tools, &genai.Tool{URLContext: &genai.URLContext{}})
	}
	if len(tools) > 0 {
		config.Tools = tools
	}

	if effective.hasThinkingConfig() {
		thinking := &genai.ThinkingConfig{}
		if effective.includeThoughts != nil {
			thinking.IncludeThoughts = *effective.includeThoughts
		}
		if effective.thinkingBudget != nil {
			budget := *effective.thinkingBudget
			thinking.ThinkingBudget = &budget
		}
		if effective.thinkingLevel != "" {
			thinking.ThinkingLevel = effective.thinkingLevel
		}
		config.ThinkingConfig = thinking
	}
	if effective.responseMIME != "" {
		config.ResponseMIMEType = effective.responseMIME
	}

	return contents, config, effective, nil
}

func mapMessageRole(role otogi.LLMMessageRole) (string, error) {
	switch role {
	case otogi.LLMMessageRoleUser:
		return string(genai.RoleUser), nil
	case otogi.LLMMessageRoleAssistant:
		return string(genai.RoleModel), nil
	default:
		return "", fmt.Errorf("unsupported role %q", role)
	}
}

func parseMetadataOverrides(metadata map[string]string) (requestOptions, error) {
	var overrides requestOptions

	if raw, exists := metadata[metadataGoogleSearch]; exists {
		parsed, err := parseMetadataBool(raw)
		if err != nil {
			return requestOptions{}, fmt.Errorf("%s: %w", metadataGoogleSearch, err)
		}
		overrides.googleSearch = &parsed
	}
	if raw, exists := metadata[metadataURLContext]; exists {
		parsed, err := parseMetadataBool(raw)
		if err != nil {
			return requestOptions{}, fmt.Errorf("%s: %w", metadataURLContext, err)
		}
		overrides.urlContext = &parsed
	}
	if raw, exists := metadata[metadataThinkingBudget]; exists {
		parsed, err := parseMetadataInt32(raw)
		if err != nil {
			return requestOptions{}, fmt.Errorf("%s: %w", metadataThinkingBudget, err)
		}
		overrides.thinkingBudget = &parsed
	}
	if raw, exists := metadata[metadataIncludeThoughts]; exists {
		parsed, err := parseMetadataBool(raw)
		if err != nil {
			return requestOptions{}, fmt.Errorf("%s: %w", metadataIncludeThoughts, err)
		}
		overrides.includeThoughts = &parsed
	}
	if raw, exists := metadata[metadataThinkingLevel]; exists {
		level, err := normalizeThinkingLevel(raw)
		if err != nil {
			return requestOptions{}, fmt.Errorf("%s: %w", metadataThinkingLevel, err)
		}
		overrides.thinkingLevel = level
	}
	if raw, exists := metadata[metadataResponseMIME]; exists {
		mime, err := normalizeResponseMIME(raw)
		if err != nil {
			return requestOptions{}, fmt.Errorf("%s: %w", metadataResponseMIME, err)
		}
		overrides.responseMIME = mime
	}
	if err := overrides.validateThinkingSelection(); err != nil {
		return requestOptions{}, fmt.Errorf("metadata thinking options: %w", err)
	}

	return overrides, nil
}

func normalizeProviderConfig(cfg ProviderConfig) (normalizedProviderConfig, error) {
	trimmedAPIKey := strings.TrimSpace(cfg.APIKey)
	if trimmedAPIKey == "" {
		return normalizedProviderConfig{}, fmt.Errorf("missing api_key")
	}

	trimmedBaseURL := strings.TrimSpace(cfg.BaseURL)
	if trimmedBaseURL != "" {
		parsed, err := url.Parse(trimmedBaseURL)
		if err != nil {
			return normalizedProviderConfig{}, fmt.Errorf("parse base_url: %w", err)
		}
		if parsed.Scheme == "" || parsed.Host == "" {
			return normalizedProviderConfig{}, fmt.Errorf("parse base_url: must include scheme and host")
		}
	}

	trimmedAPIVersion := strings.TrimSpace(cfg.APIVersion)
	if trimmedAPIVersion == "" {
		trimmedAPIVersion = defaultAPIVersion
	}
	if !isValidAPIVersion(trimmedAPIVersion) {
		return normalizedProviderConfig{}, fmt.Errorf("invalid api_version %q", cfg.APIVersion)
	}
	defaults, err := optionsFromConfig(cfg)
	if err != nil {
		return normalizedProviderConfig{}, err
	}

	return normalizedProviderConfig{
		apiKey:     trimmedAPIKey,
		baseURL:    trimmedBaseURL,
		apiVersion: trimmedAPIVersion,
		defaults:   defaults,
	}, nil
}

func optionsFromConfig(cfg ProviderConfig) (requestOptions, error) {
	thinkingBudget, err := normalizeThinkingBudget(cfg.ThinkingBudget)
	if err != nil {
		return requestOptions{}, fmt.Errorf("thinking_budget: %w", err)
	}
	thinkingLevel, err := normalizeThinkingLevel(cfg.ThinkingLevel)
	if err != nil {
		return requestOptions{}, fmt.Errorf("thinking_level: %w", err)
	}
	responseMIME, err := normalizeResponseMIME(cfg.ResponseMIMEType)
	if err != nil {
		return requestOptions{}, fmt.Errorf("response_mime_type: %w", err)
	}

	options := requestOptions{
		googleSearch:    cloneBoolPointer(cfg.GoogleSearch),
		urlContext:      cloneBoolPointer(cfg.URLContext),
		thinkingBudget:  thinkingBudget,
		includeThoughts: cloneBoolPointer(cfg.IncludeThoughts),
		thinkingLevel:   thinkingLevel,
		responseMIME:    responseMIME,
	}
	if err := options.validateThinkingSelection(); err != nil {
		return requestOptions{}, fmt.Errorf("thinking options: %w", err)
	}

	return options, nil
}

func mergeRequestOptions(defaults, overrides requestOptions) requestOptions {
	merged := requestOptions{
		googleSearch:    cloneBoolPointer(defaults.googleSearch),
		urlContext:      cloneBoolPointer(defaults.urlContext),
		thinkingBudget:  cloneInt32Pointer(defaults.thinkingBudget),
		includeThoughts: cloneBoolPointer(defaults.includeThoughts),
		thinkingLevel:   defaults.thinkingLevel,
		responseMIME:    defaults.responseMIME,
	}

	if overrides.googleSearch != nil {
		merged.googleSearch = cloneBoolPointer(overrides.googleSearch)
	}
	if overrides.urlContext != nil {
		merged.urlContext = cloneBoolPointer(overrides.urlContext)
	}
	if overrides.thinkingBudget != nil {
		merged.thinkingBudget = cloneInt32Pointer(overrides.thinkingBudget)
	}
	if overrides.includeThoughts != nil {
		merged.includeThoughts = cloneBoolPointer(overrides.includeThoughts)
	}
	if overrides.thinkingLevel != "" {
		merged.thinkingLevel = overrides.thinkingLevel
	}
	if overrides.responseMIME != "" {
		merged.responseMIME = overrides.responseMIME
	}

	return merged
}

func normalizeThinkingBudget(raw *int) (*int32, error) {
	if raw == nil {
		return nil, nil
	}
	if *raw < 0 {
		return nil, fmt.Errorf("must be >= 0")
	}
	if *raw > math.MaxInt32 {
		return nil, fmt.Errorf("must fit int32")
	}
	normalized := int32(*raw)
	return &normalized, nil
}

func normalizeThinkingLevel(raw string) (genai.ThinkingLevel, error) {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	switch normalized {
	case "":
		return "", nil
	case thinkingLevelLow:
		return genai.ThinkingLevelLow, nil
	case thinkingLevelMedium:
		return genai.ThinkingLevelMedium, nil
	case thinkingLevelHigh:
		return genai.ThinkingLevelHigh, nil
	default:
		return "", fmt.Errorf("unsupported value %q", raw)
	}
}

func normalizeResponseMIME(raw string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	switch normalized {
	case "":
		return "", nil
	case responseMIMEText, responseMIMEJSON:
		return normalized, nil
	default:
		return "", fmt.Errorf("unsupported value %q", raw)
	}
}

func parseMetadataBool(raw string) (bool, error) {
	normalized := strings.TrimSpace(raw)
	if normalized == "" {
		return false, fmt.Errorf("empty value")
	}
	parsed, err := strconv.ParseBool(normalized)
	if err != nil {
		return false, fmt.Errorf("parse bool: %w", err)
	}
	return parsed, nil
}

func parseMetadataInt32(raw string) (int32, error) {
	normalized := strings.TrimSpace(raw)
	if normalized == "" {
		return 0, fmt.Errorf("empty value")
	}
	parsed, err := strconv.ParseInt(normalized, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("parse int: %w", err)
	}
	if parsed < 0 {
		return 0, fmt.Errorf("must be >= 0")
	}
	return int32(parsed), nil
}

func isValidAPIVersion(raw string) bool {
	if raw == "" {
		return false
	}
	for _, r := range raw {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			continue
		}
		switch r {
		case '-', '.', '_':
			continue
		default:
			return false
		}
	}
	return true
}

func isTrue(value *bool) bool {
	return value != nil && *value
}

func (o requestOptions) hasThinkingConfig() bool {
	return o.includeThoughts != nil || o.thinkingBudget != nil || o.thinkingLevel != ""
}

func (o requestOptions) includeThoughtsEnabled() bool {
	return isTrue(o.includeThoughts)
}

func (o requestOptions) validateThinkingSelection() error {
	if o.thinkingBudget != nil && o.thinkingLevel != "" {
		return fmt.Errorf("thinking_budget and thinking_level are mutually exclusive")
	}

	return nil
}

func cloneBoolPointer(value *bool) *bool {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneInt32Pointer(value *int32) *int32 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

var _ otogi.LLMProvider = (*Provider)(nil)
