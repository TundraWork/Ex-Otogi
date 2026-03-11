package whoami

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ex-otogi/pkg/otogi"
)

func TestModuleHandleCommand(t *testing.T) {
	tests := []struct {
		name            string
		event           *otogi.Event
		sendErr         error
		wantErr         bool
		wantSent        bool
		wantContains    []string
		wantNotContains []string
	}{
		{
			name:     "whoami reports full identity",
			event:    newWhoamiEvent("user-42", "johndoe", "John Doe", "chat-100", otogi.ConversationTypeGroup, "My Group"),
			wantSent: true,
			wantContains: []string{
				"source_platform: telegram",
				"user_id: tg-main/user-42",
				"username: @johndoe",
				"display_name: John Doe",
				"chat_id: tg-main/chat-100",
				"chat_type: group",
				"chat_title: My Group",
			},
			wantNotContains: []string{
				"source_id:",
				"allowlist_id:",
			},
		},
		{
			name:     "whoami with minimal identity",
			event:    newWhoamiEvent("user-1", "", "", "chat-1", otogi.ConversationTypePrivate, ""),
			wantSent: true,
			wantContains: []string{
				"source_platform: telegram",
				"user_id: tg-main/user-1",
				"chat_id: tg-main/chat-1",
				"chat_type: private",
			},
			wantNotContains: []string{
				"source_id:",
				"allowlist_id:",
				"username:",
				"display_name:",
				"chat_title:",
			},
		},
		{
			name:     "whoami with empty user ID shows dash",
			event:    newWhoamiEvent("", "", "", "chat-1", otogi.ConversationTypePrivate, ""),
			wantSent: true,
			wantContains: []string{
				"user_id: -",
			},
			wantNotContains: []string{
				"source_id:",
				"allowlist_id:",
			},
		},
		{
			name:     "non-whoami command is ignored",
			event:    newSystemCommandEvent("other", "user-1", "chat-1"),
			wantSent: false,
		},
		{
			name:     "ordinary command kind is ignored",
			event:    newOrdinaryCommandEvent("whoami", "user-1", "chat-1"),
			wantSent: false,
		},
		{
			name:     "nil event is ignored",
			event:    nil,
			wantSent: false,
		},
		{
			name:     "missing command payload is ignored",
			event:    newMissingCommandPayloadEvent(),
			wantSent: false,
		},
		{
			name:     "send failure returns error",
			event:    newWhoamiEvent("user-1", "", "", "chat-1", otogi.ConversationTypePrivate, ""),
			sendErr:  errors.New("dispatcher failure"),
			wantErr:  true,
			wantSent: true,
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

			sent := dispatcher.calls.Load() > 0
			if sent != testCase.wantSent {
				t.Fatalf("sent = %v, want %v", sent, testCase.wantSent)
			}
			if !sent {
				return
			}

			text := dispatcher.lastRequest.Text
			for _, want := range testCase.wantContains {
				if !strings.Contains(text, want) {
					t.Errorf("response missing %q, got:\n%s", want, text)
				}
			}
			for _, notWant := range testCase.wantNotContains {
				if strings.Contains(text, notWant) {
					t.Errorf("response should not contain %q, got:\n%s", notWant, text)
				}
			}
			assertPreformattedText(t, dispatcher.lastRequest.Entities, text)

			if testCase.event != nil && testCase.event.Article != nil {
				if dispatcher.lastRequest.ReplyToMessageID != testCase.event.Article.ID {
					t.Fatalf(
						"reply_to = %q, want %q",
						dispatcher.lastRequest.ReplyToMessageID,
						testCase.event.Article.ID,
					)
				}
			}
		})
	}
}

func assertPreformattedText(t *testing.T, entities []otogi.TextEntity, text string) {
	t.Helper()

	if len(entities) != 1 {
		t.Fatalf("entity count = %d, want 1", len(entities))
	}

	entity := entities[0]
	if entity.Type != otogi.TextEntityTypePre {
		t.Fatalf("entity type = %q, want %q", entity.Type, otogi.TextEntityTypePre)
	}
	if entity.Offset != 0 {
		t.Fatalf("entity offset = %d, want 0", entity.Offset)
	}
	if entity.Length != len([]rune(text)) {
		t.Fatalf("entity length = %d, want %d", entity.Length, len([]rune(text)))
	}
}

