package eventcache

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"ex-otogi/pkg/otogi"
)

func TestModuleOnRegister(t *testing.T) {
	tests := []struct {
		name             string
		services         map[string]any
		wantErr          bool
		wantErrSubstring string
	}{
		{
			name: "registers cache service with optional logger",
			services: map[string]any{
				ServiceLogger:                   slog.Default(),
				otogi.ServiceOutboundDispatcher: &captureDispatcher{},
			},
		},
		{
			name: "invalid logger type fails",
			services: map[string]any{
				ServiceLogger:                   struct{}{},
				otogi.ServiceOutboundDispatcher: &captureDispatcher{},
			},
			wantErr:          true,
			wantErrSubstring: "event cache resolve logger",
		},
		{
			name: "missing outbound dispatcher fails",
			services: map[string]any{
				ServiceLogger: slog.Default(),
			},
			wantErr:          true,
			wantErrSubstring: "event cache resolve outbound dispatcher",
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			registry := newServiceRegistryStub()
			for name, service := range testCase.services {
				if service == nil {
					continue
				}
				if err := registry.Register(name, service); err != nil {
					t.Fatalf("register service %s failed: %v", name, err)
				}
			}

			module := New()
			err := module.OnRegister(context.Background(), moduleRuntimeStub{registry: registry})
			if testCase.wantErr && err == nil {
				t.Fatal("expected error")
			}
			if !testCase.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if testCase.wantErr {
				if testCase.wantErrSubstring != "" && !strings.Contains(err.Error(), testCase.wantErrSubstring) {
					t.Fatalf("error = %v, want substring %q", err, testCase.wantErrSubstring)
				}
				return
			}

			resolved, err := registry.Resolve(otogi.ServiceEventCache)
			if err != nil {
				t.Fatalf("resolve event cache service failed: %v", err)
			}
			if resolved != module {
				t.Fatal("resolved event cache service is not module instance")
			}
		})
	}
}

