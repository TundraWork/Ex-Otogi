package sleep

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ex-otogi/pkg/otogi"
)

func TestHandleSleepSuccess(t *testing.T) {
	t.Parallel()

	module := mustNew(t)
	dispatcher := &captureDispatcher{messageID: "sent-1"}
	moderation := &captureModerationDispatcher{}
	module.dispatcher = dispatcher
	module.moderation = moderation

	event := newSleepEvent("30m")
	if err := module.handleSleep(context.Background(), event); err != nil {
		t.Fatalf("handleSleep error: %v", err)
	}

	if moderation.calls.Load() != 1 {
		t.Fatalf("restrict calls = %d, want 1", moderation.calls.Load())
	}
	if moderation.lastRequest.MemberID != "user-1" {
		t.Fatalf("member id = %q, want user-1", moderation.lastRequest.MemberID)
	}
	if moderation.lastRequest.Permissions.SendMessages {
		t.Fatal("expected SendMessages to be false for muted user")
	}
	if !moderation.lastRequest.Permissions.InviteUsers {
		t.Fatal("expected InviteUsers to be true for muted user")
	}

	if dispatcher.calls.Load() != 2 {
		t.Fatalf("send calls = %d, want 2 (sleep message + wake code)", dispatcher.calls.Load())
	}

	firstMsg := dispatcher.allRequests[0]
	if !strings.Contains(firstMsg.Text, "30分钟") {
		t.Fatalf("first message should contain duration, got %q", firstMsg.Text)
	}
	secondMsg := dispatcher.allRequests[1]
	if !strings.HasPrefix(secondMsg.Text, "/wake ") {
		t.Fatalf("second message should contain wake command, got %q", secondMsg.Text)
	}
}

func TestHandleSleepInvalidDuration(t *testing.T) {
	t.Parallel()

	module := mustNew(t)
	dispatcher := &captureDispatcher{messageID: "sent-1"}
	moderation := &captureModerationDispatcher{}
	module.dispatcher = dispatcher
	module.moderation = moderation

	event := newSleepEvent("99h")
	if err := module.handleSleep(context.Background(), event); err != nil {
		t.Fatalf("handleSleep error: %v", err)
	}

	if moderation.calls.Load() != 0 {
		t.Fatalf("restrict calls = %d, want 0", moderation.calls.Load())
	}
	if dispatcher.calls.Load() != 1 {
		t.Fatalf("send calls = %d, want 1 (error reply)", dispatcher.calls.Load())
	}
}

func TestHandlePermissionChangeFailureRepliesInCommandConversation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                     string
		buildEvent               func(t *testing.T, module *Module) *otogi.Event
		handler                  func(context.Context, *Module, *otogi.Event) error
		wantModerationTargetID   string
		wantReplyConversationID  string
		wantReplyMessageContains string
	}{
		{
			name: "sleep restrict failure",
			buildEvent: func(_ *testing.T, _ *Module) *otogi.Event {
				return newSleepEvent("30m")
			},
			handler: func(ctx context.Context, module *Module, event *otogi.Event) error {
				return module.handleSleep(ctx, event)
			},
			wantModerationTargetID:   "chat-42",
			wantReplyConversationID:  "chat-42",
			wantReplyMessageContains: "没能开始休息",
		},
		{
			name: "wake unrestrict failure",
			buildEvent: func(t *testing.T, module *Module) *otogi.Event {
				event := newWakeEvent("")
				code, err := module.codeManager.Generate(codeScopeFromEvent(event), time.Now().Add(time.Minute))
				if err != nil {
					t.Fatalf("Generate() error: %v", err)
				}
				event.Article.Text = "/wake " + code
				event.Command.Value = code
				event.Command.RawInput = "/wake " + code
				return event
			},
			handler: func(ctx context.Context, module *Module, event *otogi.Event) error {
				return module.handleWake(ctx, event)
			},
			wantModerationTargetID:   "chat-42",
			wantReplyConversationID:  "chat-42",
			wantReplyMessageContains: "没能恢复你的权限",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			module := mustNew(t)
			dispatcher := &captureDispatcher{messageID: "sent-1"}
			moderation := &captureModerationDispatcher{
				restrictErr: errors.New("permission denied"),
			}
			module.dispatcher = dispatcher
			module.moderation = moderation

			event := tc.buildEvent(t, module)
			err := tc.handler(context.Background(), module, event)
			if err == nil {
				t.Fatal("expected error from permission change failure")
			}
			if !strings.Contains(err.Error(), "permission denied") {
				t.Fatalf("error = %v, want wrapped permission denied", err)
			}
			if moderation.calls.Load() != 1 {
				t.Fatalf("restrict calls = %d, want 1", moderation.calls.Load())
			}
			if moderation.lastRequest.Target.Conversation.ID != tc.wantModerationTargetID {
				t.Fatalf(
					"moderation target conversation = %q, want %q",
					moderation.lastRequest.Target.Conversation.ID,
					tc.wantModerationTargetID,
				)
			}
			if dispatcher.calls.Load() != 1 {
				t.Fatalf("send calls = %d, want 1 (error reply)", dispatcher.calls.Load())
			}
			if dispatcher.lastRequest.Target.Conversation.ID != tc.wantReplyConversationID {
				t.Fatalf(
					"reply conversation = %q, want %q",
					dispatcher.lastRequest.Target.Conversation.ID,
					tc.wantReplyConversationID,
				)
			}
			if dispatcher.lastRequest.ReplyToMessageID != event.Article.ID {
				t.Fatalf(
					"reply to message = %q, want %q",
					dispatcher.lastRequest.ReplyToMessageID,
					event.Article.ID,
				)
			}
			if !strings.Contains(dispatcher.lastRequest.Text, tc.wantReplyMessageContains) {
				t.Fatalf(
					"reply text = %q, want substring %q",
					dispatcher.lastRequest.Text,
					tc.wantReplyMessageContains,
				)
			}
		})
	}
}

