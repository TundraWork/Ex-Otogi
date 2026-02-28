package llmchat

import (
	"context"
	"time"

	"ex-otogi/pkg/otogi"
)

func (m *Module) warnIntermediateEditFailure(
	ctx context.Context,
	target otogi.OutboundTarget,
	placeholderMessageID string,
	attempts int,
	err error,
) {
	m.logDeliveryWarning(
		ctx,
		"llmchat intermediate edit failed",
		target,
		placeholderMessageID,
		attempts,
		err,
	)
}

func (m *Module) warnEditRetryExhausted(
	ctx context.Context,
	request otogi.EditMessageRequest,
	attempts int,
	err error,
) {
	m.logDeliveryWarning(
		ctx,
		"llmchat edit retry exhausted",
		request.Target,
		request.MessageID,
		attempts,
		err,
	)
}

func (m *Module) logDeliveryWarning(
	ctx context.Context,
	message string,
	target otogi.OutboundTarget,
	placeholderMessageID string,
	attempts int,
	err error,
) {
	if m == nil || m.logger == nil {
		return
	}

	operation, kind, retryAfter := describeOutboundEditError(err)
	m.logger.WarnContext(
		ctx,
		message,
		"conversation_id",
		target.Conversation.ID,
		"placeholder_message_id",
		placeholderMessageID,
		"operation",
		string(operation),
		"kind",
		string(kind),
		"retry_after",
		retryAfter,
		"attempts",
		attempts,
		"deadline_remaining",
		describeContextDeadlineRemaining(ctx),
		"error",
		err,
	)
}

func describeOutboundEditError(err error) (
	operation otogi.OutboundOperation,
	kind otogi.OutboundErrorKind,
	retryAfter time.Duration,
) {
	operation = otogi.OutboundOperationEditMessage
	kind = otogi.OutboundErrorKindUnknown

	if err == nil {
		return operation, kind, 0
	}

	if outboundErr, ok := otogi.AsOutboundError(err); ok {
		if outboundErr.Operation != "" {
			operation = outboundErr.Operation
		}
		if outboundErr.Kind != "" {
			kind = outboundErr.Kind
		}

		return operation, kind, outboundErr.RetryAfter
	}

	if hint, ok := parseRetryAfterHint(err); ok {
		return operation, otogi.OutboundErrorKindRateLimited, hint
	}
	if isRateLimitError(err) {
		return operation, otogi.OutboundErrorKindRateLimited, 0
	}

	return operation, kind, 0
}
