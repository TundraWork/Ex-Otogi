package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"text/template"
	"time"
	"unicode"
)

const (
	defaultRequestTimeout = 90 * time.Second

	providerTypeOpenAI = "openai"
	providerTypeGemini = "gemini"

	defaultGeminiAPIVersion = "v1beta"

	metadataKeyAgent             = "agent"
	metadataKeyProvider          = "provider"
	metadataKeyConversationID    = "conversation_id"
	metadataGeminiThinkingBudget = "gemini.thinking_budget"
	metadataGeminiThinkingLevel  = "gemini.thinking_level"

	geminiThinkingLevelLow    = "low"
	geminiThinkingLevelMedium = "medium"
	geminiThinkingLevelHigh   = "high"

	geminiResponseMIMEText = "text/plain"
	geminiResponseMIMEJSON = "application/json"
)

// Config is the full runtime LLM configuration model loaded from JSON.
type Config struct {
	// RequestTimeout bounds one LLM request lifecycle for llmchat.
	RequestTimeout time.Duration
	// Providers contains provider profiles keyed by profile name.
	Providers map[string]ProviderProfile
	// Agents contains triggerable llmchat agents.
	Agents []Agent
}

// ProviderProfile describes one named provider profile.
type ProviderProfile struct {
	// Type identifies provider implementation kind.
	Type string
	// APIKey is the provider credential.
	APIKey string
	// BaseURL optionally overrides provider API endpoint.
	BaseURL string
	// OpenAI carries OpenAI-specific options.
	OpenAI *OpenAIOptions
	// Gemini carries Gemini-specific options.
	Gemini *GeminiOptions
}

// OpenAIOptions carries OpenAI-specific profile options.
type OpenAIOptions struct {
	// Organization optionally scopes requests to one OpenAI organization.
	Organization string
	// Project optionally scopes requests to one OpenAI project.
	Project string
	// MaxRetries optionally overrides SDK retry count.
	MaxRetries *int
}

// GeminiOptions carries Gemini-specific profile options.
type GeminiOptions struct {
	// APIVersion selects the Gemini Developer API version.
	APIVersion string
	// RequestDefaults are default generation options applied to each request.
	RequestDefaults GeminiRequestDefaults
}

// GeminiRequestDefaults contains default Gemini request options.
type GeminiRequestDefaults struct {
	// GoogleSearch enables the Google Search tool.
	GoogleSearch *bool
	// URLContext enables the URL Context tool.
	URLContext *bool
	// ThinkingBudget optionally sets thinking token budget.
	//
	// ThinkingBudget and ThinkingLevel are mutually exclusive.
	ThinkingBudget *int
	// IncludeThoughts requests thought parts when supported.
	IncludeThoughts *bool
	// ThinkingLevel sets model thinking level (low|medium|high).
	//
	// ThinkingLevel and ThinkingBudget are mutually exclusive.
	ThinkingLevel string
	// ResponseMIMEType sets output MIME type.
	ResponseMIMEType string
}

// Agent describes one configured llmchat agent.
type Agent struct {
	// Name is the trigger keyword for this agent.
	Name string
	// Description is a short operator-facing explanation for this agent.
	Description string
	// Provider identifies which provider profile to resolve.
	Provider string
	// Model identifies which provider model name to call.
	Model string
	// SystemPromptTemplate is the system prompt template for this agent.
	SystemPromptTemplate string
	// TemplateVariables are additional template variables injected at render time.
	TemplateVariables map[string]string
	// MaxOutputTokens optionally limits generated token count.
	MaxOutputTokens int
	// Temperature optionally controls output randomness.
	Temperature float64
	// RequestTimeout bounds one LLM request lifecycle for this agent.
	RequestTimeout time.Duration
	// RequestMetadata carries provider-agnostic per-agent metadata overrides.
	RequestMetadata map[string]string
}

type fileConfig struct {
	RequestTimeout string                       `json:"request_timeout"`
	Providers      map[string]fileProviderEntry `json:"providers"`
	Agents         []fileAgent                  `json:"agents"`
}

type fileProviderEntry struct {
	Type    string           `json:"type"`
	APIKey  string           `json:"api_key"`
	BaseURL string           `json:"base_url"`
	OpenAI  *fileOpenAIEntry `json:"openai"`
	Gemini  *fileGeminiEntry `json:"gemini"`
}

type fileOpenAIEntry struct {
	Organization string `json:"organization"`
	Project      string `json:"project"`
	MaxRetries   *int   `json:"max_retries"`
}