func TestModuleIntrospectionCommands(t *testing.T) {
	tests := []struct {
		name                string
		seedEvents          []*otogi.Event
		commandEvent        *otogi.Event
		sendErr             error
		wantErr             bool
		wantSent            bool
		wantText            string
		wantTextContains    []string
		wantTextNotContains []string
	}{
		{
			name: "reply raw returns message entity json representation",
			seedEvents: []*otogi.Event{
				newCreatedEvent("msg-1", "hello", ""),
			},
			commandEvent: newCreatedEvent("msg-2", "~raw", "msg-1"),
			wantSent:     true,
			wantTextContains: []string{
				"\"Message\": {",
				"\"ID\": \"msg-1\"",
				"\"Text\": \"hello\"",
			},
			wantTextNotContains: []string{
				"\"Kind\":",
			},
		},
		{
			name: "reply raw with bot mention returns representation",
			seedEvents: []*otogi.Event{
				newCreatedEvent("msg-1", "hello", ""),
			},
			commandEvent: newCreatedEvent("msg-2", "~raw@mybot", "msg-1"),
			wantSent:     true,
			wantTextContains: []string{
				"\"Message\": {",
				"\"ID\": \"msg-1\"",
			},
		},
		{
			name: "reply raw after edit returns updated entity projection",
			seedEvents: []*otogi.Event{
				newCreatedEvent("msg-1", "hello", ""),
				newEditedEvent("msg-1", "hello edited"),
			},
			commandEvent: newCreatedEvent("msg-2", "~raw", "msg-1"),
			wantSent:     true,
			wantTextContains: []string{
				"\"ID\": \"msg-1\"",
				"\"Text\": \"hello edited\"",
			},
			wantTextNotContains: []string{
				"\"Mutation\":",
			},
		},
		{
			name: "reply raw after message mutation event returns updated projection",
			seedEvents: []*otogi.Event{
				newCreatedEvent("msg-1", "hello", ""),
				newMessageMutationEvent("msg-1", "hello edited"),
			},
			commandEvent: newCreatedEvent("msg-2", "~raw", "msg-1"),
			wantSent:     true,
			wantTextContains: []string{
				"\"Text\": \"hello edited\"",
			},
			wantTextNotContains: []string{
				"\"Mutation\":",
			},
		},
		{
			name: "reply raw after reaction still returns message entity",
			seedEvents: []*otogi.Event{
				newCreatedEvent("msg-1", "hello", ""),
				newReactionEvent("msg-1", "👍", otogi.ReactionActionAdd),
			},
			commandEvent: newCreatedEvent("msg-2", "~raw", "msg-1"),
			wantSent:     true,
			wantTextContains: []string{
				"\"ID\": \"msg-1\"",
				"\"Text\": \"hello\"",
				"\"Reactions\": [",
				"\"Emoji\": \"👍\"",
				"\"Count\": 1",
			},
			wantTextNotContains: []string{
				"\"Kind\":",
			},
		},
		{
			name:         "raw without reply is ignored",
			commandEvent: newCreatedEvent("msg-2", "~raw", ""),
			wantSent:     false,
		},
		{
			name:         "non raw command is ignored",
			commandEvent: newCreatedEvent("msg-2", "/ping", "msg-1"),
			wantSent:     false,
		},
		{
			name:         "raw with cache miss returns miss message",
			commandEvent: newCreatedEvent("msg-2", "~raw", "msg-404"),
			wantSent:     true,
			wantText:     "raw: replied message not found in event cache",
		},
		{
			name: "legacy slash raw command is ignored",
			seedEvents: []*otogi.Event{
				newCreatedEvent("msg-1", "hello", ""),
			},
			commandEvent: newCreatedEvent("msg-2", "/raw", "msg-1"),
			wantSent:     false,
		},
		{
			name: "raw can target explicit integer message id",
			seedEvents: []*otogi.Event{
				newCreatedEvent("114514", "hello explicit", ""),
			},
			commandEvent: newCreatedEvent("msg-2", "~raw 114514", ""),
			wantSent:     true,
			wantTextContains: []string{
				"\"ID\": \"114514\"",
				"\"Text\": \"hello explicit\"",
			},
		},
		{
			name:         "raw explicit message id cache miss returns miss message",
			commandEvent: newCreatedEvent("msg-2", "~raw 114514", ""),
			wantSent:     true,
			wantText:     "raw: message not found in event cache",
		},
		{
			name:         "raw invalid message id returns parse message",
			commandEvent: newCreatedEvent("msg-2", "~raw invalid", ""),
			wantSent:     true,
			wantText:     "raw: invalid message id \"invalid\", expected a positive integer",
		},
		{
			name:         "raw with too many arguments returns parse message",
			commandEvent: newCreatedEvent("msg-2", "~raw 1 2", ""),
			wantSent:     true,
			wantText:     "raw: expected at most one integer message id argument",
		},
		{
			name: "raw send failure returns error",
			seedEvents: []*otogi.Event{
				newCreatedEvent("msg-1", "hello", ""),
			},
			commandEvent: newCreatedEvent("msg-2", "~raw", "msg-1"),
			sendErr:      errors.New("send failed"),
			wantErr:      true,
			wantSent:     true,
		},
		{
			name: "raw output is truncated when too long",
			seedEvents: []*otogi.Event{
				newCreatedEvent("msg-1", strings.Repeat("x", 5000), ""),
			},
			commandEvent: newCreatedEvent("msg-2", "~raw", "msg-1"),
			wantSent:     true,
			wantTextContains: []string{
				"...(truncated)",
			},
		},
		{
			name: "reply history returns full event stream representation",
			seedEvents: []*otogi.Event{
				newCreatedEvent("msg-1", "hello", ""),
				newReactionEvent("msg-1", "👍", otogi.ReactionActionAdd),
			},
			commandEvent: newCreatedEvent("msg-2", "~history", "msg-1"),
			wantSent:     true,
			wantTextContains: []string{
				"\"Kind\": \"message.created\"",
				"\"Kind\": \"reaction.added\"",
			},
		},
		{
			name: "history can target explicit integer message id",
			seedEvents: []*otogi.Event{
				newCreatedEvent("114514", "hello", ""),
				newEditedEvent("114514", "hello edited"),
			},
			commandEvent: newCreatedEvent("msg-2", "~history 114514", ""),
			wantSent:     true,
			wantTextContains: []string{
				"\"Kind\": \"message.created\"",
				"\"Kind\": \"message.edited\"",
				"\"Before\": {",
			},
			wantTextNotContains: []string{
				"\"Before\": null",
			},
		},
		{
			name:         "history without reply is ignored",
			commandEvent: newCreatedEvent("msg-2", "~history", ""),
			wantSent:     false,
		},
		{
			name:         "history with cache miss returns miss message",
			commandEvent: newCreatedEvent("msg-2", "~history", "msg-404"),
			wantSent:     true,
			wantText:     "history: replied message not found in event cache",
		},
		{
			name:         "history explicit message id cache miss returns miss message",
			commandEvent: newCreatedEvent("msg-2", "~history 114514", ""),
			wantSent:     true,
			wantText:     "history: message not found in event cache",
		},
		{
			name:         "history invalid message id returns parse message",
			commandEvent: newCreatedEvent("msg-2", "~history invalid", ""),
			wantSent:     true,
			wantText:     "history: invalid message id \"invalid\", expected a positive integer",
		},
		{
			name: "history send failure returns error",
			seedEvents: []*otogi.Event{
				newCreatedEvent("msg-1", "hello", ""),
			},
			commandEvent: newCreatedEvent("msg-2", "~history", "msg-1"),
			sendErr:      errors.New("send failed"),
			wantErr:      true,
			wantSent:     true,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			dispatcher := &captureDispatcher{sendErr: testCase.sendErr}
			module := New(
				WithTTL(24*time.Hour),
				withClock(func() time.Time { return time.Unix(200, 0).UTC() }),
			)
			module.dispatcher = dispatcher

			for _, seedEvent := range testCase.seedEvents {
				if err := module.handleEvent(context.Background(), seedEvent); err != nil {
					t.Fatalf("seed event %s failed: %v", seedEvent.ID, err)
				}
			}

			err := module.handleEvent(context.Background(), testCase.commandEvent)
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

			if dispatcher.lastRequest.ReplyToMessageID != testCase.commandEvent.Message.ID {
				t.Fatalf(
					"reply_to = %q, want %q",
					dispatcher.lastRequest.ReplyToMessageID,
					testCase.commandEvent.Message.ID,
				)
			}
			if len(dispatcher.lastRequest.Entities) != 1 {
				t.Fatalf("entities len = %d, want 1", len(dispatcher.lastRequest.Entities))
			}
			entity := dispatcher.lastRequest.Entities[0]
			if entity.Type != otogi.TextEntityTypePre {
				t.Fatalf("entity type = %q, want %q", entity.Type, otogi.TextEntityTypePre)
			}
			if entity.Offset != 0 {
				t.Fatalf("entity offset = %d, want 0", entity.Offset)
			}
			if entity.Length != utf8.RuneCountInString(dispatcher.lastRequest.Text) {
				t.Fatalf(
					"entity length = %d, want %d",
					entity.Length,
					utf8.RuneCountInString(dispatcher.lastRequest.Text),
				)
			}
			if entity.Language != "json" {
				t.Fatalf("entity language = %q, want json", entity.Language)
			}
			if testCase.wantText != "" && dispatcher.lastRequest.Text != testCase.wantText {
				t.Fatalf("text = %q, want %q", dispatcher.lastRequest.Text, testCase.wantText)
			}
			for _, wantSubstring := range testCase.wantTextContains {
				if !strings.Contains(dispatcher.lastRequest.Text, wantSubstring) {
					t.Fatalf("text = %q, missing substring %q", dispatcher.lastRequest.Text, wantSubstring)
				}
			}
			for _, wantSubstring := range testCase.wantTextNotContains {
				if strings.Contains(dispatcher.lastRequest.Text, wantSubstring) {
					t.Fatalf("text = %q, contains forbidden substring %q", dispatcher.lastRequest.Text, wantSubstring)
				}
			}
		})
	}
}