func TestHandleWakeSuccess(t *testing.T) {
	t.Parallel()

	module := mustNew(t)
	dispatcher := &captureDispatcher{messageID: "sent-1"}
	moderation := &captureModerationDispatcher{}
	module.dispatcher = dispatcher
	module.moderation = moderation

	event := newWakeEvent("")
	code, err := module.codeManager.Generate(codeScopeFromEvent(event), time.Now().Add(time.Minute))
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}
	event.Article.Text = "/wake " + code
	event.Command.Value = code
	event.Command.RawInput = "/wake " + code
	if err := module.handleWake(context.Background(), event); err != nil {
		t.Fatalf("handleWake error: %v", err)
	}

	if moderation.calls.Load() != 1 {
		t.Fatalf("restrict calls = %d, want 1", moderation.calls.Load())
	}
	if moderation.lastRequest.Target.Conversation.ID != event.Conversation.ID {
		t.Fatalf(
			"moderation target conversation = %q, want %q",
			moderation.lastRequest.Target.Conversation.ID,
			event.Conversation.ID,
		)
	}
	if !moderation.lastRequest.Permissions.SendMessages {
		t.Fatal("expected SendMessages to be true for unrestricted user")
	}
	if dispatcher.calls.Load() != 1 {
		t.Fatalf("send calls = %d, want 1 (wake announcement)", dispatcher.calls.Load())
	}
	if dispatcher.lastRequest.Target.Conversation.ID != event.Conversation.ID {
		t.Fatalf(
			"announcement conversation = %q, want %q",
			dispatcher.lastRequest.Target.Conversation.ID,
			event.Conversation.ID,
		)
	}
}

func TestHandleWakeInvalidCode(t *testing.T) {
	t.Parallel()

	module := mustNew(t)
	dispatcher := &captureDispatcher{messageID: "sent-1"}
	moderation := &captureModerationDispatcher{}
	module.dispatcher = dispatcher
	module.moderation = moderation

	event := newWakeEvent("invalid-code")
	if err := module.handleWake(context.Background(), event); err != nil {
		t.Fatalf("handleWake error: %v", err)
	}

	if moderation.calls.Load() != 0 {
		t.Fatalf("restrict calls = %d, want 0", moderation.calls.Load())
	}
	if dispatcher.calls.Load() != 1 {
		t.Fatalf("send calls = %d, want 1 (error reply)", dispatcher.calls.Load())
	}
}

func TestHandleWakeCodeBoundToConversation(t *testing.T) {
	t.Parallel()

	module := mustNew(t)
	dispatcher := &captureDispatcher{messageID: "sent-1"}
	moderation := &captureModerationDispatcher{}
	module.dispatcher = dispatcher
	module.moderation = moderation

	originalEvent := newWakeEvent("")
	code, err := module.codeManager.Generate(codeScopeFromEvent(originalEvent), time.Now().Add(time.Minute))
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}

	event := newWakeEvent(code)
	event.Conversation.ID = "chat-99"
	event.Article.Text = "/wake " + code
	event.Command.Value = code
	event.Command.RawInput = "/wake " + code

	if err := module.handleWake(context.Background(), event); err != nil {
		t.Fatalf("handleWake error: %v", err)
	}

	if moderation.calls.Load() != 0 {
		t.Fatalf("restrict calls = %d, want 0", moderation.calls.Load())
	}
	if dispatcher.calls.Load() != 1 {
		t.Fatalf("send calls = %d, want 1 (error reply)", dispatcher.calls.Load())
	}
	if dispatcher.lastRequest.Target.Conversation.ID != event.Conversation.ID {
		t.Fatalf(
			"reply conversation = %q, want %q",
			dispatcher.lastRequest.Target.Conversation.ID,
			event.Conversation.ID,
		)
	}
}

func TestHandleWakeEmptyCode(t *testing.T) {
	t.Parallel()

	module := mustNew(t)
	dispatcher := &captureDispatcher{messageID: "sent-1"}
	moderation := &captureModerationDispatcher{}
	module.dispatcher = dispatcher
	module.moderation = moderation

	event := newWakeEvent("")
	if err := module.handleWake(context.Background(), event); err != nil {
		t.Fatalf("handleWake error: %v", err)
	}

	if moderation.calls.Load() != 0 {
		t.Fatalf("restrict calls = %d, want 0", moderation.calls.Load())
	}
}

