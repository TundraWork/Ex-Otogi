package otogi

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestAsOutboundErrorPreservesUnwrap(t *testing.T) {
	t.Parallel()

	rootCause := errors.New("rpc failed")
	err := fmt.Errorf(
		"outer wrapper: %w",
		&OutboundError{
			Operation:  OutboundOperationEditMessage,
			Kind:       OutboundErrorKindTemporary,
			Platform:   PlatformTelegram,
			SinkID:     "tg-main",
			RetryAfter: 3 * time.Second,
			Code:       500,
			Type:       "INTERNAL",
			Cause:      rootCause,
		},
	)

	outboundErr, ok := AsOutboundError(err)
	if !ok {
		t.Fatal("AsOutboundError = false, want true")
	}
	if outboundErr.Kind != OutboundErrorKindTemporary {
		t.Fatalf("kind = %s, want %s", outboundErr.Kind, OutboundErrorKindTemporary)
	}
	if outboundErr.Operation != OutboundOperationEditMessage {
		t.Fatalf("operation = %s, want %s", outboundErr.Operation, OutboundOperationEditMessage)
	}
	if !errors.Is(err, rootCause) {
		t.Fatalf("errors.Is(err, rootCause) = false, want true (err=%v)", err)
	}
}

func TestAsOutboundRateLimit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		err          error
		wantDuration time.Duration
		wantOK       bool
	}{
		{
			name: "non outbound error",
			err:  errors.New("plain"),
		},
		{
			name: "outbound non rate limited",
			err: &OutboundError{
				Operation: OutboundOperationSendMessage,
				Kind:      OutboundErrorKindPermanent,
				Platform:  PlatformTelegram,
				SinkID:    "tg-main",
				Cause:     errors.New("bad request"),
			},
		},
		{
			name: "rate limited with retry after",
			err: fmt.Errorf(
				"wrapped: %w",
				&OutboundError{
					Operation:  OutboundOperationEditMessage,
					Kind:       OutboundErrorKindRateLimited,
					Platform:   PlatformTelegram,
					SinkID:     "tg-main",
					RetryAfter: 7 * time.Second,
					Cause:      errors.New("flood wait"),
				},
			),
			wantDuration: 7 * time.Second,
			wantOK:       true,
		},
		{
			name: "rate limited without retry after",
			err: &OutboundError{
				Operation: OutboundOperationEditMessage,
				Kind:      OutboundErrorKindRateLimited,
				Platform:  PlatformTelegram,
				SinkID:    "tg-main",
				Cause:     errors.New("flood wait unknown"),
			},
			wantOK: true,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			gotDuration, gotOK := AsOutboundRateLimit(testCase.err)
			if gotOK != testCase.wantOK {
				t.Fatalf("ok = %v, want %v", gotOK, testCase.wantOK)
			}
			if gotDuration != testCase.wantDuration {
				t.Fatalf("duration = %s, want %s", gotDuration, testCase.wantDuration)
			}
		})
	}
}
