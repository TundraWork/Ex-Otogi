package telegram

import (
	"errors"
	"strings"

	"ex-otogi/pkg/otogi"

	"github.com/gotd/td/tgerr"
)

func mapTelegramOutboundError(
	operation otogi.OutboundOperation,
	sink otogi.EventSink,
	err error,
) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, otogi.ErrInvalidOutboundRequest) {
		return err
	}

	outboundErr := &otogi.OutboundError{
		Operation: operation,
		Kind:      otogi.OutboundErrorKindUnknown,
		Platform:  sink.Platform,
		SinkID:    sink.ID,
		Cause:     err,
	}

	if retryAfter, ok := tgerr.AsFloodWait(err); ok {
		outboundErr.Kind = otogi.OutboundErrorKindRateLimited
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

func classifyTelegramRPCError(rpcErr *tgerr.Error) otogi.OutboundErrorKind {
	if rpcErr == nil {
		return otogi.OutboundErrorKindUnknown
	}

	errorType := strings.ToUpper(strings.TrimSpace(rpcErr.Type))
	if rpcErr.Code == 420 || rpcErr.Code == 429 || strings.Contains(errorType, "FLOOD") {
		return otogi.OutboundErrorKindRateLimited
	}

	switch rpcErr.Code {
	case 303:
		return otogi.OutboundErrorKindTemporary
	case 400, 401, 403, 404, 405, 406:
		return otogi.OutboundErrorKindPermanent
	case 500, 501, 502, 503, 504:
		return otogi.OutboundErrorKindTemporary
	}
	if rpcErr.Code >= 500 {
		return otogi.OutboundErrorKindTemporary
	}

	return otogi.OutboundErrorKindUnknown
}
