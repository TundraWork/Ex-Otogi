package llmchat

import (
	"context"
	"time"

	"ex-otogi/pkg/otogi/platform"
)

func (m *Module) warnIntermediateEditFailure(
	ctx context.Context,
	target platform.OutboundTarget,
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
	request platform.EditMessageRequest,
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
	target platform.OutboundTarget,
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

func (m *Module) warnMarkdownParseFallback(
	ctx context.Context,
	target platform.OutboundTarget,
	placeholderMessageID string,
	err error,
) {
	if m == nil || m.logger == nil || err == nil {
		return
	}

	m.logger.WarnContext(
		ctx,
		"llmchat markdown parse fallback to plain text",
		"conversation_id",
		target.Conversation.ID,
		"placeholder_message_id",
		placeholderMessageID,
		"deadline_remaining",
		describeContextDeadlineRemaining(ctx),
		"error",
		err,
	)
}

func describeOutboundEditError(err error) (
	operation platform.OutboundOperation,
	kind platform.OutboundErrorKind,
	retryAfter time.Duration,
) {
	operation = platform.OutboundOperationEditMessage
	kind = platform.OutboundErrorKindUnknown

	if err == nil {
		return operation, kind, 0
	}

	if outboundErr, ok := platform.AsOutboundError(err); ok {
		if outboundErr.Operation != "" {
			operation = outboundErr.Operation
		}
		if outboundErr.Kind != "" {
			kind = outboundErr.Kind
		}

		return operation, kind, outboundErr.RetryAfter
	}

	if hint, ok := parseRetryAfterHint(err); ok {
		return operation, platform.OutboundErrorKindRateLimited, hint
	}
	if isRateLimitError(err) {
		return operation, platform.OutboundErrorKindRateLimited, 0
	}

	return operation, kind, 0
}
