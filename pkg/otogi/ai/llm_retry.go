package ai

import "time"

// LLMRetryDelayProvider optionally classifies transient provider failures for
// caller-managed retries.
//
// Implementations inspect provider-specific wrapped errors and return one
// suggested delay when the failure is transient and safe to retry at the call
// site. Callers remain responsible for retry budgets, idempotency, and waiting.
type LLMRetryDelayProvider interface {
	// RetryDelay reports one suggested delay for retrying err.
	//
	// The boolean result is false when err should not be retried or when the
	// provider cannot classify the error.
	RetryDelay(err error) (time.Duration, bool)
}
