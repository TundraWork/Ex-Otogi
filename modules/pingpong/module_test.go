package pingpong

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ex-otogi/pkg/otogi/core"
	"ex-otogi/pkg/otogi/platform"
)

func TestModuleHandleCommand(t *testing.T) {
	tests := []struct {
		name         string
		event        *platform.Event
		sendErr      error
		wantErr      bool
		wantSentPong bool
	}{
		{
			name:         "ordinary ping command triggers pong",
			event:        newCommandEvent("/ping"),
			wantSentPong: true,
		},
		{
			name:         "ping command with mention triggers pong",
			event:        newCommandEvent("/ping@mybot"),
			wantSentPong: true,
		},
		{
			name:         "system ping command is ignored",
			event:        newCommandEvent("~ping"),
			wantSentPong: false,
		},
		{
			name:         "non-ping command is ignored",
			event:        newCommandEvent("/hello"),
			wantSentPong: false,
		},
		{
			name:         "missing command payload is ignored",
			event:        newMissingCommandPayloadEvent(),
			wantSentPong: false,
		},
		{
			name:         "ping send failure returns error",
			event:        newCommandEvent("/ping"),
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

			err := module.handleCommand(context.Background(), testCase.event)
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
			if !sentPong {
				return
			}

			if dispatcher.lastRequest.Text != "pong!" {
				t.Fatalf("sent text = %q, want pong!", dispatcher.lastRequest.Text)
			}
			if dispatcher.lastRequest.ReplyToMessageID != testCase.event.Article.ID {
				t.Fatalf(
					"reply_to = %q, want %q",
					dispatcher.lastRequest.ReplyToMessageID,
					testCase.event.Article.ID,
				)
			}
			if dispatcher.lastRequest.Target.Sink == nil {
				t.Fatal("target sink = nil, want source sink")
			}
			if dispatcher.lastRequest.Target.Sink.ID != "tg-main" {
				t.Fatalf("target sink id = %q, want tg-main", dispatcher.lastRequest.Target.Sink.ID)
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
				platform.ServiceSinkDispatcher: dispatcher,
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

func TestModuleSpecUsesCommandCapability(t *testing.T) {
	t.Parallel()

	module := New()
	spec := module.Spec()
	if len(spec.Handlers) != 1 {
		t.Fatalf("handler count = %d, want 1", len(spec.Handlers))
	}
	if len(spec.Commands) != 1 {
		t.Fatalf("command count = %d, want 1", len(spec.Commands))
	}
	if spec.Commands[0].Prefix != platform.CommandPrefixOrdinary {
		t.Fatalf("command prefix = %q, want %q", spec.Commands[0].Prefix, platform.CommandPrefixOrdinary)
	}
	if spec.Commands[0].Name != pingCommandName {
		t.Fatalf("command name = %q, want %q", spec.Commands[0].Name, pingCommandName)
	}

	handler := spec.Handlers[0]
	if !handler.Capability.Interest.RequireArticle {
		t.Fatal("expected RequireArticle to be true")
	}
	if !handler.Capability.Interest.RequireCommand {
		t.Fatal("expected RequireCommand to be true")
	}
	if len(handler.Capability.Interest.Kinds) != 1 || handler.Capability.Interest.Kinds[0] != platform.EventKindCommandReceived {
		t.Fatalf("kinds = %v, want [%s]", handler.Capability.Interest.Kinds, platform.EventKindCommandReceived)
	}
	if len(handler.Capability.Interest.CommandNames) != 1 || handler.Capability.Interest.CommandNames[0] != pingCommandName {
		t.Fatalf("command names = %v, want [%s]", handler.Capability.Interest.CommandNames, pingCommandName)
	}
	if handler.Subscription.Buffer != 0 || handler.Subscription.Workers != 0 || handler.Subscription.HandlerTimeout != 0 {
		t.Fatalf("expected subscription to defer runtime defaults, got %#v", handler.Subscription)
	}

	required := map[string]bool{}
	for _, serviceName := range handler.Capability.RequiredServices {
		required[serviceName] = true
	}
	if !required[platform.ServiceSinkDispatcher] {
		t.Fatalf("required services missing %s", platform.ServiceSinkDispatcher)
	}
	if len(required) != 1 {
		t.Fatalf("required service count = %d, want 1", len(required))
	}
}

func newCommandEvent(text string) *platform.Event {
	candidate, matched, err := platform.ParseCommandCandidate(text)
	if err != nil {
		panic(err)
	}
	if !matched {
		panic("newCommandEvent expects command text")
	}
	commandKind := platform.EventKindCommandReceived
	if candidate.Prefix == platform.CommandPrefixSystem {
		commandKind = platform.EventKindSystemCommandReceived
	}

	return &platform.Event{
		ID:         "event-1",
		Kind:       commandKind,
		OccurredAt: time.Unix(1, 0).UTC(),
		Source: platform.EventSource{
			Platform: platform.PlatformTelegram,
			ID:       "tg-main",
		},
		Conversation: platform.Conversation{
			ID:   "42",
			Type: platform.ConversationTypePrivate,
		},
		Article: &platform.Article{
			ID:   "msg-1",
			Text: text,
		},
		Command: &platform.CommandInvocation{
			Name:            candidate.Name,
			Mention:         candidate.Mention,
			Value:           strings.Join(candidate.Tokens, " "),
			SourceEventID:   "source-event-1",
			SourceEventKind: platform.EventKindArticleCreated,
			RawInput:        text,
		},
	}
}

func newMissingCommandPayloadEvent() *platform.Event {
	return &platform.Event{
		ID:         "event-1",
		Kind:       platform.EventKindCommandReceived,
		OccurredAt: time.Unix(1, 0).UTC(),
		Source: platform.EventSource{
			Platform: platform.PlatformTelegram,
			ID:       "tg-main",
		},
		Conversation: platform.Conversation{
			ID:   "42",
			Type: platform.ConversationTypePrivate,
		},
		Article: &platform.Article{
			ID:   "msg-1",
			Text: "/ping",
		},
	}
}

type captureDispatcher struct {
	calls       atomic.Int64
	messageID   string
	sendErr     error
	lastRequest platform.SendMessageRequest
}

func (d *captureDispatcher) SendMessage(
	_ context.Context,
	request platform.SendMessageRequest,
) (*platform.OutboundMessage, error) {
	d.calls.Add(1)
	d.lastRequest = request
	if d.sendErr != nil {
		return nil, d.sendErr
	}

	return &platform.OutboundMessage{
		ID: d.messageID,
	}, nil
}

func (d *captureDispatcher) EditMessage(context.Context, platform.EditMessageRequest) error {
	return nil
}

func (d *captureDispatcher) DeleteMessage(context.Context, platform.DeleteMessageRequest) error {
	return nil
}

func (d *captureDispatcher) SetReaction(context.Context, platform.SetReactionRequest) error {
	return nil
}

func (d *captureDispatcher) ListSinks(context.Context) ([]platform.EventSink, error) {
	return nil, nil
}

func (d *captureDispatcher) ListSinksByPlatform(
	context.Context,
	platform.Platform,
) ([]platform.EventSink, error) {
	return nil, nil
}

type moduleRuntimeStub struct {
	registry core.ServiceRegistry
	configs  core.ConfigRegistry
}

func (s moduleRuntimeStub) Services() core.ServiceRegistry {
	return s.registry
}

func (s moduleRuntimeStub) Config() core.ConfigRegistry {
	return s.configs
}

func (moduleRuntimeStub) Subscribe(
	context.Context,
	core.InterestSet,
	core.SubscriptionSpec,
	core.EventHandler,
) (core.Subscription, error) {
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
		return nil, core.ErrServiceNotFound
	}

	return value, nil
}
