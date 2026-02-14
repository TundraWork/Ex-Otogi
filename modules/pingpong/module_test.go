package pingpong

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ex-otogi/pkg/otogi"
)

func TestModuleHandleMessage(t *testing.T) {
	tests := []struct {
		name         string
		event        *otogi.Event
		configure    func(module *Module, dispatcher *captureDispatcher)
		wantErr      bool
		wantLogged   bool
		wantSentPong bool
	}{
		{
			name:         "exact ping triggers pong",
			event:        newMessageEvent("ping"),
			configure:    attachDispatcher,
			wantLogged:   true,
			wantSentPong: true,
		},
		{
			name:         "trimmed ping triggers pong",
			event:        newMessageEvent("  PiNg  "),
			configure:    attachDispatcher,
			wantLogged:   true,
			wantSentPong: true,
		},
		{
			name:         "non-ping does not trigger pong",
			event:        newMessageEvent("hello"),
			configure:    attachDispatcher,
			wantLogged:   false,
			wantSentPong: false,
		},
		{
			name:    "nil event returns error",
			event:   nil,
			wantErr: true,
		},
		{
			name: "missing message payload returns error",
			event: &otogi.Event{
				ID:   "e1",
				Kind: otogi.EventKindMessageCreated,
			},
			wantErr: true,
		},
		{
			name:    "ping without outbound dispatcher returns error",
			event:   newMessageEvent("ping"),
			wantErr: true,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			handler := &captureHandler{}
			module := newWithLogger(slog.New(handler))
			dispatcher := &captureDispatcher{
				messageID: "sent-1",
			}
			if testCase.configure != nil {
				testCase.configure(module, dispatcher)
			}

			err := module.handleMessage(context.Background(), testCase.event)
			if testCase.wantErr && err == nil {
				t.Fatal("expected error")
			}
			if !testCase.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			logged := handler.containsMessage("pong")
			if logged != testCase.wantLogged {
				t.Fatalf("logged pong = %v, want %v", logged, testCase.wantLogged)
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

func attachDispatcher(module *Module, dispatcher *captureDispatcher) {
	module.dispatcher = dispatcher
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

type captureHandler struct {
	mu       sync.Mutex
	messages []string
}

func (h *captureHandler) Enabled(context.Context, slog.Level) bool {
	return true
}

func (h *captureHandler) Handle(_ context.Context, record slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.messages = append(h.messages, record.Message)

	return nil
}

func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler {
	return h
}

func (h *captureHandler) WithGroup(string) slog.Handler {
	return h
}

func (h *captureHandler) containsMessage(target string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, message := range h.messages {
		if message == target {
			return true
		}
	}

	return false
}

type captureDispatcher struct {
	calls     atomic.Int64
	messageID string
}

func (d *captureDispatcher) SendMessage(
	_ context.Context,
	_ otogi.SendMessageRequest,
) (*otogi.OutboundMessage, error) {
	d.calls.Add(1)
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
