package llmchat

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"ex-otogi/pkg/otogi/platform"
)

func TestRetryEditMessageExhaustsAtMaxAttempts(t *testing.T) {
	t.Parallel()

	module := newTestModule(validModuleConfig())

	sink := &sinkDispatcherStub{
		editErrors: make([]error, editRetryMaxAttempts+2),
	}
	for idx := range sink.editErrors {
		sink.editErrors[idx] = errors.New("429 too many requests")
	}
	module.dispatcher = sink

	sleepCalls := 0
	module.sleep = func(_ context.Context, _ time.Duration) error {
		sleepCalls++
		return nil
	}

	err := module.retryEditMessage(context.Background(), platform.EditMessageRequest{
		MessageID: "msg-1",
		Text:      "hello",
	})
	if err == nil {
		t.Fatal("retryEditMessage error = nil, want exhaustion error")
	}
	if !strings.Contains(err.Error(), "exhausted after 12 attempts") {
		t.Fatalf("error = %q, want exhausted-after-attempts context", err)
	}
	if len(sink.editRequests) != editRetryMaxAttempts {
		t.Fatalf("edit calls = %d, want %d", len(sink.editRequests), editRetryMaxAttempts)
	}
	if sleepCalls != editRetryMaxAttempts-1 {
		t.Fatalf("sleep calls = %d, want %d", sleepCalls, editRetryMaxAttempts-1)
	}
}

func TestRetryEditMessageSucceedsWithinMaxAttempts(t *testing.T) {
	t.Parallel()

	module := newTestModule(validModuleConfig())

	sink := &sinkDispatcherStub{
		editErrors: []error{
			errors.New("429 too many requests"),
			errors.New("429 too many requests"),
			nil,
		},
	}
	module.dispatcher = sink

	sleepCalls := 0
	module.sleep = func(_ context.Context, _ time.Duration) error {
		sleepCalls++
		return nil
	}

	if err := module.retryEditMessage(context.Background(), platform.EditMessageRequest{
		MessageID: "msg-1",
		Text:      "hello",
	}); err != nil {
		t.Fatalf("retryEditMessage failed: %v", err)
	}
	if len(sink.editRequests) != 3 {
		t.Fatalf("edit calls = %d, want 3", len(sink.editRequests))
	}
	if sleepCalls != 2 {
		t.Fatalf("sleep calls = %d, want 2", sleepCalls)
	}
}
