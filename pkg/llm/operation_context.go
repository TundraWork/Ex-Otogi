package llm

import (
	"context"

	"ex-otogi/pkg/otogi/ai"
)

type attemptContextKey struct{}

// WithAttempt returns a child context carrying the one-based application-level
// attempt number for one provider operation. Values below one are normalized
// to the first attempt.
func WithAttempt(ctx context.Context, attempt int) context.Context {
	if attempt < 1 {
		attempt = 1
	}
	return context.WithValue(ctx, attemptContextKey{}, attempt)
}

// AttemptFromContext returns the one-based application-level provider attempt.
// Calls outside a retry boundary are treated as the first attempt.
func AttemptFromContext(ctx context.Context) int {
	if ctx != nil {
		if attempt, ok := ctx.Value(attemptContextKey{}).(int); ok && attempt > 0 {
			return attempt
		}
	}
	return 1
}

// ProviderFailureClass maps transport cancellation semantics to a stable coarse
// class. Provider-specific classification is refined by the chat lifecycle.
func ProviderFailureClass(err error) string {
	return string(ai.ClassifyLLMFailure(err))
}
