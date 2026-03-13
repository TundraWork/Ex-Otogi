package telegram

import (
	"errors"
	"strings"

	"ex-otogi/pkg/otogi/platform"

	"github.com/gotd/td/tgerr"
)

func mapTelegramOutboundError(
	operation platform.OutboundOperation,
	sink platform.EventSink,
	err error,
) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, platform.ErrInvalidOutboundRequest) {
		return err
	}

	outboundErr := &platform.OutboundError{
		Operation: operation,
		Kind:      platform.OutboundErrorKindUnknown,
		Platform:  sink.Platform,
		SinkID:    sink.ID,
		Cause:     err,
	}

	if retryAfter, ok := tgerr.AsFloodWait(err); ok {
		outboundErr.Kind = platform.OutboundErrorKindRateLimited
		outboundErr.RetryAfter = retryAfter
		if rpcErr, hasRPC := tgerr.As(err); hasRPC {
			outboundErr.Code = rpcErr.Code
			outboundErr.Type = rpcErr.Type
		}

		return outboundErr
	}

	rpcErr, ok := tgerr.As(err)
	if !ok {
		return outboundErr
	}

	outboundErr.Code = rpcErr.Code
	outboundErr.Type = rpcErr.Type
	outboundErr.Kind = classifyTelegramRPCError(rpcErr)

	return outboundErr
}

func classifyTelegramRPCError(rpcErr *tgerr.Error) platform.OutboundErrorKind {
	if rpcErr == nil {
		return platform.OutboundErrorKindUnknown
	}

	errorType := strings.ToUpper(strings.TrimSpace(rpcErr.Type))
	if rpcErr.Code == 420 || rpcErr.Code == 429 || strings.Contains(errorType, "FLOOD") {
		return platform.OutboundErrorKindRateLimited
	}

	switch rpcErr.Code {
	case 303:
		return platform.OutboundErrorKindTemporary
	case 400, 401, 403, 404, 405, 406:
		return platform.OutboundErrorKindPermanent
	case 500, 501, 502, 503, 504:
		return platform.OutboundErrorKindTemporary
	}
	if rpcErr.Code >= 500 {
		return platform.OutboundErrorKindTemporary
	}

	return platform.OutboundErrorKindUnknown
}
