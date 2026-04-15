package llmchat

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"ex-otogi/pkg/otogi/ai"
)

const (
	llmRetryMinInterval  = 200 * time.Millisecond
	llmRetryBaseInterval = time.Second
	llmRetryMaxInterval  = 5 * time.Second
	llmRetryMaxAttempts  = 3
)

func retryLLMOperation[T any](
	ctx context.Context,
	sleep func(context.Context, time.Duration) error,
	logger *slog.Logger,
	operation string,
	provider ai.LLMProvider,
	execute func() (T, error),
) (T, error) {
	var zero T
	if execute == nil {
		return zero, fmt.Errorf("%s: nil execute function", strings.TrimSpace(operation))
	}
	if strings.TrimSpace(operation) == "" {
		operation = "llm operation"
	}
	if sleep == nil {
		sleep = sleepWithContext
	}

	var result T
	var err error
	for attempt := 1; attempt <= llmRetryMaxAttempts; attempt++ {
		result, err = execute()
		if err == nil {
			return result, nil
		}
		if attempt >= llmRetryMaxAttempts {
			return zero, fmt.Errorf("%s exhausted after %d attempts: %w", operation, attempt, err)
		}

		delay, retryable := nextLLMRetryDelay(provider, err)
		if !retryable {
			return zero, err
		}

		if logger != nil {
			logger.WarnContext(ctx, "llmchat retrying transient llm provider error",
				"operation", operation,
				"attempt", attempt,
				"next_attempt", attempt+1,
				"delay", delay,
				"error", err,
			)
		}
		if waitErr := sleep(ctx, delay); waitErr != nil {
			return zero, fmt.Errorf("%s retry wait after attempt %d: %w", operation, attempt, errors.Join(err, waitErr))
		}
	}

	return zero, fmt.Errorf("%s exhausted: unreachable", operation)
}

func nextLLMRetryDelay(provider ai.LLMProvider, err error) (time.Duration, bool) {
	if err == nil {
		return 0, false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return 0, false
	}

	if provider != nil {
		if retryProvider, ok := provider.(ai.LLMRetryDelayProvider); ok {
			if delay, ok := retryProvider.RetryDelay(err); ok {
				return clampDuration(delay, llmRetryMinInterval, llmRetryMaxInterval), true
			}
		}
	}
	if hint, ok := parseRetryAfterHint(err); ok {
		return clampDuration(hint, llmRetryMinInterval, llmRetryMaxInterval), true
	}
	if isGenericTransientLLMError(err) {
		return llmRetryBaseInterval, true
	}

	return 0, false
}

func isGenericTransientLLMError(err error) bool {
	if err == nil {
		return false
	}

	raw := strings.ToLower(err.Error())
	switch {
	case strings.Contains(raw, "error 408"),
		strings.Contains(raw, "error 409"),
		strings.Contains(raw, "error 425"),
		strings.Contains(raw, "error 429"),
		strings.Contains(raw, "error 500"),
		strings.Contains(raw, "error 502"),
		strings.Contains(raw, "error 503"),
		strings.Contains(raw, "error 504"),
		strings.Contains(raw, "status code: 408"),
		strings.Contains(raw, "status code: 409"),
		strings.Contains(raw, "status code: 425"),
		strings.Contains(raw, "status code: 429"),
		strings.Contains(raw, "status code: 500"),
		strings.Contains(raw, "status code: 502"),
		strings.Contains(raw, "status code: 503"),
		strings.Contains(raw, "status code: 504"),
		strings.Contains(raw, "rate limit"),
		strings.Contains(raw, "rate_limit"),
		strings.Contains(raw, "too many requests"),
		strings.Contains(raw, "resource exhausted"),
		strings.Contains(raw, "retry later"),
		strings.Contains(raw, "try again later"),
		strings.Contains(raw, "temporarily unavailable"),
		strings.Contains(raw, "service unavailable"),
		strings.Contains(raw, "high demand"),
		strings.Contains(raw, "overloaded"),
		strings.Contains(raw, "bad gateway"),
		strings.Contains(raw, "gateway timeout"),
		strings.Contains(raw, "server_error"),
		strings.Contains(raw, "internal server error"),
		strings.Contains(raw, "rate_limit_exceeded"):
		return true
	default:
		return false
	}
}
