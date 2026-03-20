package discord

import (
	"errors"
	"net/http"
	"testing"

	"ex-otogi/pkg/otogi/platform"

	"github.com/bwmarrin/discordgo"
)

func TestMapDiscordOutboundError(t *testing.T) {
	t.Parallel()

	testSink := platform.EventSink{Platform: DriverPlatform, ID: "bot-test"}

	tests := []struct {
		name      string
		err       error
		wantNil   bool
		wantKind  platform.OutboundErrorKind
		wantRetry bool
	}{
		{
			name:    "nil error passthrough",
			err:     nil,
			wantNil: true,
		},
		{
			name: "invalid outbound request passthrough",
			err:  platform.ErrInvalidOutboundRequest,
		},
		{
			name:     "unknown error classified as unknown",
			err:      errors.New("something unexpected"),
			wantKind: platform.OutboundErrorKindUnknown,
		},
		{
			name: "RESTError 429 classified as rate limited",
			err: &discordgo.RESTError{
				Response: &http.Response{StatusCode: 429},
			},
			wantKind:  platform.OutboundErrorKindRateLimited,
			wantRetry: true,
		},
		{
			name: "RESTError 403 classified as permanent",
			err: &discordgo.RESTError{
				Response: &http.Response{StatusCode: 403},
			},
			wantKind: platform.OutboundErrorKindPermanent,
		},
		{
			name: "RESTError 404 classified as permanent",
			err: &discordgo.RESTError{
				Response: &http.Response{StatusCode: 404},
			},
			wantKind: platform.OutboundErrorKindPermanent,
		},
		{
			name: "RESTError 500 classified as temporary",
			err: &discordgo.RESTError{
				Response: &http.Response{StatusCode: 500},
			},
			wantKind: platform.OutboundErrorKindTemporary,
		},
		{
			name: "RESTError 503 classified as temporary",
			err: &discordgo.RESTError{
				Response: &http.Response{StatusCode: 503},
			},
			wantKind: platform.OutboundErrorKindTemporary,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			result := mapDiscordOutboundError(platform.OutboundOperationSendMessage, testSink, testCase.err)

			if testCase.wantNil {
				if result != nil {
					t.Errorf("expected nil error, got: %v", result)
				}

				return
			}

			if errors.Is(testCase.err, platform.ErrInvalidOutboundRequest) {
				if !errors.Is(result, platform.ErrInvalidOutboundRequest) {
					t.Errorf("expected ErrInvalidOutboundRequest passthrough, got: %v", result)
				}

				return
			}

			if testCase.wantKind == "" {
				return
			}

			outboundErr, ok := platform.AsOutboundError(result)
			if !ok {
				t.Fatalf("result is not OutboundError: %v", result)
			}
			if outboundErr.Kind != testCase.wantKind {
				t.Errorf("Kind = %q, want %q", outboundErr.Kind, testCase.wantKind)
			}
			if outboundErr.Platform != DriverPlatform {
				t.Errorf("Platform = %q, want %q", outboundErr.Platform, DriverPlatform)
			}
			if testCase.wantRetry && outboundErr.RetryAfter == 0 {
				t.Error("expected non-zero RetryAfter for rate-limited error")
			}
		})
	}
}

func TestClassifyDiscordHTTPStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		code int
		want platform.OutboundErrorKind
	}{
		{429, platform.OutboundErrorKindRateLimited},
		{400, platform.OutboundErrorKindPermanent},
		{401, platform.OutboundErrorKindPermanent},
		{403, platform.OutboundErrorKindPermanent},
		{404, platform.OutboundErrorKindPermanent},
		{499, platform.OutboundErrorKindPermanent},
		{500, platform.OutboundErrorKindTemporary},
		{502, platform.OutboundErrorKindTemporary},
		{503, platform.OutboundErrorKindTemporary},
		{200, platform.OutboundErrorKindUnknown},
		{0, platform.OutboundErrorKindUnknown},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(http.StatusText(tc.code), func(t *testing.T) {
			t.Parallel()

			got := classifyDiscordHTTPStatus(tc.code)
			if got != tc.want {
				t.Errorf("classifyDiscordHTTPStatus(%d) = %q, want %q", tc.code, got, tc.want)
			}
		})
	}
}