func TestModuleEventLifecycle(t *testing.T) {
	tests := []struct {
		name      string
		events    []*otogi.Event
		lookup    otogi.MessageLookup
		wantFound bool
		wantText  string
	}{
		{
			name: "created message can be read",
			events: []*otogi.Event{
				newCreatedEvent("msg-1", "hello", ""),
			},
			lookup: otogi.MessageLookup{
				Platform:       otogi.PlatformTelegram,
				ConversationID: "chat-1",
				MessageID:      "msg-1",
			},
			wantFound: true,
			wantText:  "hello",
		},
		{
			name: "edit updates message text",
			events: []*otogi.Event{
				newCreatedEvent("msg-1", "hello", ""),
				newEditedEvent("msg-1", "hello edited"),
			},
			lookup: otogi.MessageLookup{
				Platform:       otogi.PlatformTelegram,
				ConversationID: "chat-1",
				MessageID:      "msg-1",
			},
			wantFound: true,
			wantText:  "hello edited",
		},
		{
			name: "retracted message is removed",
			events: []*otogi.Event{
				newCreatedEvent("msg-1", "hello", ""),
				newRetractedEvent("msg-1"),
			},
			lookup: otogi.MessageLookup{
				Platform:       otogi.PlatformTelegram,
				ConversationID: "chat-1",
				MessageID:      "msg-1",
			},
			wantFound: false,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			module := New(
				WithMaxEntries(100),
				WithTTL(24*time.Hour),
				withClock(func() time.Time { return time.Unix(100, 0).UTC() }),
			)

			for _, event := range testCase.events {
				if err := module.handleEvent(context.Background(), event); err != nil {
					t.Fatalf("handle event %s failed: %v", event.Kind, err)
				}
			}

			cached, found, err := module.GetMessage(context.Background(), testCase.lookup)
			if err != nil {
				t.Fatalf("get message failed: %v", err)
			}
			if found != testCase.wantFound {
				t.Fatalf("found = %v, want %v", found, testCase.wantFound)
			}
			if !testCase.wantFound {
				return
			}
			if cached.Message.Text != testCase.wantText {
				t.Fatalf("cached text = %q, want %q", cached.Message.Text, testCase.wantText)
			}
		})
	}
}

