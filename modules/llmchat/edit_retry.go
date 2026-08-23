package llmchat

import (
	"context"
	"errors"
	"fmt"
	"time"

	"ex-otogi/pkg/otogi/platform"
)

const (
	editRetryBaseInterval          = 500 * time.Millisecond
	editRetryRateLimitBaseInterval = 2 * time.Second
	editRetryMinInterval           = 200 * time.Millisecond
	editRetryMaxAttempts           = 12
)

func (m *Module) retryEditMessage(ctx context.Context, request platform.EditMessageRequest) error {
	if ctx == nil {
		return fmt.Errorf("retry edit message %s: nil context", request.MessageID)
	}
	if err := ctx.Err(); err != nil {
		m.warnEditRetryExhausted(ctx, request, 0, err)
		return fmt.Errorf(
			"retry edit message %s exhausted before handler timeout after 0 attempts: %w",
			request.MessageID,
			err,
		)
	}

	attempts := 0
	for {
		attempts++
		editErr := m.dispatcher.EditMessage(ctx, request)
		if editErr == nil {
			return nil
		}
		if errors.Is(editErr, platform.ErrInvalidOutboundRequest) {
			return fmt.Errorf("retry edit message %s: non-retryable edit error: %w", request.MessageID, editErr)
		}
		if attempts >= editRetryMaxAttempts {
			m.warnEditRetryExhausted(ctx, request, attempts, editErr)
			return fmt.Errorf(
				"retry edit message %s exhausted after %d attempts: %w",
				request.MessageID,
				attempts,
				editErr,
			)
		}

		delay := nextRetryDelay(attempts, editErr)
		if waitErr := m.wait(ctx, delay); waitErr != nil {
			exhaustedErr := errors.Join(editErr, waitErr)
			m.warnEditRetryExhausted(ctx, request, attempts, exhaustedErr)
			return fmt.Errorf(
				"retry edit message %s exhausted before handler timeout after %d attempts: %w",
				request.MessageID,
				attempts,
				exhaustedErr,
			)
		}
	}
}

func (m *Module) wait(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	if m.sleep != nil {
		return m.sleep(ctx, delay)
	}

	return sleepWithContext(ctx, delay)
}

func nextRetryDelay(attempt int, err error) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if retryAfter, ok := platform.AsOutboundRateLimit(err); ok {
		if retryAfter > 0 {
			return clampDuration(retryAfter, editRetryMinInterval, maxEditInterval)
		}

		return exponentialBackoff(
			editRetryRateLimitBaseInterval,
			attempt,
			maxEditInterval,
		)
	}

	if hint, ok := parseRetryAfterHint(err); ok {
		return clampDuration(hint, editRetryMinInterval, maxEditInterval)
	}
	if isRateLimitError(err) {
		return exponentialBackoff(
			editRetryRateLimitBaseInterval,
			attempt,
			maxEditInterval,
		)
	}

	return exponentialBackoff(editRetryBaseInterval, attempt, maxEditInterval)
}

func exponentialBackoff(base time.Duration, attempt int, maxInterval time.Duration) time.Duration {
	if base <= 0 {
		base = editRetryBaseInterval
	}
	if maxInterval <= 0 {
		maxInterval = base
	}

	delay := base
	for retry := 1; retry < attempt; retry++ {
		if delay >= maxInterval {
			return maxInterval
		}
		delay *= 2
		if delay > maxInterval {
			return maxInterval
		}
	}

	return delay
}