type fileGeminiEntry struct {
	APIVersion       string `json:"api_version"`
	GoogleSearch     *bool  `json:"google_search"`
	URLContext       *bool  `json:"url_context"`
	ThinkingBudget   *int   `json:"thinking_budget"`
	IncludeThoughts  *bool  `json:"include_thoughts"`
	ThinkingLevel    string `json:"thinking_level"`
	ResponseMIMEType string `json:"response_mime_type"`
}

type fileAgent struct {
	Name                 string            `json:"name"`
	Description          string            `json:"description"`
	Provider             string            `json:"provider"`
	Model                string            `json:"model"`
	SystemPromptTemplate string            `json:"system_prompt_template"`
	TemplateVariables    map[string]string `json:"template_variables"`
	MaxOutputTokens      int               `json:"max_output_tokens"`
	Temperature          float64           `json:"temperature"`
	RequestTimeout       string            `json:"request_timeout"`
	RequestMetadata      map[string]string `json:"request_metadata"`
}

type rootRaw struct {
	Providers json.RawMessage `json:"providers"`
}

// LoadFile reads and validates runtime LLM configuration from path.
func LoadFile(path string) (Config, error) {
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return Config{}, fmt.Errorf("load llm config: empty path")
	}

	data, err := os.ReadFile(trimmedPath)
	if err != nil {
		return Config{}, fmt.Errorf("load llm config read %s: %w", trimmedPath, err)
	}

	if err := validateDuplicateProviderKeys(data); err != nil {
		return Config{}, fmt.Errorf("load llm config parse %s: %w", trimmedPath, err)
	}

	var parsed fileConfig
	if err := decodeStrictJSON(data, &parsed); err != nil {
		return Config{}, fmt.Errorf("load llm config parse %s: %w", trimmedPath, err)
	}

	cfg := Config{
		RequestTimeout: defaultRequestTimeout,
		Providers:      make(map[string]ProviderProfile, len(parsed.Providers)),
		Agents:         make([]Agent, 0, len(parsed.Agents)),
	}

	if rawTimeout := strings.TrimSpace(parsed.RequestTimeout); rawTimeout != "" {
		timeout, err := time.ParseDuration(rawTimeout)
		if err != nil {
			return Config{}, fmt.Errorf("load llm config parse request_timeout: %w", err)
		}
		if timeout <= 0 {
			return Config{}, fmt.Errorf("load llm config parse request_timeout: must be > 0")
		}
		cfg.RequestTimeout = timeout
	}

	for key, rawProvider := range parsed.Providers {
		profileKey := strings.TrimSpace(key)
		if profileKey == "" {
			return Config{}, fmt.Errorf("load llm config providers: empty provider key")
		}
		if _, exists := cfg.Providers[profileKey]; exists {
			return Config{}, fmt.Errorf("load llm config providers: duplicate provider key %s", profileKey)
		}

		profile, err := parseProviderProfile(rawProvider)
		if err != nil {
			return Config{}, fmt.Errorf("load llm config providers[%s]: %w", profileKey, err)
		}
		if err := validateProviderProfile(profileKey, profile); err != nil {
			return Config{}, fmt.Errorf("load llm config providers[%s]: %w", profileKey, err)
		}
		cfg.Providers[profileKey] = profile
	}

	for index, rawAgent := range parsed.Agents {
		rawRequestTimeout := strings.TrimSpace(rawAgent.RequestTimeout)
		if rawRequestTimeout == "" {
			return Config{}, fmt.Errorf("load llm config agents[%d]: missing request_timeout", index)
		}
		agentRequestTimeout, err := time.ParseDuration(rawRequestTimeout)
		if err != nil {
			return Config{}, fmt.Errorf("load llm config agents[%d]: parse request_timeout: %w", index, err)
		}
		if agentRequestTimeout <= 0 {
			return Config{}, fmt.Errorf("load llm config agents[%d]: parse request_timeout: must be > 0", index)
		}

		agent := Agent{
			Name:                 strings.TrimSpace(rawAgent.Name),
			Description:          strings.TrimSpace(rawAgent.Description),
			Provider:             strings.TrimSpace(rawAgent.Provider),
			Model:                strings.TrimSpace(rawAgent.Model),
			SystemPromptTemplate: strings.TrimSpace(rawAgent.SystemPromptTemplate),
			TemplateVariables:    cloneStringMap(rawAgent.TemplateVariables),
			MaxOutputTokens:      rawAgent.MaxOutputTokens,
			Temperature:          rawAgent.Temperature,
			RequestTimeout:       agentRequestTimeout,
			RequestMetadata:      cloneStringMap(rawAgent.RequestMetadata),
		}
		if err := validateAgent(agent); err != nil {
			return Config{}, fmt.Errorf("load llm config agents[%d]: %w", index, err)
		}
		cfg.Agents = append(cfg.Agents, agent)
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

// Validate checks configuration coherence.
func (cfg Config) Validate() error {
	if cfg.RequestTimeout <= 0 {
		return fmt.Errorf("validate llm config: request_timeout must be > 0")
	}
	if len(cfg.Providers) == 0 {
		return fmt.Errorf("validate llm config: providers is required")
	}
	if len(cfg.Agents) == 0 {
		return fmt.Errorf("validate llm config: at least one agent is required")
	}

	seenProviders := make(map[string]struct{}, len(cfg.Providers))
	for key, profile := range cfg.Providers {
		profileKey := strings.TrimSpace(key)
		if profileKey == "" {
			return fmt.Errorf("validate llm config providers: empty provider key")
		}
		if _, exists := seenProviders[profileKey]; exists {
			return fmt.Errorf("validate llm config providers: duplicate provider key %s", profileKey)
		}
		seenProviders[profileKey] = struct{}{}

		if err := validateProviderProfile(profileKey, profile); err != nil {
			return fmt.Errorf("validate llm config providers[%s]: %w", profileKey, err)
		}
	}

	seenNames := make(map[string]struct{}, len(cfg.Agents))
	for index, agent := range cfg.Agents {
		if err := validateAgent(agent); err != nil {
			return fmt.Errorf("validate llm config agents[%d]: %w", index, err)
		}

		normalized := normalizeAgentName(agent.Name)
		if _, exists := seenNames[normalized]; exists {
			return fmt.Errorf("validate llm config: duplicate agent name %q", agent.Name)
		}
		seenNames[normalized] = struct{}{}

		providerKey := strings.TrimSpace(agent.Provider)
		providerProfile, exists := cfg.Providers[providerKey]
		if !exists {
			return fmt.Errorf("validate llm config agents[%d]: provider %s is not configured", index, providerKey)
		}
		if strings.EqualFold(strings.TrimSpace(providerProfile.Type), providerTypeGemini) {
			if err := validateGeminiAgentThinkingOptions(providerKey, providerProfile, agent); err != nil {
				return fmt.Errorf("validate llm config agents[%d]: %w", index, err)
			}
		}
		if agent.RequestTimeout > cfg.RequestTimeout {
			return fmt.Errorf(
				"validate llm config agents[%d]: request_timeout %s exceeds global request_timeout %s",
				index,
				agent.RequestTimeout,
				cfg.RequestTimeout,
			)
		}
	}

	return nil
}

func parseProviderProfile(raw fileProviderEntry) (ProviderProfile, error) {
	profile := ProviderProfile{
		Type:    strings.ToLower(strings.TrimSpace(raw.Type)),
		APIKey:  strings.TrimSpace(raw.APIKey),
		BaseURL: strings.TrimSpace(raw.BaseURL),
		OpenAI:  parseOpenAIOptions(raw.OpenAI),
		Gemini:  parseGeminiOptions(raw.Gemini),
	}

	if profile.Type == providerTypeGemini {
		if profile.Gemini == nil {
			profile.Gemini = &GeminiOptions{
				APIVersion: defaultGeminiAPIVersion,
			}
		}
		if strings.TrimSpace(profile.Gemini.APIVersion) == "" {
			profile.Gemini.APIVersion = defaultGeminiAPIVersion
		}
	}

	return profile, nil
}

func parseOpenAIOptions(raw *fileOpenAIEntry) *OpenAIOptions {
	if raw == nil {
		return nil
	}

	return &OpenAIOptions{
		Organization: strings.TrimSpace(raw.Organization),
		Project:      strings.TrimSpace(raw.Project),
		MaxRetries:   cloneIntPointer(raw.MaxRetries),
	}
}

func parseGeminiOptions(raw *fileGeminiEntry) *GeminiOptions {
	if raw == nil {
		return nil
	}

	return &GeminiOptions{
		APIVersion: strings.TrimSpace(raw.APIVersion),
		RequestDefaults: GeminiRequestDefaults{
			GoogleSearch:     cloneBoolPointer(raw.GoogleSearch),
			URLContext:       cloneBoolPointer(raw.URLContext),
			ThinkingBudget:   cloneIntPointer(raw.ThinkingBudget),
			IncludeThoughts:  cloneBoolPointer(raw.IncludeThoughts),
			ThinkingLevel:    normalizeGeminiThinkingLevel(raw.ThinkingLevel),
			ResponseMIMEType: normalizeGeminiResponseMIMEType(raw.ResponseMIMEType),
		},
	}
}

func validateProviderProfile(profileKey string, profile ProviderProfile) error {
	if strings.TrimSpace(profileKey) == "" {
		return fmt.Errorf("empty provider key")
	}

	providerType := strings.ToLower(strings.TrimSpace(profile.Type))
	if providerType == "" {
		return fmt.Errorf("missing type")
	}

	switch providerType {
	case providerTypeOpenAI:
		if strings.TrimSpace(profile.APIKey) == "" {
			return fmt.Errorf("missing api_key")
		}
		if profile.Gemini != nil {
			return fmt.Errorf("gemini options are only supported for gemini providers")
		}
		if err := validateOpenAIOptions(profile.OpenAI); err != nil {
			return fmt.Errorf("invalid openai options: %w", err)
		}
	case providerTypeGemini:
		if strings.TrimSpace(profile.APIKey) == "" {
			return fmt.Errorf("missing api_key")
		}
		if profile.OpenAI != nil {
			return fmt.Errorf("openai options are only supported for openai providers")
		}
		if err := validateGeminiOptions(profile.Gemini); err != nil {
			return fmt.Errorf("invalid gemini options: %w", err)
		}
	default:
		return fmt.Errorf("unsupported type %q", profile.Type)
	}

	if rawBaseURL := strings.TrimSpace(profile.BaseURL); rawBaseURL != "" {
		parsed, err := url.Parse(rawBaseURL)
		if err != nil {
			return fmt.Errorf("invalid base_url: %w", err)
		}
		if parsed.Scheme == "" || parsed.Host == "" {
			return fmt.Errorf("invalid base_url: must include scheme and host")
		}
	}

	return nil
}

func validateOpenAIOptions(options *OpenAIOptions) error {
	if options == nil {
		return nil
	}
	if options.MaxRetries != nil && *options.MaxRetries < 0 {
		return fmt.Errorf("max_retries must be >= 0")
	}

	return nil
}

func validateGeminiOptions(options *GeminiOptions) error {
	if options == nil {
		return nil
	}

	if strings.TrimSpace(options.APIVersion) == "" {
		return fmt.Errorf("invalid api_version %q", options.APIVersion)
	}
	if !isValidAPIVersion(options.APIVersion) {
		return fmt.Errorf("invalid api_version %q", options.APIVersion)
	}

	return validateGeminiRequestDefaults(options.RequestDefaults)
}

func validateGeminiRequestDefaults(options GeminiRequestDefaults) error {
	if options.ThinkingBudget != nil && *options.ThinkingBudget < 0 {
		return fmt.Errorf("thinking_budget must be >= 0")
	}
	if level := normalizeGeminiThinkingLevel(options.ThinkingLevel); level != "" {
		switch level {
		case geminiThinkingLevelLow, geminiThinkingLevelMedium, geminiThinkingLevelHigh:
		default:
			return fmt.Errorf("unsupported thinking_level %q", options.ThinkingLevel)
		}
	}
	if options.ThinkingBudget != nil && normalizeGeminiThinkingLevel(options.ThinkingLevel) != "" {
		return fmt.Errorf("thinking_budget and thinking_level are mutually exclusive")
	}
	if mime := normalizeGeminiResponseMIMEType(options.ResponseMIMEType); mime != "" {
		switch mime {
		case geminiResponseMIMEText, geminiResponseMIMEJSON:
		default:
			return fmt.Errorf("unsupported response_mime_type %q", options.ResponseMIMEType)
		}
	}

	return nil
}

func validateGeminiAgentThinkingOptions(
	providerKey string,
	profile ProviderProfile,
	agent Agent,
) error {
	defaultBudgetSet := false
	defaultLevelSet := false
	if profile.Gemini != nil {
		defaultBudgetSet = profile.Gemini.RequestDefaults.ThinkingBudget != nil
		defaultLevelSet = normalizeGeminiThinkingLevel(profile.Gemini.RequestDefaults.ThinkingLevel) != ""
	}

	metadataBudgetSet := hasRequestMetadataKey(agent.RequestMetadata, metadataGeminiThinkingBudget)
	metadataLevelSet := hasRequestMetadataKey(agent.RequestMetadata, metadataGeminiThinkingLevel)

	effectiveBudgetSet := defaultBudgetSet || metadataBudgetSet
	effectiveLevelSet := defaultLevelSet || metadataLevelSet
	if effectiveBudgetSet && effectiveLevelSet {
		return fmt.Errorf(
			"provider %s agent %q sets both %s and %s across defaults/request_metadata",
			providerKey,
			agent.Name,
			metadataGeminiThinkingBudget,
			metadataGeminiThinkingLevel,
		)
	}

	return nil
}

func hasRequestMetadataKey(metadata map[string]string, key string) bool {
	_, exists := metadata[key]
	return exists
}

func validateAgent(agent Agent) error {
	if strings.TrimSpace(agent.Name) == "" {
		return fmt.Errorf("missing name")
	}
	if strings.TrimSpace(agent.Description) == "" {
		return fmt.Errorf("missing description")
	}
	if strings.TrimSpace(agent.Provider) == "" {
		return fmt.Errorf("missing provider")
	}
	if strings.TrimSpace(agent.Model) == "" {
		return fmt.Errorf("missing model")
	}
	if strings.TrimSpace(agent.SystemPromptTemplate) == "" {
		return fmt.Errorf("missing system_prompt_template")
	}
	if agent.MaxOutputTokens < 0 {
		return fmt.Errorf("max_output_tokens must be >= 0")
	}
	if agent.Temperature < 0 {
		return fmt.Errorf("temperature must be >= 0")
	}
	if agent.RequestTimeout <= 0 {
		return fmt.Errorf("request_timeout must be > 0")
	}
	if _, err := template.New("system-prompt").Option("missingkey=error").Parse(agent.SystemPromptTemplate); err != nil {
		return fmt.Errorf("invalid system_prompt_template: %w", err)
	}
	return validateRequestMetadata(agent.RequestMetadata)
}

func validateRequestMetadata(metadata map[string]string) error {
	for key, value := range metadata {
		if err := validateRequestMetadataEntry(key, value); err != nil {
			return err
		}
	}

	return nil
}

func validateRequestMetadataEntry(key string, value string) error {
	trimmedKey := strings.TrimSpace(key)
	if trimmedKey == "" {
		return fmt.Errorf("request_metadata contains empty key")
	}

	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return fmt.Errorf("request_metadata[%s]: empty value", trimmedKey)
	}

	if isReservedMetadataKey(trimmedKey) {
		return fmt.Errorf("request_metadata[%s]: reserved key", trimmedKey)
	}

	return nil
}