func TestModuleGetRepliedMessage(t *testing.T) {
	t.Parallel()

	module := New(
		WithTTL(24*time.Hour),
		withClock(func() time.Time { return time.Unix(200, 0).UTC() }),
	)

	if err := module.handleEvent(context.Background(), newCreatedEvent("msg-1", "origin", "")); err != nil {
		t.Fatalf("seed created event failed: %v", err)
	}

	replyEvent := newCreatedEvent("msg-2", "ping", "msg-1")
	cached, found, err := module.GetRepliedMessage(context.Background(), replyEvent)
	if err != nil {
		t.Fatalf("get replied message failed: %v", err)
	}
	if !found {
		t.Fatal("expected reply cache hit")
	}
	if cached.Message.Text != "origin" {
		t.Fatalf("reply text = %q, want origin", cached.Message.Text)
	}

	nonReplyEvent := newCreatedEvent("msg-3", "ping", "")
	_, found, err = module.GetRepliedMessage(context.Background(), nonReplyEvent)
	if err != nil {
		t.Fatalf("get replied message for non-reply failed: %v", err)
	}
	if found {
		t.Fatal("expected no reply cache hit")
	}
}

func TestModuleEventHistoryAndEntityProjectionSeparation(t *testing.T) {
	t.Parallel()

	module := New(
		WithTTL(24*time.Hour),
		withClock(func() time.Time { return time.Unix(220, 0).UTC() }),
	)

	events := []*otogi.Event{
		newCreatedEvent("msg-1", "hello", ""),
		newReactionEvent("msg-1", "👍", otogi.ReactionActionAdd),
		newEditedEvent("msg-1", "hello edited"),
	}
	for _, event := range events {
		if err := module.handleEvent(context.Background(), event); err != nil {
			t.Fatalf("handle event %s failed: %v", event.ID, err)
		}
	}

	lookup := otogi.MessageLookup{
		Platform:       otogi.PlatformTelegram,
		ConversationID: "chat-1",
		MessageID:      "msg-1",
	}

	cached, found, err := module.GetMessage(context.Background(), lookup)
	if err != nil {
		t.Fatalf("get message failed: %v", err)
	}
	if !found {
		t.Fatal("expected message projection cache hit")
	}
	if cached.Message.Text != "hello edited" {
		t.Fatalf("cached text = %q, want hello edited", cached.Message.Text)
	}

	history, found, err := module.GetEvents(context.Background(), lookup)
	if err != nil {
		t.Fatalf("get events failed: %v", err)
	}
	if !found {
		t.Fatal("expected event history cache hit")
	}
	if len(history) != 3 {
		t.Fatalf("history length = %d, want 3", len(history))
	}

	wantKinds := []otogi.EventKind{
		otogi.EventKindMessageCreated,
		otogi.EventKindReactionAdded,
		otogi.EventKindMessageEdited,
	}
	for idx, wantKind := range wantKinds {
		if history[idx].Kind != wantKind {
			t.Fatalf("history[%d].Kind = %s, want %s", idx, history[idx].Kind, wantKind)
		}
	}
}

