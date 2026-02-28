package llmchat

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"ex-otogi/pkg/otogi"
)

const (
	editRetryBaseInterval          = 500 * time.Millisecond
	editRetryRateLimitBaseInterval = 2 * time.Second
	editRetryMinInterval           = 200 * time.Millisecond
)

var (
	floodWaitSecondsPattern = regexp.MustCompile(`(?i)flood[\s_-]*wait[\s_-]*(\d+)`)
	retryAfterPattern       = regexp.MustCompile(
		`(?i)retry[\s_-]*after[^\d]*(\d+)(?:\s*(ms|msec|millisecond|milliseconds|s|sec|secs|second|seconds))?`,
	)
)

func (m *Module) retryEditMessage(ctx context.Context, request otogi.EditMessageRequest) error {
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
		if errors.Is(editErr, otogi.ErrInvalidOutboundRequest) {
			return fmt.Errorf("retry edit message %s: non-retryable edit error: %w", request.MessageID, editErr)
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

func sleepWithContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return fmt.Errorf("sleep with context: %w", ctx.Err())
	case <-timer.C:
		return nil
	}
}

func nextRetryDelay(attempt int, err error) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if retryAfter, ok := otogi.AsOutboundRateLimit(err); ok {
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

func parseRetryAfterHint(err error) (time.Duration, bool) {
	if err == nil {
		return 0, false
	}

	raw := strings.ToLower(err.Error())
	if duration, ok := parseDurationSeconds(raw, floodWaitSecondsPattern); ok {
		return duration, true
	}

	matches := retryAfterPattern.FindStringSubmatch(raw)
	if len(matches) == 0 {
		return 0, false
	}

	amount, convErr := strconv.Atoi(matches[1])
	if convErr != nil || amount <= 0 {
		return 0, false
	}

	unit := strings.TrimSpace(matches[2])
	switch unit {
	case "ms", "msec", "millisecond", "milliseconds":
		return time.Duration(amount) * time.Millisecond, true
	default:
		return time.Duration(amount) * time.Second, true
	}
}

func parseDurationSeconds(raw string, pattern *regexp.Regexp) (time.Duration, bool) {
	matches := pattern.FindStringSubmatch(raw)
	if len(matches) != 2 {
		return 0, false
	}

	seconds, convErr := strconv.Atoi(matches[1])
	if convErr != nil || seconds <= 0 {
		return 0, false
	}

	return time.Duration(seconds) * time.Second, true
}

func clampDuration(value, minInterval, maxInterval time.Duration) time.Duration {
	if minInterval > 0 && value < minInterval {
		return minInterval
	}
	if maxInterval > 0 && value > maxInterval {
		return maxInterval
	}

	return value
}
