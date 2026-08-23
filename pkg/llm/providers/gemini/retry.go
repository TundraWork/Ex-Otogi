package gemini

import (
	"context"
	"errors"
	"strings"
	"time"

	"ex-otogi/pkg/otogi/ai"

	"google.golang.org/genai"
)

const (
	defaultTransientRetryDelay = time.Second
	maxTransientRetryDelay     = 5 * time.Second
)

func retryDelay(err error) (time.Duration, bool) {
	if err == nil {
		return 0, false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return 0, false
	}
	if delay, ok := ai.LLMFailureRetry(err); ok {
		return clampRetryDelay(delay), true
	}

	var apiErr genai.APIError
	if !errors.As(err, &apiErr) {
		return 0, false
	}

	if !isTransientAPIError(apiErr) {
		return 0, false
	}

	if delay, ok := parseRetryDelayDetails(apiErr.Details); ok {
		return clampRetryDelay(delay), true
	}

	return defaultTransientRetryDelay, true
}

// RetryDelay classifies transient Gemini API failures for caller-managed retries.
func (p *Provider) RetryDelay(err error) (time.Duration, bool) {
	return retryDelay(err)
}

func isTransientAPIError(err genai.APIError) bool {
	switch err.Code {
	case 429, 500, 502, 503, 504:
		return true
	}

	switch strings.ToUpper(strings.TrimSpace(err.Status)) {
	case "RESOURCE_EXHAUSTED", "UNAVAILABLE", "INTERNAL", "DEADLINE_EXCEEDED":
		return true
	default:
		return false
	}
}

func parseRetryDelayDetails(details []map[string]any) (time.Duration, bool) {
	for _, detail := range details {
		if detail == nil {
			continue
		}

		raw, ok := detail["retryDelay"]
		if !ok {
			continue
		}

		text, ok := raw.(string)
		if !ok {
			continue
		}

		delay, err := time.ParseDuration(strings.TrimSpace(text))
		if err != nil || delay <= 0 {
			continue
		}

		return delay, true
	}

	return 0, false
}

func clampRetryDelay(delay time.Duration) time.Duration {
	if delay <= 0 {
		return defaultTransientRetryDelay
	}
	if delay > maxTransientRetryDelay {
		return maxTransientRetryDelay
	}

	return delay
}

var _ ai.LLMRetryDelayProvider = (*Provider)(nil)
