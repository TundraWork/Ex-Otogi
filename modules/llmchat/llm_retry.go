package llmchat

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"ex-otogi/pkg/llm"
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
	execute func(context.Context) (T, error),
) (T, error) {
	return retryLLMOperationWhen(ctx, sleep, logger, operation, provider, execute, nil)
}

func retryLLMOperationWhen[T any](
	ctx context.Context,
	sleep func(context.Context, time.Duration) error,
	logger *slog.Logger,
	operation string,
	provider ai.LLMProvider,
	execute func(context.Context) (T, error),
	canRetry func(T, error) bool,
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
		attemptCtx := llm.WithAttempt(ctx, attempt)
		result, err = execute(attemptCtx)
		if err == nil {
			return result, nil
		}
		if attempt >= llmRetryMaxAttempts {
			return result, fmt.Errorf("%s exhausted after %d attempts: %w", operation, attempt, err)
		}

		delay, retryable := nextLLMRetryDelay(provider, err)
		if !retryable || canRetry != nil && !canRetry(result, err) {
			return result, err
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
	if delay, ok := ai.LLMFailureRetry(err); ok {
		return clampDuration(delay, llmRetryMinInterval, llmRetryMaxInterval), true
	}

	if provider != nil {
		if retryProvider, ok := provider.(ai.LLMRetryDelayProvider); ok {
			if delay, ok := retryProvider.RetryDelay(err); ok {
				return clampDuration(delay, llmRetryMinInterval, llmRetryMaxInterval), true
			}
		}
	}
	return 0, false
}
