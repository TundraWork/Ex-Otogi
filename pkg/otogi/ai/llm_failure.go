package ai

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// LLMFailureClass is a provider-independent generation failure category.
type LLMFailureClass string

const (
	// LLMFailureInvalidRequest means the request cannot be accepted as formed.
	LLMFailureInvalidRequest LLMFailureClass = "invalid_request"
	// LLMFailureAuthentication means provider credentials or authorization failed.
	LLMFailureAuthentication LLMFailureClass = "authentication"
	// LLMFailureRateLimited means provider capacity or quota temporarily rejected the request.
	LLMFailureRateLimited LLMFailureClass = "rate_limited"
	// LLMFailureProviderUnavailable means a transient provider or transport outage.
	LLMFailureProviderUnavailable LLMFailureClass = "provider_unavailable"
	// LLMFailureTimeout means the request deadline elapsed.
	LLMFailureTimeout LLMFailureClass = "timeout"
	// LLMFailureCanceled means the caller canceled the request.
	LLMFailureCanceled LLMFailureClass = "canceled"
	// LLMFailureSafetyRefusal means provider safety policy refused the request or response.
	LLMFailureSafetyRefusal LLMFailureClass = "safety_refusal"
	// LLMFailureEmptyResponse means generation completed without usable answer output.
	LLMFailureEmptyResponse LLMFailureClass = "empty_response"
	// LLMFailureTool means a requested tool could not be executed.
	LLMFailureTool LLMFailureClass = "tool_failure"
	// LLMFailureDelivery means the generated outcome could not be delivered.
	LLMFailureDelivery LLMFailureClass = "delivery_failure"
	// LLMFailureInternal is the fallback for an unclassified implementation failure.
	LLMFailureInternal LLMFailureClass = "internal"
)

// LLMFailure carries stable failure semantics while preserving the provider error.
type LLMFailure struct {
	Class      LLMFailureClass
	Retryable  bool
	RetryAfter time.Duration
	Err        error
}

// Error returns the underlying diagnostic message.
func (f *LLMFailure) Error() string {
	if f == nil || f.Err == nil {
		return "llm failure"
	}
	return f.Err.Error()
}

// Unwrap preserves the provider or orchestration cause.
func (f *LLMFailure) Unwrap() error {
	if f == nil {
		return nil
	}
	return f.Err
}

// NewLLMFailure creates a classified failure around err.
func NewLLMFailure(class LLMFailureClass, retryable bool, retryAfter time.Duration, err error) *LLMFailure {
	if err == nil {
		err = fmt.Errorf("%s", class)
	}
	return &LLMFailure{Class: class, Retryable: retryable, RetryAfter: retryAfter, Err: err}
}

// ClassifyLLMFailure returns the stable class for err.
func ClassifyLLMFailure(err error) LLMFailureClass {
	if err == nil {
		return ""
	}
	var failure *LLMFailure
	if errors.As(err, &failure) && failure.Class != "" {
		return failure.Class
	}
	switch {
	case errors.Is(err, context.Canceled):
		return LLMFailureCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return LLMFailureTimeout
	}
	return LLMFailureInternal
}

// LLMFailureRetry reports typed retry policy and its optional delay.
func LLMFailureRetry(err error) (time.Duration, bool) {
	var failure *LLMFailure
	if !errors.As(err, &failure) || !failure.Retryable {
		return 0, false
	}
	return failure.RetryAfter, true
}