func TestModuleHistoryBackfillsMissingEditBeforeSnapshot(t *testing.T) {
	t.Parallel()

	module := New(
		WithTTL(24*time.Hour),
		withClock(func() time.Time { return time.Unix(225, 0).UTC() }),
	)

	if err := module.handleEvent(context.Background(), newCreatedEvent("msg-1", "hello", "")); err != nil {
		t.Fatalf("seed created event failed: %v", err)
	}
	if err := module.handleEvent(context.Background(), newEditedEvent("msg-1", "hello edited")); err != nil {
		t.Fatalf("seed edited event failed: %v", err)
	}

	history, found, err := module.GetEvents(context.Background(), otogi.MessageLookup{
		Platform:       otogi.PlatformTelegram,
		ConversationID: "chat-1",
		MessageID:      "msg-1",
	})
	if err != nil {
		t.Fatalf("get events failed: %v", err)
	}
	if !found {
		t.Fatal("expected history cache hit")
	}
	if len(history) != 2 {
		t.Fatalf("history length = %d, want 2", len(history))
	}
	if history[1].Mutation == nil {
		t.Fatal("expected mutation payload in edited history event")
	}
	if history[1].Mutation.Before == nil {
		t.Fatal("expected mutation before snapshot to be backfilled")
	}
	if history[1].Mutation.Before.Text != "hello" {
		t.Fatalf("before text = %q, want hello", history[1].Mutation.Before.Text)
	}
	if history[1].Mutation.After == nil {
		t.Fatal("expected mutation after snapshot")
	}
	if history[1].Mutation.After.Text != "hello edited" {
		t.Fatalf("after text = %q, want hello edited", history[1].Mutation.After.Text)
	}
}

func TestModuleMessageMutationUsesChangedAtForUpdatedAt(t *testing.T) {
	t.Parallel()

	module := New(
		WithTTL(24*time.Hour),
		withClock(func() time.Time { return time.Unix(260, 0).UTC() }),
	)

	createdAt := time.Unix(100, 0).UTC()
	editedAt := time.Unix(180, 0).UTC()
	if err := module.handleEvent(context.Background(), newCreatedEventAt("msg-1", "origin", "", createdAt)); err != nil {
		t.Fatalf("seed created event failed: %v", err)
	}
	if err := module.handleEvent(
		context.Background(),
		newMessageMutationEventWithTimestamps("msg-1", "edited", createdAt, editedAt),
	); err != nil {
		t.Fatalf("seed message mutation event failed: %v", err)
	}

	cached, found, err := module.GetMessage(context.Background(), otogi.MessageLookup{
		Platform:       otogi.PlatformTelegram,
		ConversationID: "chat-1",
		MessageID:      "msg-1",
	})
	if err != nil {
		t.Fatalf("get message failed: %v", err)
	}
	if !found {
		t.Fatal("expected cache hit")
	}
	if !cached.CreatedAt.Equal(createdAt) {
		t.Fatalf("created_at = %v, want %v", cached.CreatedAt, createdAt)
	}
	if !cached.UpdatedAt.Equal(editedAt) {
		t.Fatalf("updated_at = %v, want %v", cached.UpdatedAt, editedAt)
	}
	if cached.Message.Text != "edited" {
		t.Fatalf("message text = %q, want edited", cached.Message.Text)
	}
}

func TestModuleReactionProjectionDerivedFromEvents(t *testing.T) {
	t.Parallel()

	module := New(
		WithTTL(24*time.Hour),
		withClock(func() time.Time { return time.Unix(280, 0).UTC() }),
	)

	createdAt := time.Unix(100, 0).UTC()
	reactionAddedAt := time.Unix(140, 0).UTC()
	reactionRemovedAt := time.Unix(160, 0).UTC()
	if err := module.handleEvent(context.Background(), newCreatedEventAt("msg-1", "origin", "", createdAt)); err != nil {
		t.Fatalf("seed created event failed: %v", err)
	}
	if err := module.handleEvent(
		context.Background(),
		newReactionEventAt("msg-1", "❤️", otogi.ReactionActionAdd, reactionAddedAt),
	); err != nil {
		t.Fatalf("seed reaction add event failed: %v", err)
	}
	if err := module.handleEvent(
		context.Background(),
		newReactionEventAt("msg-1", "❤️", otogi.ReactionActionRemove, reactionRemovedAt),
	); err != nil {
		t.Fatalf("seed reaction remove event failed: %v", err)
	}

	cached, found, err := module.GetMessage(context.Background(), otogi.MessageLookup{
		Platform:       otogi.PlatformTelegram,
		ConversationID: "chat-1",
		MessageID:      "msg-1",
	})
	if err != nil {
		t.Fatalf("get message failed: %v", err)
	}
	if !found {
		t.Fatal("expected cache hit")
	}
	if len(cached.Message.Reactions) != 0 {
		t.Fatalf("reactions = %+v, want empty", cached.Message.Reactions)
	}
	if !cached.UpdatedAt.Equal(createdAt) {
		t.Fatalf("updated_at = %v, want %v", cached.UpdatedAt, createdAt)
	}
}

