package openai

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"ex-otogi/pkg/otogi/ai"

	openai "github.com/openai/openai-go/v3"
)

func classifyOpenAIFailure(err error) error {
	if err == nil {
		return nil
	}
	var existing *ai.LLMFailure
	if errors.As(err, &existing) {
		return err
	}
	if errors.Is(err, context.Canceled) {
		return ai.NewLLMFailure(ai.LLMFailureCanceled, false, 0, err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return ai.NewLLMFailure(ai.LLMFailureTimeout, false, 0, err)
	}

	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		class, retryable := openAIStatusFailure(apiErr.StatusCode)
		if safetyCode(apiErr.Code) || safetyCode(apiErr.Type) {
			class, retryable = ai.LLMFailureSafetyRefusal, false
		}
		return ai.NewLLMFailure(class, retryable, openAIRetryAfter(apiErr.Response), err)
	}
	var networkErr net.Error
	if errors.As(err, &networkErr) {
		if networkErr.Timeout() {
			return ai.NewLLMFailure(ai.LLMFailureTimeout, true, time.Second, err)
		}
		return ai.NewLLMFailure(ai.LLMFailureProviderUnavailable, true, time.Second, err)
	}
	return ai.NewLLMFailure(ai.LLMFailureInternal, false, 0, err)
}

// RetryDelay exposes the adapter's typed transient-failure decision.
func (p *Provider) RetryDelay(err error) (time.Duration, bool) {
	return ai.LLMFailureRetry(classifyOpenAIFailure(err))
}

func classifyOpenAIStreamCode(code string, err error) error {
	normalized := strings.ToLower(strings.TrimSpace(code))
	switch {
	case safetyCode(normalized):
		return ai.NewLLMFailure(ai.LLMFailureSafetyRefusal, false, 0, err)
	case strings.Contains(normalized, "rate_limit"), strings.Contains(normalized, "quota"):
		return ai.NewLLMFailure(ai.LLMFailureRateLimited, true, time.Second, err)
	case strings.Contains(normalized, "auth"), strings.Contains(normalized, "api_key"):
		return ai.NewLLMFailure(ai.LLMFailureAuthentication, false, 0, err)
	case strings.Contains(normalized, "invalid"):
		return ai.NewLLMFailure(ai.LLMFailureInvalidRequest, false, 0, err)
	case strings.Contains(normalized, "server"), strings.Contains(normalized, "overload"):
		return ai.NewLLMFailure(ai.LLMFailureProviderUnavailable, true, time.Second, err)
	default:
		return ai.NewLLMFailure(ai.LLMFailureInternal, false, 0, err)
	}
}

func openAIStatusFailure(status int) (ai.LLMFailureClass, bool) {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return ai.LLMFailureAuthentication, false
	case status == http.StatusTooManyRequests:
		return ai.LLMFailureRateLimited, true
	case status == http.StatusRequestTimeout || status == http.StatusGatewayTimeout:
		return ai.LLMFailureTimeout, true
	case status == http.StatusConflict || status == http.StatusTooEarly || status >= 500:
		return ai.LLMFailureProviderUnavailable, true
	case status >= 400 && status < 500:
		return ai.LLMFailureInvalidRequest, false
	default:
		return ai.LLMFailureInternal, false
	}
}

func safetyCode(raw string) bool {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	return strings.Contains(normalized, "content_filter") ||
		strings.Contains(normalized, "content_policy") ||
		strings.Contains(normalized, "prohibited_content") ||
		strings.Contains(normalized, "safety")
}

func openAIRetryAfter(response *http.Response) time.Duration {
	if response == nil {
		return time.Second
	}
	raw := strings.TrimSpace(response.Header.Get("Retry-After"))
	if raw == "" {
		return time.Second
	}
	var seconds int
	if _, err := fmt.Sscanf(raw, "%d", &seconds); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	return time.Second
}

var _ ai.LLMRetryDelayProvider = (*Provider)(nil)
