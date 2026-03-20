package discord

import (
	"errors"
	"time"

	"ex-otogi/pkg/otogi/platform"

	"github.com/bwmarrin/discordgo"
)

// mapDiscordOutboundError wraps a discordgo REST error into a structured
// platform.OutboundError with classified kind and retry metadata.
func mapDiscordOutboundError(
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
	if errors.Is(err, platform.ErrInvalidMediaDownloadRequest) {
		return err
	}

	outboundErr := &platform.OutboundError{
		Operation: operation,
		Kind:      platform.OutboundErrorKindUnknown,
		Platform:  sink.Platform,
		SinkID:    sink.ID,
		Cause:     err,
	}

	// Check for explicit rate-limit response from discordgo.
	var rateLimitErr discordgo.RateLimitError
	if errors.As(err, &rateLimitErr) {
		outboundErr.Kind = platform.OutboundErrorKindRateLimited
		outboundErr.RetryAfter = rateLimitErr.RetryAfter

		return outboundErr
	}

	// Check for REST API error with HTTP status code.
	var restErr *discordgo.RESTError
	if errors.As(err, &restErr) && restErr.Response != nil {
		outboundErr.Code = restErr.Response.StatusCode
		outboundErr.Kind = classifyDiscordHTTPStatus(restErr.Response.StatusCode)
		if outboundErr.Kind == platform.OutboundErrorKindRateLimited {
			outboundErr.RetryAfter = discordDefaultRateLimitRetry
		}

		return outboundErr
	}

	return outboundErr
}

const discordDefaultRateLimitRetry = 5 * time.Second

// classifyDiscordHTTPStatus maps a Discord HTTP response status code to
// an outbound error kind.
func classifyDiscordHTTPStatus(code int) platform.OutboundErrorKind {
	switch {
	case code == 429:
		return platform.OutboundErrorKindRateLimited
	case code >= 400 && code < 500:
		return platform.OutboundErrorKindPermanent
	case code >= 500:
		return platform.OutboundErrorKindTemporary
	default:
		return platform.OutboundErrorKindUnknown
	}
}