func TestModuleRetractedEntityStillPreservesEventHistory(t *testing.T) {
	t.Parallel()

	module := New(
		WithTTL(24*time.Hour),
		withClock(func() time.Time { return time.Unix(230, 0).UTC() }),
	)

	if err := module.handleEvent(context.Background(), newCreatedEvent("msg-1", "hello", "")); err != nil {
		t.Fatalf("seed created event failed: %v", err)
	}
	if err := module.handleEvent(context.Background(), newRetractedEvent("msg-1")); err != nil {
		t.Fatalf("seed retracted event failed: %v", err)
	}

	lookup := otogi.MessageLookup{
		Platform:       otogi.PlatformTelegram,
		ConversationID: "chat-1",
		MessageID:      "msg-1",
	}

	_, found, err := module.GetMessage(context.Background(), lookup)
	if err != nil {
		t.Fatalf("get message failed: %v", err)
	}
	if found {
		t.Fatal("expected retracted message projection to be removed")
	}

	history, found, err := module.GetEvents(context.Background(), lookup)
	if err != nil {
		t.Fatalf("get events failed: %v", err)
	}
	if !found {
		t.Fatal("expected event history to remain after retraction")
	}
	if len(history) != 2 {
		t.Fatalf("history length = %d, want 2", len(history))
	}
	if history[1].Kind != otogi.EventKindMessageRetracted {
		t.Fatalf("history[1].Kind = %s, want %s", history[1].Kind, otogi.EventKindMessageRetracted)
	}
}

func TestModuleGetRepliedEvents(t *testing.T) {
	t.Parallel()

	module := New(
		WithTTL(24*time.Hour),
		withClock(func() time.Time { return time.Unix(240, 0).UTC() }),
	)

	if err := module.handleEvent(context.Background(), newCreatedEvent("msg-1", "origin", "")); err != nil {
		t.Fatalf("seed created event failed: %v", err)
	}
	if err := module.handleEvent(context.Background(), newReactionEvent("msg-1", "👍", otogi.ReactionActionAdd)); err != nil {
		t.Fatalf("seed reaction event failed: %v", err)
	}

	replyEvent := newCreatedEvent("msg-2", "ping", "msg-1")
	history, found, err := module.GetRepliedEvents(context.Background(), replyEvent)
	if err != nil {
		t.Fatalf("get replied events failed: %v", err)
	}
	if !found {
		t.Fatal("expected replied event history cache hit")
	}
	if len(history) != 2 {
		t.Fatalf("history length = %d, want 2", len(history))
	}

	nonReplyEvent := newCreatedEvent("msg-3", "ping", "")
	_, found, err = module.GetRepliedEvents(context.Background(), nonReplyEvent)
	if err != nil {
		t.Fatalf("get replied events for non-reply failed: %v", err)
	}
	if found {
		t.Fatal("expected no replied event history for non-reply")
	}
}

func TestModuleCapacityEviction(t *testing.T) {
	t.Parallel()

	module := New(
		WithMaxEntries(1),
		WithTTL(24*time.Hour),
		withClock(func() time.Time { return time.Unix(300, 0).UTC() }),
	)

	if err := module.handleEvent(context.Background(), newCreatedEvent("msg-1", "first", "")); err != nil {
		t.Fatalf("handle first event failed: %v", err)
	}
	if err := module.handleEvent(context.Background(), newCreatedEvent("msg-2", "second", "")); err != nil {
		t.Fatalf("handle second event failed: %v", err)
	}

	_, found, err := module.GetMessage(context.Background(), otogi.MessageLookup{
		Platform:       otogi.PlatformTelegram,
		ConversationID: "chat-1",
		MessageID:      "msg-1",
	})
	if err != nil {
		t.Fatalf("lookup oldest message failed: %v", err)
	}
	if found {
		t.Fatal("expected oldest message to be evicted")
	}

	cached, found, err := module.GetMessage(context.Background(), otogi.MessageLookup{
		Platform:       otogi.PlatformTelegram,
		ConversationID: "chat-1",
		MessageID:      "msg-2",
	})
	if err != nil {
		t.Fatalf("lookup newest message failed: %v", err)
	}
	if !found {
		t.Fatal("expected newest message to remain")
	}
	if cached.Message.Text != "second" {
		t.Fatalf("cached text = %q, want second", cached.Message.Text)
	}
}

