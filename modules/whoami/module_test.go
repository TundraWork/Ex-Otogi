package whoami

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
		name            string
		event           *platform.Event
		sendErr         error
		wantErr         bool
		wantSent        bool
		wantContains    []string
		wantNotContains []string
	}{
		{
			name:     "whoami reports full identity",
			event:    newWhoamiEvent("user-42", "johndoe", "John Doe", "chat-100", platform.ConversationTypeGroup, "My Group"),
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
			event:    newWhoamiEvent("user-1", "", "", "chat-1", platform.ConversationTypePrivate, ""),
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
			event:    newWhoamiEvent("", "", "", "chat-1", platform.ConversationTypePrivate, ""),
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
			event:    newWhoamiEvent("user-1", "", "", "chat-1", platform.ConversationTypePrivate, ""),
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

func assertPreformattedText(t *testing.T, entities []platform.TextEntity, text string) {
	t.Helper()

	if len(entities) != 1 {
		t.Fatalf("entity count = %d, want 1", len(entities))
	}

	entity := entities[0]
	if entity.Type != platform.TextEntityTypePre {
		t.Fatalf("entity type = %q, want %q", entity.Type, platform.TextEntityTypePre)
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
	if spec.Commands[0].Prefix != platform.CommandPrefixSystem {
		t.Fatalf("command prefix = %q, want %q", spec.Commands[0].Prefix, platform.CommandPrefixSystem)
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
	if len(handler.Capability.Interest.Kinds) != 1 || handler.Capability.Interest.Kinds[0] != platform.EventKindSystemCommandReceived {
		t.Fatalf("kinds = %v, want [%s]", handler.Capability.Interest.Kinds, platform.EventKindSystemCommandReceived)
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
	chatType platform.ConversationType,
	chatTitle string,
) *platform.Event {
	return &platform.Event{
		ID:         "event-1",
		Kind:       platform.EventKindSystemCommandReceived,
		OccurredAt: time.Unix(1, 0).UTC(),
		Source: platform.EventSource{
			Platform: platform.PlatformTelegram,
			ID:       "tg-main",
		},
		Conversation: platform.Conversation{
			ID:    chatID,
			Type:  chatType,
			Title: chatTitle,
		},
		Actor: platform.Actor{
			ID:          userID,
			Username:    username,
			DisplayName: displayName,
		},
		Article: &platform.Article{
			ID:   "msg-1",
			Text: "~whoami",
		},
		Command: &platform.CommandInvocation{
			Name:            commandName,
			SourceEventID:   "source-event-1",
			SourceEventKind: platform.EventKindArticleCreated,
			RawInput:        "~whoami",
		},
	}
}

func newSystemCommandEvent(name string, userID string, chatID string) *platform.Event {
	return &platform.Event{
		ID:         "event-1",
		Kind:       platform.EventKindSystemCommandReceived,
		OccurredAt: time.Unix(1, 0).UTC(),
		Source: platform.EventSource{
			Platform: platform.PlatformTelegram,
			ID:       "tg-main",
		},
		Conversation: platform.Conversation{
			ID:   chatID,
			Type: platform.ConversationTypePrivate,
		},
		Actor: platform.Actor{ID: userID},
		Article: &platform.Article{
			ID:   "msg-1",
			Text: "~" + name,
		},
		Command: &platform.CommandInvocation{
			Name:            name,
			SourceEventID:   "source-event-1",
			SourceEventKind: platform.EventKindArticleCreated,
			RawInput:        "~" + name,
		},
	}
}

func newOrdinaryCommandEvent(name string, userID string, chatID string) *platform.Event {
	return &platform.Event{
		ID:         "event-1",
		Kind:       platform.EventKindCommandReceived,
		OccurredAt: time.Unix(1, 0).UTC(),
		Source: platform.EventSource{
			Platform: platform.PlatformTelegram,
			ID:       "tg-main",
		},
		Conversation: platform.Conversation{
			ID:   chatID,
			Type: platform.ConversationTypePrivate,
		},
		Actor: platform.Actor{ID: userID},
		Article: &platform.Article{
			ID:   "msg-1",
			Text: "/" + name,
		},
		Command: &platform.CommandInvocation{
			Name:            name,
			SourceEventID:   "source-event-1",
			SourceEventKind: platform.EventKindArticleCreated,
			RawInput:        "/" + name,
		},
	}
}

func newMissingCommandPayloadEvent() *platform.Event {
	return &platform.Event{
		ID:         "event-1",
		Kind:       platform.EventKindSystemCommandReceived,
		OccurredAt: time.Unix(1, 0).UTC(),
		Source: platform.EventSource{
			Platform: platform.PlatformTelegram,
			ID:       "tg-main",
		},
		Conversation: platform.Conversation{
			ID:   "chat-1",
			Type: platform.ConversationTypePrivate,
		},
		Article: &platform.Article{
			ID:   "msg-1",
			Text: "~whoami",
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

	return &platform.OutboundMessage{ID: d.messageID}, nil
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