func TestHandleSleepNilEvent(t *testing.T) {
	t.Parallel()

	module := mustNew(t)
	if err := module.handleSleep(context.Background(), nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHandleWakeNilEvent(t *testing.T) {
	t.Parallel()

	module := mustNew(t)
	if err := module.handleWake(context.Background(), nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestModuleOnRegister(t *testing.T) {
	t.Parallel()

	module := mustNew(t)
	dispatcher := &captureDispatcher{messageID: "sent-1"}
	moderation := &captureModerationDispatcher{}
	runtime := moduleRuntimeStub{
		registry: serviceRegistryStub{
			values: map[string]any{
				otogi.ServiceSinkDispatcher:       dispatcher,
				otogi.ServiceModerationDispatcher: moderation,
			},
		},
	}

	if err := module.OnRegister(context.Background(), runtime); err != nil {
		t.Fatalf("OnRegister failed: %v", err)
	}
	if module.dispatcher == nil {
		t.Fatal("expected sink dispatcher to be configured")
	}
	if module.moderation == nil {
		t.Fatal("expected moderation dispatcher to be configured")
	}
}

func TestModuleSpec(t *testing.T) {
	t.Parallel()

	module := mustNew(t)
	spec := module.Spec()
	if len(spec.Handlers) != 2 {
		t.Fatalf("handler count = %d, want 2", len(spec.Handlers))
	}
	if len(spec.Commands) != 2 {
		t.Fatalf("command count = %d, want 2", len(spec.Commands))
	}

	commandNames := make(map[string]bool)
	for _, cmd := range spec.Commands {
		commandNames[cmd.Name] = true
	}
	if !commandNames[sleepCommandName] {
		t.Fatal("missing sleep command")
	}
	if !commandNames[wakeCommandName] {
		t.Fatal("missing wake command")
	}
}

func newSleepEvent(duration string) *otogi.Event {
	return &otogi.Event{
		ID:         "event-1",
		Kind:       otogi.EventKindCommandReceived,
		OccurredAt: time.Unix(1, 0).UTC(),
		Source: otogi.EventSource{
			Platform: otogi.PlatformTelegram,
			ID:       "tg-main",
		},
		Conversation: otogi.Conversation{
			ID:   "chat-42",
			Type: otogi.ConversationTypeGroup,
		},
		Actor: otogi.Actor{
			ID:       "user-1",
			Username: "testuser",
		},
		Article: &otogi.Article{
			ID:   "msg-1",
			Text: "/sleep " + duration,
		},
		Command: &otogi.CommandInvocation{
			Name:            sleepCommandName,
			Value:           duration,
			SourceEventID:   "source-1",
			SourceEventKind: otogi.EventKindArticleCreated,
			RawInput:        "/sleep " + duration,
		},
	}
}

func newWakeEvent(code string) *otogi.Event {
	return &otogi.Event{
		ID:         "event-2",
		Kind:       otogi.EventKindCommandReceived,
		OccurredAt: time.Unix(1, 0).UTC(),
		Source: otogi.EventSource{
			Platform: otogi.PlatformTelegram,
			ID:       "tg-main",
		},
		Conversation: otogi.Conversation{
			ID:   "chat-42",
			Type: otogi.ConversationTypeGroup,
		},
		Actor: otogi.Actor{
			ID:       "user-1",
			Username: "testuser",
		},
		Article: &otogi.Article{
			ID:   "msg-2",
			Text: "/wake " + code,
		},
		Command: &otogi.CommandInvocation{
			Name:            wakeCommandName,
			Value:           code,
			SourceEventID:   "source-2",
			SourceEventKind: otogi.EventKindArticleCreated,
			RawInput:        "/wake " + code,
		},
	}
}

type captureDispatcher struct {
	mu          sync.Mutex
	calls       atomic.Int64
	messageID   string
	sendErr     error
	lastRequest otogi.SendMessageRequest
	allRequests []otogi.SendMessageRequest
}

func (d *captureDispatcher) SendMessage(
	_ context.Context,
	request otogi.SendMessageRequest,
) (*otogi.OutboundMessage, error) {
	d.calls.Add(1)
	d.mu.Lock()
	d.lastRequest = request
	d.allRequests = append(d.allRequests, request)
	d.mu.Unlock()

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

type captureModerationDispatcher struct {
	calls       atomic.Int64
	lastRequest otogi.RestrictMemberRequest
	restrictErr error
}

func (d *captureModerationDispatcher) RestrictMember(
	_ context.Context,
	request otogi.RestrictMemberRequest,
) error {
	d.calls.Add(1)
	d.lastRequest = request

	return d.restrictErr
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

func mustNew(t *testing.T) *Module {
	t.Helper()

	m, err := New(Config{SigningKey: testSigningKey()})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	return m
}