func isReservedMetadataKey(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case metadataKeyAgent, metadataKeyProvider, metadataKeyConversationID:
		return true
	default:
		return false
	}
}

func validateDuplicateProviderKeys(data []byte) error {
	var raw rootRaw
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("decode root json: %w", err)
	}
	if len(raw.Providers) == 0 {
		return nil
	}

	seen := make(map[string]struct{})
	decoder := json.NewDecoder(bytes.NewReader(raw.Providers))
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("providers: %w", err)
	}
	delim, ok := token.(json.Delim)
	if !ok || delim != '{' {
		return fmt.Errorf("providers: expected object")
	}

	for decoder.More() {
		rawKey, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("providers: %w", err)
		}
		key, ok := rawKey.(string)
		if !ok {
			return fmt.Errorf("providers: expected string key")
		}
		trimmedKey := strings.TrimSpace(key)
		if _, exists := seen[trimmedKey]; exists {
			return fmt.Errorf("providers: duplicate provider key %s", trimmedKey)
		}
		seen[trimmedKey] = struct{}{}

		var discard json.RawMessage
		if err := decoder.Decode(&discard); err != nil {
			return fmt.Errorf("providers[%s]: %w", trimmedKey, err)
		}
	}
	if _, err := decoder.Token(); err != nil {
		return fmt.Errorf("providers: %w", err)
	}

	return nil
}

func decodeStrictJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode json: %w", err)
	}

	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected trailing content")
		}
		return fmt.Errorf("decode trailing json: %w", err)
	}

	return nil
}

func normalizeGeminiThinkingLevel(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

func normalizeGeminiResponseMIMEType(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

func isValidAPIVersion(raw string) bool {
	if strings.TrimSpace(raw) == "" {
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

func normalizeAgentName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}

	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}

	return cloned
}

func cloneIntPointer(value *int) *int {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneBoolPointer(value *bool) *bool {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