func TestModuleTTLExpiry(t *testing.T) {
	t.Parallel()

	now := time.Unix(400, 0).UTC()
	module := New(
		WithTTL(time.Minute),
		withClock(func() time.Time { return now }),
	)

	if err := module.handleEvent(context.Background(), newCreatedEvent("msg-1", "ttl", "")); err != nil {
		t.Fatalf("handle created event failed: %v", err)
	}

	now = now.Add(2 * time.Minute)
	_, found, err := module.GetMessage(context.Background(), otogi.MessageLookup{
		Platform:       otogi.PlatformTelegram,
		ConversationID: "chat-1",
		MessageID:      "msg-1",
	})
	if err != nil {
		t.Fatalf("lookup ttl message failed: %v", err)
	}
	if found {
		t.Fatal("expected expired message to miss")
	}
}

func TestModuleGetMessageReturnsDefensiveCopy(t *testing.T) {
	t.Parallel()

	module := New(
		WithTTL(24*time.Hour),
		withClock(func() time.Time { return time.Unix(500, 0).UTC() }),
	)

	if err := module.handleEvent(context.Background(), newCreatedEvent("msg-1", "copy", "")); err != nil {
		t.Fatalf("handle created event failed: %v", err)
	}

	lookup := otogi.MessageLookup{
		Platform:       otogi.PlatformTelegram,
		ConversationID: "chat-1",
		MessageID:      "msg-1",
	}

	cached, found, err := module.GetMessage(context.Background(), lookup)
	if err != nil {
		t.Fatalf("get message failed: %v", err)
	}
	if !found {
		t.Fatal("expected message cache hit")
	}
	cached.Message.Text = "mutated"

	cachedAgain, found, err := module.GetMessage(context.Background(), lookup)
	if err != nil {
		t.Fatalf("get message second time failed: %v", err)
	}
	if !found {
		t.Fatal("expected message cache hit")
	}
	if cachedAgain.Message.Text != "copy" {
		t.Fatalf("cached text = %q, want copy", cachedAgain.Message.Text)
	}
}

func TestModuleGetEventsReturnsDefensiveCopy(t *testing.T) {
	t.Parallel()

	module := New(
		WithTTL(24*time.Hour),
		withClock(func() time.Time { return time.Unix(510, 0).UTC() }),
	)

	if err := module.handleEvent(context.Background(), newCreatedEvent("msg-1", "copy", "")); err != nil {
		t.Fatalf("handle created event failed: %v", err)
	}

	lookup := otogi.MessageLookup{
		Platform:       otogi.PlatformTelegram,
		ConversationID: "chat-1",
		MessageID:      "msg-1",
	}

	history, found, err := module.GetEvents(context.Background(), lookup)
	if err != nil {
		t.Fatalf("get events failed: %v", err)
	}
	if !found {
		t.Fatal("expected event history cache hit")
	}
	history[0].ID = "mutated"

	historyAgain, found, err := module.GetEvents(context.Background(), lookup)
	if err != nil {
		t.Fatalf("get events second time failed: %v", err)
	}
	if !found {
		t.Fatal("expected event history cache hit")
	}
	if historyAgain[0].ID != "evt-created-msg-1" {
		t.Fatalf("event id = %q, want evt-created-msg-1", historyAgain[0].ID)
	}
}

