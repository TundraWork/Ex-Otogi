package otogi

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// OutboundOperation identifies one outbound dispatcher operation type.
type OutboundOperation string

const (
	// OutboundOperationSendMessage identifies SendMessage operations.
	OutboundOperationSendMessage OutboundOperation = "send_message"
	// OutboundOperationEditMessage identifies EditMessage operations.
	OutboundOperationEditMessage OutboundOperation = "edit_message"
	// OutboundOperationDeleteMessage identifies DeleteMessage operations.
	OutboundOperationDeleteMessage OutboundOperation = "delete_message"
	// OutboundOperationSetReaction identifies SetReaction operations.
	OutboundOperationSetReaction OutboundOperation = "set_reaction"
)

// OutboundErrorKind describes coarse-grained outbound failure classification.
type OutboundErrorKind string

const (
	// OutboundErrorKindRateLimited indicates platform-side rate limiting.
	OutboundErrorKindRateLimited OutboundErrorKind = "rate_limited"
	// OutboundErrorKindTemporary indicates retryable transient failure.
	OutboundErrorKindTemporary OutboundErrorKind = "temporary"
	// OutboundErrorKindPermanent indicates non-retryable permanent failure.
	OutboundErrorKindPermanent OutboundErrorKind = "permanent"
	// OutboundErrorKindUnknown indicates unclassified failure.
	OutboundErrorKindUnknown OutboundErrorKind = "unknown"
)

// OutboundError carries structured metadata for one outbound operation failure.
type OutboundError struct {
	// Operation identifies which outbound operation failed.
	Operation OutboundOperation
	// Kind classifies whether and how callers should retry.
	Kind OutboundErrorKind
	// Platform identifies which destination platform produced the failure.
	Platform Platform
	// SinkID identifies which configured sink produced the failure when known.
	SinkID string
	// RetryAfter carries suggested retry delay for rate-limited failures when known.
	RetryAfter time.Duration
	// Code carries optional platform RPC/status code when known.
	Code int
	// Type carries optional platform error type token when known.
	Type string
	// Cause is the wrapped platform/transport error.
	Cause error
}

// Error returns one operator-readable failure summary.
func (e *OutboundError) Error() string {
	if e == nil {
		return "<nil>"
	}

	fields := make([]string, 0, 7)
	if operation := strings.TrimSpace(string(e.Operation)); operation != "" {
		fields = append(fields, "operation="+operation)
	}
	if kind := strings.TrimSpace(string(e.Kind)); kind != "" {
		fields = append(fields, "kind="+kind)
	}
	if platform := strings.TrimSpace(string(e.Platform)); platform != "" {
		fields = append(fields, "platform="+platform)
	}
	if sinkID := strings.TrimSpace(e.SinkID); sinkID != "" {
		fields = append(fields, "sink_id="+sinkID)
	}
	if e.RetryAfter > 0 {
		fields = append(fields, "retry_after="+e.RetryAfter.String())
	}
	if e.Code != 0 {
		fields = append(fields, fmt.Sprintf("code=%d", e.Code))
	}
	if errorType := strings.TrimSpace(e.Type); errorType != "" {
		fields = append(fields, "type="+errorType)
	}

	if len(fields) == 0 {
		if e.Cause == nil {
			return "outbound error"
		}
		return fmt.Sprintf("outbound error: %v", e.Cause)
	}

	if e.Cause == nil {
		return "outbound error: " + strings.Join(fields, " ")
	}
	return "outbound error: " + strings.Join(fields, " ") + ": " + e.Cause.Error()
}

// Unwrap returns the wrapped root cause.
func (e *OutboundError) Unwrap() error {
	if e == nil {
		return nil
	}

	return e.Cause
}

// AsOutboundError extracts one OutboundError from wrapped error chains.
func AsOutboundError(err error) (*OutboundError, bool) {
	if err == nil {
		return nil, false
	}

	var outboundErr *OutboundError
	if errors.As(err, &outboundErr) {
		return outboundErr, true
	}

	return nil, false
}

// AsOutboundRateLimit extracts retry delay metadata from outbound rate-limit errors.
//
// It returns `(0, false)` if err is not classified as rate-limited.
// It returns `(0, true)` when rate-limited but no retry-after hint is known.
func AsOutboundRateLimit(err error) (time.Duration, bool) {
	outboundErr, ok := AsOutboundError(err)
	if !ok || outboundErr == nil || outboundErr.Kind != OutboundErrorKindRateLimited {
		return 0, false
	}

	return outboundErr.RetryAfter, true
}