func TestModuleOnRegister(t *testing.T) {
	t.Parallel()

	module := New()
	dispatcher := &captureDispatcher{messageID: "sent-1"}
	runtime := moduleRuntimeStub{
		registry: serviceRegistryStub{
			values: map[string]any{
				otogi.ServiceSinkDispatcher: dispatcher,
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

func TestModuleSpecUsesSystemCommandCapability(t *testing.T) {
	t.Parallel()

	module := New()
	spec := module.Spec()
	if len(spec.Handlers) != 1 {
		t.Fatalf("handler count = %d, want 1", len(spec.Handlers))
	}
	if len(spec.Commands) != 1 {
		t.Fatalf("command count = %d, want 1", len(spec.Commands))
	}
	if spec.Commands[0].Prefix != otogi.CommandPrefixSystem {
		t.Fatalf("command prefix = %q, want %q", spec.Commands[0].Prefix, otogi.CommandPrefixSystem)
	}
	if spec.Commands[0].Name != commandName {
		t.Fatalf("command name = %q, want %q", spec.Commands[0].Name, commandName)
	}

	handler := spec.Handlers[0]
	if !handler.Capability.Interest.RequireArticle {
		t.Fatal("expected RequireArticle to be true")
	}
	if !handler.Capability.Interest.RequireCommand {
		t.Fatal("expected RequireCommand to be true")
	}
	if len(handler.Capability.Interest.Kinds) != 1 || handler.Capability.Interest.Kinds[0] != otogi.EventKindSystemCommandReceived {
		t.Fatalf("kinds = %v, want [%s]", handler.Capability.Interest.Kinds, otogi.EventKindSystemCommandReceived)
	}
	if len(handler.Capability.Interest.CommandNames) != 1 || handler.Capability.Interest.CommandNames[0] != commandName {
		t.Fatalf("command names = %v, want [%s]", handler.Capability.Interest.CommandNames, commandName)
	}
}

func newWhoamiEvent(
	userID string,
	username string,
	displayName string,
	chatID string,
	chatType otogi.ConversationType,
	chatTitle string,
) *otogi.Event {
	return &otogi.Event{
		ID:         "event-1",
		Kind:       otogi.EventKindSystemCommandReceived,
		OccurredAt: time.Unix(1, 0).UTC(),
		Source: otogi.EventSource{
			Platform: otogi.PlatformTelegram,
			ID:       "tg-main",
		},
		Conversation: otogi.Conversation{
			ID:    chatID,
			Type:  chatType,
			Title: chatTitle,
		},
		Actor: otogi.Actor{
			ID:          userID,
			Username:    username,
			DisplayName: displayName,
		},
		Article: &otogi.Article{
			ID:   "msg-1",
			Text: "~whoami",
		},
		Command: &otogi.CommandInvocation{
			Name:            commandName,
			SourceEventID:   "source-event-1",
			SourceEventKind: otogi.EventKindArticleCreated,
			RawInput:        "~whoami",
		},
	}
}

func newSystemCommandEvent(name string, userID string, chatID string) *otogi.Event {
	return &otogi.Event{
		ID:         "event-1",
		Kind:       otogi.EventKindSystemCommandReceived,
		OccurredAt: time.Unix(1, 0).UTC(),
		Source: otogi.EventSource{
			Platform: otogi.PlatformTelegram,
			ID:       "tg-main",
		},
		Conversation: otogi.Conversation{
			ID:   chatID,
			Type: otogi.ConversationTypePrivate,
		},
		Actor: otogi.Actor{ID: userID},
		Article: &otogi.Article{
			ID:   "msg-1",
			Text: "~" + name,
		},
		Command: &otogi.CommandInvocation{
			Name:            name,
			SourceEventID:   "source-event-1",
			SourceEventKind: otogi.EventKindArticleCreated,
			RawInput:        "~" + name,
		},
	}
}

func newOrdinaryCommandEvent(name string, userID string, chatID string) *otogi.Event {
	return &otogi.Event{
		ID:         "event-1",
		Kind:       otogi.EventKindCommandReceived,
		OccurredAt: time.Unix(1, 0).UTC(),
		Source: otogi.EventSource{
			Platform: otogi.PlatformTelegram,
			ID:       "tg-main",
		},
		Conversation: otogi.Conversation{
			ID:   chatID,
			Type: otogi.ConversationTypePrivate,
		},
		Actor: otogi.Actor{ID: userID},
		Article: &otogi.Article{
			ID:   "msg-1",
			Text: "/" + name,
		},
		Command: &otogi.CommandInvocation{
			Name:            name,
			SourceEventID:   "source-event-1",
			SourceEventKind: otogi.EventKindArticleCreated,
			RawInput:        "/" + name,
		},
	}
}

func newMissingCommandPayloadEvent() *otogi.Event {
	return &otogi.Event{
		ID:         "event-1",
		Kind:       otogi.EventKindSystemCommandReceived,
		OccurredAt: time.Unix(1, 0).UTC(),
		Source: otogi.EventSource{
			Platform: otogi.PlatformTelegram,
			ID:       "tg-main",
		},
		Conversation: otogi.Conversation{
			ID:   "chat-1",
			Type: otogi.ConversationTypePrivate,
		},
		Article: &otogi.Article{
			ID:   "msg-1",
			Text: "~whoami",
		},
	}
}

type captureDispatcher struct {
	calls       atomic.Int64
	messageID   string
	sendErr     error
	lastRequest otogi.SendMessageRequest
}

func (d *captureDispatcher) SendMessage(
	_ context.Context,
	request otogi.SendMessageRequest,
) (*otogi.OutboundMessage, error) {
	d.calls.Add(1)
	d.lastRequest = request
	if d.sendErr != nil {
		return nil, d.sendErr
	}

	return &otogi.OutboundMessage{ID: d.messageID}, nil
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

func (d *captureDispatcher) ListSinks(context.Context) ([]otogi.EventSink, error) {
	return nil, nil
}

func (d *captureDispatcher) ListSinksByPlatform(
	context.Context,
	otogi.Platform,
) ([]otogi.EventSink, error) {
	return nil, nil
}

type moduleRuntimeStub struct {
	registry otogi.ServiceRegistry
	configs  otogi.ConfigRegistry
}

func (s moduleRuntimeStub) Services() otogi.ServiceRegistry {
	return s.registry
}

func (s moduleRuntimeStub) Config() otogi.ConfigRegistry {
	return s.configs
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
