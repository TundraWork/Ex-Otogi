package llmchat

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	floodWaitSecondsPattern = regexp.MustCompile(`(?i)flood[\s_-]*wait[\s_-]*(\d+)`)
	retryAfterPattern       = regexp.MustCompile(
		`(?i)retry[\s_-]*after[^\d]*(\d+)(?:\s*(ms|msec|millisecond|milliseconds|s|sec|secs|second|seconds))?`,
	)
)

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
