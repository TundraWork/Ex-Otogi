package gemini

import (
	"context"
	"errors"
	"strings"
	"time"

	"ex-otogi/pkg/otogi/ai"

	"google.golang.org/genai"
)

func classifyGeminiFailure(err error) error {
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

	var apiErr genai.APIError
	if errors.As(err, &apiErr) {
		class, retryable := geminiAPIFailure(apiErr)
		delay := time.Duration(0)
		if retryable {
			delay = defaultTransientRetryDelay
			if hinted, ok := parseRetryDelayDetails(apiErr.Details); ok {
				delay = clampRetryDelay(hinted)
			}
		}
		return ai.NewLLMFailure(class, retryable, delay, err)
	}
	return ai.NewLLMFailure(ai.LLMFailureInternal, false, 0, err)
}

func geminiAPIFailure(err genai.APIError) (ai.LLMFailureClass, bool) {
	status := strings.ToUpper(strings.TrimSpace(err.Status))
	switch {
	case err.Code == 401 || err.Code == 403 || status == "UNAUTHENTICATED" || status == "PERMISSION_DENIED":
		return ai.LLMFailureAuthentication, false
	case err.Code == 429 || status == "RESOURCE_EXHAUSTED":
		return ai.LLMFailureRateLimited, true
	case err.Code == 408 || err.Code == 504 || status == "DEADLINE_EXCEEDED":
		return ai.LLMFailureTimeout, true
	case err.Code >= 500 || status == "UNAVAILABLE" || status == "INTERNAL":
		return ai.LLMFailureProviderUnavailable, true
	case err.Code >= 400 && err.Code < 500 || status == "INVALID_ARGUMENT" || status == "FAILED_PRECONDITION":
		return ai.LLMFailureInvalidRequest, false
	default:
		return ai.LLMFailureInternal, false
	}
}