func TestModuleGetMessageContextCancellation(t *testing.T) {
	t.Parallel()

	module := New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := module.GetMessage(ctx, otogi.MessageLookup{
		Platform:       otogi.PlatformTelegram,
		ConversationID: "chat-1",
		MessageID:      "msg-1",
	})
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestModuleGetEventsContextCancellation(t *testing.T) {
	t.Parallel()

	module := New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := module.GetEvents(ctx, otogi.MessageLookup{
		Platform:       otogi.PlatformTelegram,
		ConversationID: "chat-1",
		MessageID:      "msg-1",
	})
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func newCreatedEvent(messageID string, text string, replyToID string) *otogi.Event {
	return newCreatedEventAt(messageID, text, replyToID, time.Unix(10, 0).UTC())
}

func newCreatedEventAt(messageID string, text string, replyToID string, occurredAt time.Time) *otogi.Event {
	return &otogi.Event{
		ID:         "evt-created-" + messageID,
		Kind:       otogi.EventKindMessageCreated,
		OccurredAt: occurredAt,
		Platform:   otogi.PlatformTelegram,
		Conversation: otogi.Conversation{
			ID:   "chat-1",
			Type: otogi.ConversationTypeGroup,
		},
		Actor: otogi.Actor{ID: "actor-1", DisplayName: "Alice"},
		Message: &otogi.Message{
			ID:        messageID,
			ReplyToID: replyToID,
			Text:      text,
		},
	}
}

func newEditedEvent(targetMessageID string, text string) *otogi.Event {
	return &otogi.Event{
		ID:         "evt-edit-" + targetMessageID,
		Kind:       otogi.EventKindMessageEdited,
		OccurredAt: time.Unix(20, 0).UTC(),
		Platform:   otogi.PlatformTelegram,
		Conversation: otogi.Conversation{
			ID:   "chat-1",
			Type: otogi.ConversationTypeGroup,
		},
		Mutation: &otogi.Mutation{
			Type:            otogi.MutationTypeEdit,
			TargetMessageID: targetMessageID,
			After: &otogi.MessageSnapshot{
				Text: text,
			},
		},
	}
}

func newMessageMutationEvent(targetMessageID string, text string) *otogi.Event {
	return newMessageMutationEventWithTimestamps(
		targetMessageID,
		text,
		time.Unix(20, 0).UTC(),
		time.Unix(20, 0).UTC(),
	)
}

func newMessageMutationEventWithTimestamps(
	targetMessageID string,
	text string,
	occurredAt time.Time,
	changedAt time.Time,
) *otogi.Event {
	return &otogi.Event{
		ID:         "evt-message-mutation-" + targetMessageID,
		Kind:       otogi.EventKindMessageCreated,
		OccurredAt: occurredAt,
		Platform:   otogi.PlatformTelegram,
		Conversation: otogi.Conversation{
			ID:   "chat-1",
			Type: otogi.ConversationTypeGroup,
		},
		Actor: otogi.Actor{ID: "actor-1", DisplayName: "Alice"},
		Message: &otogi.Message{
			ID:   targetMessageID,
			Text: text,
		},
		Mutation: &otogi.Mutation{
			Type:            otogi.MutationTypeEdit,
			TargetMessageID: targetMessageID,
			ChangedAt:       &changedAt,
			After: &otogi.MessageSnapshot{
				Text: text,
			},
			Reason: "telegram_edit_update",
		},
	}
}

func newRetractedEvent(targetMessageID string) *otogi.Event {
	return &otogi.Event{
		ID:         "evt-retract-" + targetMessageID,
		Kind:       otogi.EventKindMessageRetracted,
		OccurredAt: time.Unix(30, 0).UTC(),
		Platform:   otogi.PlatformTelegram,
		Conversation: otogi.Conversation{
			ID:   "chat-1",
			Type: otogi.ConversationTypeGroup,
		},
		Mutation: &otogi.Mutation{
			Type:            otogi.MutationTypeRetraction,
			TargetMessageID: targetMessageID,
		},
	}
}

func newReactionEvent(
	targetMessageID string,
	emoji string,
	action otogi.ReactionAction,
) *otogi.Event {
	return newReactionEventAt(targetMessageID, emoji, action, time.Unix(25, 0).UTC())
}

func newReactionEventAt(
	targetMessageID string,
	emoji string,
	action otogi.ReactionAction,
	occurredAt time.Time,
) *otogi.Event {
	kind := otogi.EventKindReactionAdded
	if action == otogi.ReactionActionRemove {
		kind = otogi.EventKindReactionRemoved
	}

	return &otogi.Event{
		ID:         "evt-reaction-" + targetMessageID,
		Kind:       kind,
		OccurredAt: occurredAt,
		Platform:   otogi.PlatformTelegram,
		Conversation: otogi.Conversation{
			ID:   "chat-1",
			Type: otogi.ConversationTypeGroup,
		},
		Actor: otogi.Actor{ID: "actor-2", DisplayName: "Bob"},
		Reaction: &otogi.Reaction{
			MessageID: targetMessageID,
			Emoji:     emoji,
			Action:    action,
		},
	}
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

func newServiceRegistryStub() *serviceRegistryStub {
	return &serviceRegistryStub{values: make(map[string]any)}
}

func (s *serviceRegistryStub) Register(name string, service any) error {
	if name == "" {
		return errors.New("empty service name")
	}
	if _, exists := s.values[name]; exists {
		return otogi.ErrServiceAlreadyRegistered
	}
	s.values[name] = service

	return nil
}

func (s *serviceRegistryStub) Resolve(name string) (any, error) {
	value, ok := s.values[name]
	if !ok {
		return nil, otogi.ErrServiceNotFound
	}

	return value, nil
}

type captureDispatcher struct {
	calls       atomic.Int64
	lastRequest otogi.SendMessageRequest
	sendErr     error
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

	return &otogi.OutboundMessage{ID: "sent-1"}, nil
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
