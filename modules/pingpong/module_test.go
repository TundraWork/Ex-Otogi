package pingpong

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"ex-otogi/pkg/otogi"
)

func TestModuleHandleMessage(t *testing.T) {
	tests := []struct {
		name         string
		event        *otogi.Event
		sendErr      error
		wantErr      bool
		wantSentPong bool
	}{
		{
			name:         "exact ping triggers pong",
			event:        newMessageEvent("ping"),
			wantSentPong: true,
		},
		{
			name:         "trimmed ping triggers pong",
			event:        newMessageEvent("  PiNg  "),
			wantSentPong: true,
		},
		{
			name:         "non-ping does not trigger pong",
			event:        newMessageEvent("hello"),
			wantSentPong: false,
		},
		{
			name:         "ping send failure returns error",
			event:        newMessageEvent("ping"),
			sendErr:      errors.New("dispatcher failure"),
			wantErr:      true,
			wantSentPong: true,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			module := New()
			dispatcher := &captureDispatcher{
				messageID: "sent-1",
				sendErr:   testCase.sendErr,
			}
			module.dispatcher = dispatcher

			err := module.handleMessage(context.Background(), testCase.event)
			if testCase.wantErr && err == nil {
				t.Fatal("expected error")
			}
			if !testCase.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			sentPong := dispatcher.calls.Load() > 0
			if sentPong != testCase.wantSentPong {
				t.Fatalf("sent pong = %v, want %v", sentPong, testCase.wantSentPong)
			}
		})
	}
}

func TestModuleOnRegister(t *testing.T) {
	t.Parallel()

	module := New()
	dispatcher := &captureDispatcher{messageID: "sent-1"}
	runtime := moduleRuntimeStub{
		registry: serviceRegistryStub{
			values: map[string]any{
				otogi.ServiceOutboundDispatcher: dispatcher,
			},
		},
	}

	if err := module.OnRegister(context.Background(), runtime); err != nil {
		t.Fatalf("OnRegister failed: %v", err)
	}
	if module.dispatcher == nil {
		t.Fatal("expected outbound dispatcher to be configured")
	}
}

func TestModuleSpecUsesStrictMessageCapability(t *testing.T) {
	t.Parallel()

	module := New()
	spec := module.Spec()
	if len(spec.Handlers) != 1 {
		t.Fatalf("handler count = %d, want 1", len(spec.Handlers))
	}

	handler := spec.Handlers[0]
	if !handler.Capability.Interest.RequireMessage {
		t.Fatal("expected RequireMessage to be true")
	}
	if handler.Subscription.Buffer != 0 || handler.Subscription.Workers != 0 || handler.Subscription.HandlerTimeout != 0 {
		t.Fatalf("expected subscription to defer runtime defaults, got %#v", handler.Subscription)
	}

	required := map[string]bool{}
	for _, serviceName := range handler.Capability.RequiredServices {
		required[serviceName] = true
	}
	if !required[otogi.ServiceOutboundDispatcher] {
		t.Fatalf("required services missing %s", otogi.ServiceOutboundDispatcher)
	}
	if len(required) != 1 {
		t.Fatalf("required service count = %d, want 1", len(required))
	}
}

func newMessageEvent(text string) *otogi.Event {
	return &otogi.Event{
		ID:         "event-1",
		Kind:       otogi.EventKindMessageCreated,
		OccurredAt: time.Unix(1, 0),
		Platform:   otogi.PlatformTelegram,
		Conversation: otogi.Conversation{
			ID:   "42",
			Type: otogi.ConversationTypePrivate,
		},
		Message: &otogi.Message{
			ID:   "msg-1",
			Text: text,
		},
	}
}

type captureDispatcher struct {
	calls     atomic.Int64
	messageID string
	sendErr   error
}

func (d *captureDispatcher) SendMessage(
	_ context.Context,
	_ otogi.SendMessageRequest,
) (*otogi.OutboundMessage, error) {
	d.calls.Add(1)
	if d.sendErr != nil {
		return nil, d.sendErr
	}

	return &otogi.OutboundMessage{
		ID: d.messageID,
	}, nil
}

func (d *captureDispatcher) EditMessage(context.Context, otogi.EditMessageRequest) error {
	return nil
}

func (d *captureDispatcher) DeleteMessage(context.Context, otogi.DeleteMessageRequest) error {
	return nil
}

func (d *captureDispatcher) SetReaction(context.Context, otogi.SetReactionRequest) error {
	return nil
}

type moduleRuntimeStub struct {
	registry otogi.ServiceRegistry
}

func (s moduleRuntimeStub) Services() otogi.ServiceRegistry {
	return s.registry
}

func (moduleRuntimeStub) Subscribe(
	context.Context,
	otogi.InterestSet,
	otogi.SubscriptionSpec,
	otogi.EventHandler,
) (otogi.Subscription, error) {
	return nil, nil
}

type serviceRegistryStub struct {
	values map[string]any
}

func (s serviceRegistryStub) Register(string, any) error {
	return nil
}

func (s serviceRegistryStub) Resolve(name string) (any, error) {
	value, ok := s.values[name]
	if !ok {
		return nil, otogi.ErrServiceNotFound
	}

	return value, nil
}
