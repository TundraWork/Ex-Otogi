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

	"ex-otogi/pkg/otogi/core"
	"ex-otogi/pkg/otogi/platform"
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
				ServiceLogger:                  slog.Default(),
				platform.ServiceSinkDispatcher: &captureDispatcher{},
			},
		},
		{
			name: "invalid logger type fails",
			services: map[string]any{
				ServiceLogger:                  struct{}{},
				platform.ServiceSinkDispatcher: &captureDispatcher{},
			},
			wantErr:          true,
			wantErrSubstring: "eventcache resolve logger",
		},
		{
			name: "missing outbound dispatcher fails",
			services: map[string]any{
				ServiceLogger: slog.Default(),
			},
			wantErr:          true,
			wantErrSubstring: "eventcache resolve outbound dispatcher",
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

			resolved, err := registry.Resolve(core.ServiceMemory)
			if err != nil {
				t.Fatalf("resolve memory service failed: %v", err)
			}
			if resolved != module {
				t.Fatal("resolved memory service is not module instance")
			}
		})
	}
}

func TestModuleIntrospectionCommands(t *testing.T) {
	tests := []struct {
		name                string
		seedEvents          []*platform.Event
		commandEvent        *platform.Event
		sendErr             error
		wantErr             bool
		wantSent            bool
		wantText            string
		wantTextContains    []string
		wantTextNotContains []string
	}{
		{
			name: "reply raw returns article entity json representation",
			seedEvents: []*platform.Event{
				newCreatedEvent("msg-1", "hello", ""),
			},
			commandEvent: newCommandEvent("msg-2", "~raw", "msg-1"),
			wantSent:     true,
			wantTextContains: []string{
				"\"Article\": {",
				"\"ID\": \"msg-1\"",
				"\"Text\": \"hello\"",
			},
			wantTextNotContains: []string{
				"\"Kind\":",
			},
		},
		{
			name: "reply raw with bot mention returns representation",
			seedEvents: []*platform.Event{
				newCreatedEvent("msg-1", "hello", ""),
			},
			commandEvent: newCommandEvent("msg-2", "~raw@mybot", "msg-1"),
			wantSent:     true,
			wantTextContains: []string{
				"\"Article\": {",
				"\"ID\": \"msg-1\"",
			},
		},
		{
			name: "reply raw after edit returns updated entity projection",
			seedEvents: []*platform.Event{
				newCreatedEvent("msg-1", "hello", ""),
				newEditedEvent("msg-1", "hello edited"),
			},
			commandEvent: newCommandEvent("msg-2", "~raw", "msg-1"),
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
			name: "reply raw after article mutation event returns updated projection",
			seedEvents: []*platform.Event{
				newCreatedEvent("msg-1", "hello", ""),
				newArticleMutationEvent("msg-1", "hello edited"),
			},
			commandEvent: newCommandEvent("msg-2", "~raw", "msg-1"),
			wantSent:     true,
			wantTextContains: []string{
				"\"Text\": \"hello edited\"",
			},
			wantTextNotContains: []string{
				"\"Mutation\":",
			},
		},
		{
			name: "reply raw after reaction still returns article entity",
			seedEvents: []*platform.Event{
				newCreatedEvent("msg-1", "hello", ""),
				newReactionEvent("msg-1", "👍", platform.ReactionActionAdd),
			},
			commandEvent: newCommandEvent("msg-2", "~raw", "msg-1"),
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
			commandEvent: newCommandEvent("msg-2", "~raw", ""),
			wantSent:     false,
		},
		{
			name:         "non raw command is ignored",
			commandEvent: newCommandEvent("msg-2", "/ping", "msg-1"),
			wantSent:     false,
		},
		{
			name:         "raw with cache miss returns miss text",
			commandEvent: newCommandEvent("msg-2", "~raw", "msg-404"),
			wantSent:     true,
			wantText:     "raw: replied article not found in memory",
		},
		{
			name: "slash raw command is ignored",
			seedEvents: []*platform.Event{
				newCreatedEvent("msg-1", "hello", ""),
			},
			commandEvent: newCommandEvent("msg-2", "/raw", "msg-1"),
			wantSent:     false,
		},
		{
			name: "raw can target explicit integer article id",
			seedEvents: []*platform.Event{
				newCreatedEvent("114514", "hello explicit", ""),
			},
			commandEvent: newCommandEvent("msg-2", "~raw 114514", ""),
			wantSent:     true,
			wantTextContains: []string{
				"\"ID\": \"114514\"",
				"\"Text\": \"hello explicit\"",
			},
		},
		{
			name:         "raw explicit article id cache miss returns miss text",
			commandEvent: newCommandEvent("msg-2", "~raw 114514", ""),
			wantSent:     true,
			wantText:     "raw: article not found in memory",
		},
		{
			name:         "raw invalid article id returns parse text",
			commandEvent: newCommandEvent("msg-2", "~raw invalid", ""),
			wantSent:     true,
			wantText:     "raw: invalid article id \"invalid\", expected a positive integer",
		},
		{
			name:         "raw with too many arguments returns parse text",
			commandEvent: newCommandEvent("msg-2", "~raw 1 2", ""),
			wantSent:     true,
			wantText:     "raw: expected at most one integer article id argument",
		},
		{
			name: "raw send failure returns error",
			seedEvents: []*platform.Event{
				newCreatedEvent("msg-1", "hello", ""),
			},
			commandEvent: newCommandEvent("msg-2", "~raw", "msg-1"),
			sendErr:      errors.New("send failed"),
			wantErr:      true,
			wantSent:     true,
		},
		{
			name: "raw output is truncated when too long",
			seedEvents: []*platform.Event{
				newCreatedEvent("msg-1", strings.Repeat("x", 5000), ""),
			},
			commandEvent: newCommandEvent("msg-2", "~raw", "msg-1"),
			wantSent:     true,
			wantTextContains: []string{
				"...(truncated)",
			},
		},
		{
			name: "reply history returns full event stream representation",
			seedEvents: []*platform.Event{
				newCreatedEvent("msg-1", "hello", ""),
				newReactionEvent("msg-1", "👍", platform.ReactionActionAdd),
			},
			commandEvent: newCommandEvent("msg-2", "~history", "msg-1"),
			wantSent:     true,
			wantTextContains: []string{
				"\"Kind\": \"article.created\"",
				"\"Kind\": \"article.reaction.added\"",
			},
		},
		{
			name: "history can target explicit integer article id",
			seedEvents: []*platform.Event{
				newCreatedEvent("114514", "hello", ""),
				newEditedEvent("114514", "hello edited"),
			},
			commandEvent: newCommandEvent("msg-2", "~history 114514", ""),
			wantSent:     true,
			wantTextContains: []string{
				"\"Kind\": \"article.created\"",
				"\"Kind\": \"article.edited\"",
				"\"Before\": {",
			},
			wantTextNotContains: []string{
				"\"Before\": null",
			},
		},
		{
			name:         "history without reply is ignored",
			commandEvent: newCommandEvent("msg-2", "~history", ""),
			wantSent:     false,
		},
		{
			name:         "slash history command is ignored",
			commandEvent: newCommandEvent("msg-2", "/history", "msg-1"),
			wantSent:     false,
		},
		{
			name:         "history with cache miss returns miss text",
			commandEvent: newCommandEvent("msg-2", "~history", "msg-404"),
			wantSent:     true,
			wantText:     "history: replied article not found in memory",
		},
		{
			name:         "history explicit article id cache miss returns miss text",
			commandEvent: newCommandEvent("msg-2", "~history 114514", ""),
			wantSent:     true,
			wantText:     "history: article not found in memory",
		},
		{
			name:         "history invalid article id returns parse text",
			commandEvent: newCommandEvent("msg-2", "~history invalid", ""),
			wantSent:     true,
			wantText:     "history: invalid article id \"invalid\", expected a positive integer",
		},
		{
			name: "history send failure returns error",
			seedEvents: []*platform.Event{
				newCreatedEvent("msg-1", "hello", ""),
			},
			commandEvent: newCommandEvent("msg-2", "~history", "msg-1"),
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

			if dispatcher.lastRequest.ReplyToMessageID != testCase.commandEvent.Article.ID {
				t.Fatalf(
					"reply_to = %q, want %q",
					dispatcher.lastRequest.ReplyToMessageID,
					testCase.commandEvent.Article.ID,
				)
			}
			if dispatcher.lastRequest.Target.Sink == nil {
				t.Fatal("target sink = nil, want source sink")
			}
			if dispatcher.lastRequest.Target.Sink.ID != "tg-main" {
				t.Fatalf("target sink id = %q, want tg-main", dispatcher.lastRequest.Target.Sink.ID)
			}
			if len(dispatcher.lastRequest.Entities) != 1 {
				t.Fatalf("entities len = %d, want 1", len(dispatcher.lastRequest.Entities))
			}
			entity := dispatcher.lastRequest.Entities[0]
			if entity.Type != platform.TextEntityTypeBlockquote {
				t.Fatalf("entity type = %q, want %q", entity.Type, platform.TextEntityTypeBlockquote)
			}
			if !entity.Collapsed {
				t.Fatal("entity collapsed = false, want true")
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
		events    []*platform.Event
		lookup    core.MemoryLookup
		wantFound bool
		wantText  string
	}{
		{
			name: "created article can be read",
			events: []*platform.Event{
				newCreatedEvent("msg-1", "hello", ""),
			},
			lookup: core.MemoryLookup{
				Platform:       platform.PlatformTelegram,
				ConversationID: "chat-1",
				ArticleID:      "msg-1",
			},
			wantFound: true,
			wantText:  "hello",
		},
		{
			name: "edit updates article text",
			events: []*platform.Event{
				newCreatedEvent("msg-1", "hello", ""),
				newEditedEvent("msg-1", "hello edited"),
			},
			lookup: core.MemoryLookup{
				Platform:       platform.PlatformTelegram,
				ConversationID: "chat-1",
				ArticleID:      "msg-1",
			},
			wantFound: true,
			wantText:  "hello edited",
		},
		{
			name: "retracted article is removed",
			events: []*platform.Event{
				newCreatedEvent("msg-1", "hello", ""),
				newRetractedEvent("msg-1"),
			},
			lookup: core.MemoryLookup{
				Platform:       platform.PlatformTelegram,
				ConversationID: "chat-1",
				ArticleID:      "msg-1",
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

			cached, found, err := module.Get(context.Background(), testCase.lookup)
			if err != nil {
				t.Fatalf("get article failed: %v", err)
			}
			if found != testCase.wantFound {
				t.Fatalf("found = %v, want %v", found, testCase.wantFound)
			}
			if !testCase.wantFound {
				return
			}
			if cached.Article.Text != testCase.wantText {
				t.Fatalf("cached text = %q, want %q", cached.Article.Text, testCase.wantText)
			}
		})
	}
}

func TestModuleEditProjectionUpdatesEntitiesAndMedia(t *testing.T) {
	t.Parallel()

	module := New(
		WithTTL(24*time.Hour),
		withClock(func() time.Time { return time.Unix(215, 0).UTC() }),
	)

	created := &platform.Event{
		ID:         "evt-created-msg-1",
		Kind:       platform.EventKindArticleCreated,
		OccurredAt: time.Unix(10, 0).UTC(),
		Source: platform.EventSource{
			Platform: platform.PlatformTelegram,
			ID:       "tg-main",
		},
		Conversation: platform.Conversation{
			ID:   "chat-1",
			Type: platform.ConversationTypeGroup,
		},
		Actor: platform.Actor{ID: "actor-1", DisplayName: "Alice"},
		Article: &platform.Article{
			ID:   "msg-1",
			Text: "hello",
			Entities: []platform.TextEntity{
				{Type: platform.TextEntityTypeBold, Offset: 0, Length: 5},
			},
			Media: []platform.MediaAttachment{
				{ID: "photo-1", Type: platform.MediaTypePhoto},
			},
		},
	}
	edited := &platform.Event{
		ID:         "evt-edit-msg-1",
		Kind:       platform.EventKindArticleEdited,
		OccurredAt: time.Unix(20, 0).UTC(),
		Source: platform.EventSource{
			Platform: platform.PlatformTelegram,
			ID:       "tg-main",
		},
		Conversation: platform.Conversation{
			ID:   "chat-1",
			Type: platform.ConversationTypeGroup,
		},
		Mutation: &platform.ArticleMutation{
			Type:            platform.MutationTypeEdit,
			TargetArticleID: "msg-1",
			After: &platform.ArticleSnapshot{
				Text: "hello edited",
				Entities: []platform.TextEntity{
					{Type: platform.TextEntityTypeItalic, Offset: 0, Length: 11},
				},
				Media: []platform.MediaAttachment{
					{ID: "doc-1", Type: platform.MediaTypeDocument, FileName: "changelog.txt"},
				},
			},
		},
	}

	if err := module.handleEvent(context.Background(), created); err != nil {
		t.Fatalf("seed created event failed: %v", err)
	}
	if err := module.handleEvent(context.Background(), edited); err != nil {
		t.Fatalf("seed edited event failed: %v", err)
	}

	cached, found, err := module.Get(context.Background(), core.MemoryLookup{
		Platform:       platform.PlatformTelegram,
		ConversationID: "chat-1",
		ArticleID:      "msg-1",
	})
	if err != nil {
		t.Fatalf("get article failed: %v", err)
	}
	if !found {
		t.Fatal("expected memory hit")
	}
	if cached.Article.Text != "hello edited" {
		t.Fatalf("article text = %q, want hello edited", cached.Article.Text)
	}
	if len(cached.Article.Entities) != 1 || cached.Article.Entities[0].Type != platform.TextEntityTypeItalic {
		t.Fatalf("article entities = %+v, want one italic entity", cached.Article.Entities)
	}
	if len(cached.Article.Media) != 1 || cached.Article.Media[0].ID != "doc-1" {
		t.Fatalf("article media = %+v, want one doc-1 attachment", cached.Article.Media)
	}
}

func TestModuleGetReplied(t *testing.T) {
	t.Parallel()

	module := New(
		WithTTL(24*time.Hour),
		withClock(func() time.Time { return time.Unix(200, 0).UTC() }),
	)

	origin := newCreatedEvent("msg-1", "origin", "")
	origin.Article.Tags = map[string]string{
		"llmchat.agent": "Otogi",
	}
	if err := module.handleEvent(context.Background(), origin); err != nil {
		t.Fatalf("seed created event failed: %v", err)
	}

	replyEvent := newCreatedEvent("msg-2", "ping", "msg-1")
	cached, found, err := module.GetReplied(context.Background(), replyEvent)
	if err != nil {
		t.Fatalf("get replied article failed: %v", err)
	}
	if !found {
		t.Fatal("expected reply cache hit")
	}
	if cached.Article.Text != "origin" {
		t.Fatalf("reply text = %q, want origin", cached.Article.Text)
	}
	if cached.Article.Tags["llmchat.agent"] != "Otogi" {
		t.Fatalf("reply tags = %+v, want llmchat.agent=Otogi", cached.Article.Tags)
	}

	nonReplyEvent := newCreatedEvent("msg-3", "ping", "")
	_, found, err = module.GetReplied(context.Background(), nonReplyEvent)
	if err != nil {
		t.Fatalf("get replied article for non-reply failed: %v", err)
	}
	if found {
		t.Fatal("expected no reply cache hit")
	}
}

func TestModuleGetReplyChain(t *testing.T) {
	tests := []struct {
		name               string
		seedEvents         []*platform.Event
		currentEvent       *platform.Event
		wantArticleIDs     []string
		wantIsCurrent      []bool
		wantErrSubstring   string
		wantCurrentActorID string
		wantCurrentArticle string
	}{
		{
			name:               "no reply returns current only",
			currentEvent:       newCreatedEvent("msg-1", "current", ""),
			wantArticleIDs:     []string{"msg-1"},
			wantIsCurrent:      []bool{true},
			wantCurrentActorID: "actor-1",
			wantCurrentArticle: "current",
		},
		{
			name: "one-level reply returns parent then current",
			seedEvents: []*platform.Event{
				newCreatedEvent("msg-1", "parent", ""),
			},
			currentEvent:       newCreatedEvent("msg-2", "current", "msg-1"),
			wantArticleIDs:     []string{"msg-1", "msg-2"},
			wantIsCurrent:      []bool{false, true},
			wantCurrentActorID: "actor-1",
			wantCurrentArticle: "current",
		},
		{
			name: "multi-level reply chain returns oldest to newest",
			seedEvents: []*platform.Event{
				newCreatedEvent("msg-1", "root", ""),
				newCreatedEvent("msg-2", "middle", "msg-1"),
			},
			currentEvent:       newCreatedEvent("msg-3", "current", "msg-2"),
			wantArticleIDs:     []string{"msg-1", "msg-2", "msg-3"},
			wantIsCurrent:      []bool{false, false, true},
			wantCurrentActorID: "actor-1",
			wantCurrentArticle: "current",
		},
		{
			name: "missing ancestor returns available tail and current",
			seedEvents: []*platform.Event{
				newCreatedEvent("msg-2", "middle", "msg-1"),
			},
			currentEvent:       newCreatedEvent("msg-3", "current", "msg-2"),
			wantArticleIDs:     []string{"msg-2", "msg-3"},
			wantIsCurrent:      []bool{false, true},
			wantCurrentActorID: "actor-1",
			wantCurrentArticle: "current",
		},
		{
			name: "cycle detection returns error",
			seedEvents: []*platform.Event{
				newCreatedEvent("msg-1", "first", "msg-2"),
				newCreatedEvent("msg-2", "second", "msg-1"),
			},
			currentEvent:     newCreatedEvent("msg-3", "current", "msg-2"),
			wantErrSubstring: "cycle detected",
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			module := New(
				WithTTL(24*time.Hour),
				withClock(func() time.Time { return time.Unix(200, 0).UTC() }),
			)

			for _, seedEvent := range testCase.seedEvents {
				if err := module.handleEvent(context.Background(), seedEvent); err != nil {
					t.Fatalf("seed event %s failed: %v", seedEvent.ID, err)
				}
			}

			chain, err := module.GetReplyChain(context.Background(), testCase.currentEvent)
			if testCase.wantErrSubstring != "" {
				if err == nil {
					t.Fatal("expected error")
				}
				if !strings.Contains(err.Error(), testCase.wantErrSubstring) {
					t.Fatalf("error = %v, want substring %q", err, testCase.wantErrSubstring)
				}
				return
			}
			if err != nil {
				t.Fatalf("GetReplyChain failed: %v", err)
			}

			if len(chain) != len(testCase.wantArticleIDs) {
				t.Fatalf("chain length = %d, want %d", len(chain), len(testCase.wantArticleIDs))
			}
			for index, entry := range chain {
				if entry.Article.ID != testCase.wantArticleIDs[index] {
					t.Fatalf("chain[%d].Article.ID = %q, want %q", index, entry.Article.ID, testCase.wantArticleIDs[index])
				}
				if entry.IsCurrent != testCase.wantIsCurrent[index] {
					t.Fatalf("chain[%d].IsCurrent = %v, want %v", index, entry.IsCurrent, testCase.wantIsCurrent[index])
				}
			}

			last := chain[len(chain)-1]
			if last.Actor.ID != testCase.wantCurrentActorID {
				t.Fatalf("current actor id = %q, want %q", last.Actor.ID, testCase.wantCurrentActorID)
			}
			if last.Article.Text != testCase.wantCurrentArticle {
				t.Fatalf("current article text = %q, want %q", last.Article.Text, testCase.wantCurrentArticle)
			}
		})
	}
}

func TestModuleListConversationContextBefore(t *testing.T) {
	tests := []struct {
		name             string
		ttl              time.Duration
		seedEvents       []*platform.Event
		query            core.ConversationContextBeforeQuery
		cancelBeforeCall bool
		wantArticleIDs   []string
		wantErrSubstring string
	}{
		{
			name: "returns oldest to newest predecessors",
			ttl:  24 * time.Hour,
			seedEvents: []*platform.Event{
				newCreatedEventAt("msg-1", "one", "", time.Unix(10, 0).UTC()),
				newCreatedEventAt("msg-2", "two", "", time.Unix(20, 0).UTC()),
				newCreatedEventAt("msg-3", "three", "", time.Unix(30, 0).UTC()),
				newCreatedEventAt("msg-4", "four", "", time.Unix(40, 0).UTC()),
			},
			query: core.ConversationContextBeforeQuery{
				Platform:        platform.PlatformTelegram,
				ConversationID:  "chat-1",
				AnchorArticleID: "msg-4",
				BeforeLimit:     2,
			},
			wantArticleIDs: []string{"msg-2", "msg-3"},
		},
		{
			name: "respects thread scope",
			ttl:  24 * time.Hour,
			seedEvents: []*platform.Event{
				threadedCreatedEvent("msg-1", "main", "", "", time.Unix(10, 0).UTC()),
				threadedCreatedEvent("msg-2", "topic one", "", "topic-1", time.Unix(20, 0).UTC()),
				threadedCreatedEvent("msg-3", "topic two", "", "topic-2", time.Unix(25, 0).UTC()),
				threadedCreatedEvent("msg-4", "topic one latest", "", "topic-1", time.Unix(30, 0).UTC()),
			},
			query: core.ConversationContextBeforeQuery{
				Platform:        platform.PlatformTelegram,
				ConversationID:  "chat-1",
				ThreadID:        "topic-1",
				AnchorArticleID: "msg-4",
				BeforeLimit:     3,
			},
			wantArticleIDs: []string{"msg-2"},
		},
		{
			name: "retracted articles are removed from predecessor list",
			ttl:  24 * time.Hour,
			seedEvents: []*platform.Event{
				newCreatedEventAt("msg-1", "one", "", time.Unix(10, 0).UTC()),
				newCreatedEventAt("msg-2", "two", "", time.Unix(20, 0).UTC()),
				newCreatedEventAt("msg-3", "three", "", time.Unix(30, 0).UTC()),
				newRetractedEvent("msg-2"),
				newCreatedEventAt("msg-4", "four", "", time.Unix(40, 0).UTC()),
			},
			query: core.ConversationContextBeforeQuery{
				Platform:        platform.PlatformTelegram,
				ConversationID:  "chat-1",
				AnchorArticleID: "msg-4",
				BeforeLimit:     3,
			},
			wantArticleIDs: []string{"msg-1", "msg-3"},
		},
		{
			name: "falls back to anchor time when anchor article is not in memory",
			ttl:  24 * time.Hour,
			seedEvents: []*platform.Event{
				newCreatedEventAt("msg-1", "one", "", time.Unix(10, 0).UTC()),
				newCreatedEventAt("msg-2", "two", "", time.Unix(20, 0).UTC()),
				newCreatedEventAt("msg-3", "three", "", time.Unix(30, 0).UTC()),
			},
			query: core.ConversationContextBeforeQuery{
				Platform:         platform.PlatformTelegram,
				ConversationID:   "chat-1",
				AnchorArticleID:  "msg-current",
				AnchorOccurredAt: time.Unix(35, 0).UTC(),
				BeforeLimit:      2,
				ExcludeArticleIDs: []string{
					"msg-current",
				},
			},
			wantArticleIDs: []string{"msg-2", "msg-3"},
		},
		{
			name: "anchor time query respects excluded articles",
			ttl:  24 * time.Hour,
			seedEvents: []*platform.Event{
				newCreatedEventAt("msg-1", "one", "", time.Unix(10, 0).UTC()),
				newCreatedEventAt("msg-2", "two", "", time.Unix(20, 0).UTC()),
				newCreatedEventAt("msg-3", "three", "", time.Unix(30, 0).UTC()),
			},
			query: core.ConversationContextBeforeQuery{
				Platform:         platform.PlatformTelegram,
				ConversationID:   "chat-1",
				AnchorOccurredAt: time.Unix(35, 0).UTC(),
				BeforeLimit:      3,
				ExcludeArticleIDs: []string{
					"msg-3",
				},
			},
			wantArticleIDs: []string{"msg-1", "msg-2"},
		},
		{
			name: "expired anchor returns empty result",
			ttl:  time.Second,
			seedEvents: []*platform.Event{
				newCreatedEventAt("msg-1", "one", "", time.Unix(10, 0).UTC()),
				newCreatedEventAt("msg-2", "two", "", time.Unix(20, 0).UTC()),
			},
			query: core.ConversationContextBeforeQuery{
				Platform:        platform.PlatformTelegram,
				ConversationID:  "chat-1",
				AnchorArticleID: "msg-2",
				BeforeLimit:     1,
			},
			wantArticleIDs: []string{},
		},
		{
			name: "context cancellation is propagated",
			ttl:  24 * time.Hour,
			seedEvents: []*platform.Event{
				newCreatedEventAt("msg-1", "one", "", time.Unix(10, 0).UTC()),
			},
			query: core.ConversationContextBeforeQuery{
				Platform:        platform.PlatformTelegram,
				ConversationID:  "chat-1",
				AnchorArticleID: "msg-1",
				BeforeLimit:     1,
			},
			cancelBeforeCall: true,
			wantErrSubstring: "context canceled",
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			now := time.Unix(200, 0).UTC()
			module := New(
				WithTTL(testCase.ttl),
				withClock(func() time.Time { return now }),
			)

			for _, seedEvent := range testCase.seedEvents {
				if err := module.handleEvent(context.Background(), seedEvent); err != nil {
					t.Fatalf("seed event %s failed: %v", seedEvent.ID, err)
				}
			}

			if testCase.ttl == time.Second {
				now = now.Add(2 * time.Second)
			}

			ctx := context.Background()
			if testCase.cancelBeforeCall {
				canceledCtx, cancel := context.WithCancel(context.Background())
				cancel()
				ctx = canceledCtx
			}

			entries, err := module.ListConversationContextBefore(ctx, testCase.query)
			if testCase.wantErrSubstring != "" {
				if err == nil {
					t.Fatal("expected error")
				}
				if !strings.Contains(err.Error(), testCase.wantErrSubstring) {
					t.Fatalf("error = %v, want substring %q", err, testCase.wantErrSubstring)
				}
				return
			}
			if err != nil {
				t.Fatalf("ListConversationContextBefore failed: %v", err)
			}

			if len(entries) != len(testCase.wantArticleIDs) {
				t.Fatalf("entries length = %d, want %d", len(entries), len(testCase.wantArticleIDs))
			}
			for index, entry := range entries {
				if entry.Article.ID != testCase.wantArticleIDs[index] {
					t.Fatalf("entries[%d].Article.ID = %q, want %q", index, entry.Article.ID, testCase.wantArticleIDs[index])
				}
			}
		})
	}
}

func TestModuleEventHistoryAndEntityProjectionSeparation(t *testing.T) {
	t.Parallel()

	module := New(
		WithTTL(24*time.Hour),
		withClock(func() time.Time { return time.Unix(220, 0).UTC() }),
	)

	events := []*platform.Event{
		newCreatedEvent("msg-1", "hello", ""),
		newReactionEvent("msg-1", "👍", platform.ReactionActionAdd),
		newEditedEvent("msg-1", "hello edited"),
	}
	for _, event := range events {
		if err := module.handleEvent(context.Background(), event); err != nil {
			t.Fatalf("handle event %s failed: %v", event.ID, err)
		}
	}

	lookup := core.MemoryLookup{
		Platform:       platform.PlatformTelegram,
		ConversationID: "chat-1",
		ArticleID:      "msg-1",
	}

	cached, found, err := module.Get(context.Background(), lookup)
	if err != nil {
		t.Fatalf("get article failed: %v", err)
	}
	if !found {
		t.Fatal("expected article projection cache hit")
	}
	if cached.Article.Text != "hello edited" {
		t.Fatalf("cached text = %q, want hello edited", cached.Article.Text)
	}

	history, found, err := module.getHistory(context.Background(), lookup)
	if err != nil {
		t.Fatalf("get events failed: %v", err)
	}
	if !found {
		t.Fatal("expected event history cache hit")
	}
	if len(history) != 3 {
		t.Fatalf("history length = %d, want 3", len(history))
	}

	wantKinds := []platform.EventKind{
		platform.EventKindArticleCreated,
		platform.EventKindArticleReactionAdded,
		platform.EventKindArticleEdited,
	}
	for idx, wantKind := range wantKinds {
		if history[idx].Kind != wantKind {
			t.Fatalf("history[%d].Kind = %s, want %s", idx, history[idx].Kind, wantKind)
		}
	}
}

func TestModuleGetReturnsArticleAndHistory(t *testing.T) {
	t.Parallel()

	module := New(
		WithTTL(24*time.Hour),
		withClock(func() time.Time { return time.Unix(221, 0).UTC() }),
	)

	events := []*platform.Event{
		newCreatedEvent("msg-1", "hello", ""),
		newReactionEvent("msg-1", "🔥", platform.ReactionActionAdd),
		newEditedEvent("msg-1", "hello edited"),
	}
	events[0].Article.Tags = map[string]string{
		"llmchat.agent": "Otogi",
	}
	for _, event := range events {
		if err := module.handleEvent(context.Background(), event); err != nil {
			t.Fatalf("handle event %s failed: %v", event.ID, err)
		}
	}

	memoryEntry, found, err := module.Get(context.Background(), core.MemoryLookup{
		Platform:       platform.PlatformTelegram,
		ConversationID: "chat-1",
		ArticleID:      "msg-1",
	})
	if err != nil {
		t.Fatalf("get memory failed: %v", err)
	}
	if !found {
		t.Fatal("expected memory hit")
	}
	if memoryEntry.Article.Text != "hello edited" {
		t.Fatalf("article text = %q, want hello edited", memoryEntry.Article.Text)
	}
	if memoryEntry.Article.Tags["llmchat.agent"] != "Otogi" {
		t.Fatalf("article tags = %+v, want llmchat.agent=Otogi", memoryEntry.Article.Tags)
	}
	if len(memoryEntry.History) != 3 {
		t.Fatalf("history length = %d, want 3", len(memoryEntry.History))
	}
}

func TestModuleHistoryBackfillsMissingEditBeforeSnapshot(t *testing.T) {
	t.Parallel()

	module := New(
		WithTTL(24*time.Hour),
		withClock(func() time.Time { return time.Unix(225, 0).UTC() }),
	)

	created := newCreatedEvent("msg-1", "hello", "")
	created.Article.Entities = []platform.TextEntity{
		{Type: platform.TextEntityTypeBold, Offset: 0, Length: 5},
	}
	if err := module.handleEvent(context.Background(), created); err != nil {
		t.Fatalf("seed created event failed: %v", err)
	}
	edited := newEditedEvent("msg-1", "hello edited")
	edited.Mutation.After.Entities = []platform.TextEntity{
		{Type: platform.TextEntityTypeItalic, Offset: 0, Length: 11},
	}
	if err := module.handleEvent(context.Background(), edited); err != nil {
		t.Fatalf("seed edited event failed: %v", err)
	}

	history, found, err := module.getHistory(context.Background(), core.MemoryLookup{
		Platform:       platform.PlatformTelegram,
		ConversationID: "chat-1",
		ArticleID:      "msg-1",
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
	if len(history[1].Mutation.Before.Entities) != 1 || history[1].Mutation.Before.Entities[0].Type != platform.TextEntityTypeBold {
		t.Fatalf("before entities = %+v, want one bold entity", history[1].Mutation.Before.Entities)
	}
	if history[1].Mutation.After == nil {
		t.Fatal("expected mutation after snapshot")
	}
	if history[1].Mutation.After.Text != "hello edited" {
		t.Fatalf("after text = %q, want hello edited", history[1].Mutation.After.Text)
	}
	if len(history[1].Mutation.After.Entities) != 1 || history[1].Mutation.After.Entities[0].Type != platform.TextEntityTypeItalic {
		t.Fatalf("after entities = %+v, want one italic entity", history[1].Mutation.After.Entities)
	}
}

func TestModuleArticleMutationUsesChangedAtForUpdatedAt(t *testing.T) {
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
		newArticleMutationEventWithTimestamps("msg-1", "edited", createdAt, editedAt),
	); err != nil {
		t.Fatalf("seed article mutation event failed: %v", err)
	}

	cached, found, err := module.Get(context.Background(), core.MemoryLookup{
		Platform:       platform.PlatformTelegram,
		ConversationID: "chat-1",
		ArticleID:      "msg-1",
	})
	if err != nil {
		t.Fatalf("get article failed: %v", err)
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
	if cached.Article.Text != "edited" {
		t.Fatalf("article text = %q, want edited", cached.Article.Text)
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
		newReactionEventAt("msg-1", "❤️", platform.ReactionActionAdd, reactionAddedAt),
	); err != nil {
		t.Fatalf("seed reaction add event failed: %v", err)
	}
	if err := module.handleEvent(
		context.Background(),
		newReactionEventAt("msg-1", "❤️", platform.ReactionActionRemove, reactionRemovedAt),
	); err != nil {
		t.Fatalf("seed reaction remove event failed: %v", err)
	}

	cached, found, err := module.Get(context.Background(), core.MemoryLookup{
		Platform:       platform.PlatformTelegram,
		ConversationID: "chat-1",
		ArticleID:      "msg-1",
	})
	if err != nil {
		t.Fatalf("get article failed: %v", err)
	}
	if !found {
		t.Fatal("expected cache hit")
	}
	if len(cached.Article.Reactions) != 0 {
		t.Fatalf("reactions = %+v, want empty", cached.Article.Reactions)
	}
	if !cached.UpdatedAt.Equal(createdAt) {
		t.Fatalf("updated_at = %v, want %v", cached.UpdatedAt, createdAt)
	}
}

func TestModuleRetractedEntityDeletesCachedHistory(t *testing.T) {
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

	lookup := core.MemoryLookup{
		Platform:       platform.PlatformTelegram,
		ConversationID: "chat-1",
		ArticleID:      "msg-1",
	}

	_, found, err := module.Get(context.Background(), lookup)
	if err != nil {
		t.Fatalf("get article failed: %v", err)
	}
	if found {
		t.Fatal("expected retracted article projection to be removed")
	}

	history, found, err := module.getHistory(context.Background(), lookup)
	if err != nil {
		t.Fatalf("get events failed: %v", err)
	}
	if found {
		t.Fatalf("history = %+v, want cache miss after retraction", history)
	}
}

func TestModuleGetRepliedHistory(t *testing.T) {
	t.Parallel()

	module := New(
		WithTTL(24*time.Hour),
		withClock(func() time.Time { return time.Unix(240, 0).UTC() }),
	)

	if err := module.handleEvent(context.Background(), newCreatedEvent("msg-1", "origin", "")); err != nil {
		t.Fatalf("seed created event failed: %v", err)
	}
	if err := module.handleEvent(context.Background(), newReactionEvent("msg-1", "👍", platform.ReactionActionAdd)); err != nil {
		t.Fatalf("seed reaction event failed: %v", err)
	}

	replyEvent := newCreatedEvent("msg-2", "ping", "msg-1")
	history, found, err := module.getRepliedHistory(context.Background(), replyEvent)
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
	_, found, err = module.getRepliedHistory(context.Background(), nonReplyEvent)
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

	_, found, err := module.Get(context.Background(), core.MemoryLookup{
		Platform:       platform.PlatformTelegram,
		ConversationID: "chat-1",
		ArticleID:      "msg-1",
	})
	if err != nil {
		t.Fatalf("lookup oldest article failed: %v", err)
	}
	if found {
		t.Fatal("expected oldest article to be evicted")
	}

	cached, found, err := module.Get(context.Background(), core.MemoryLookup{
		Platform:       platform.PlatformTelegram,
		ConversationID: "chat-1",
		ArticleID:      "msg-2",
	})
	if err != nil {
		t.Fatalf("lookup newest article failed: %v", err)
	}
	if !found {
		t.Fatal("expected newest article to remain")
	}
	if cached.Article.Text != "second" {
		t.Fatalf("cached text = %q, want second", cached.Article.Text)
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
	_, found, err := module.Get(context.Background(), core.MemoryLookup{
		Platform:       platform.PlatformTelegram,
		ConversationID: "chat-1",
		ArticleID:      "msg-1",
	})
	if err != nil {
		t.Fatalf("lookup ttl article failed: %v", err)
	}
	if found {
		t.Fatal("expected expired article to miss")
	}
}

func TestModuleGetArticleReturnsDefensiveCopy(t *testing.T) {
	t.Parallel()

	module := New(
		WithTTL(24*time.Hour),
		withClock(func() time.Time { return time.Unix(500, 0).UTC() }),
	)

	created := newCreatedEvent("msg-1", "copy", "")
	created.Article.Tags = map[string]string{
		"llmchat.agent": "Otogi",
	}
	if err := module.handleEvent(context.Background(), created); err != nil {
		t.Fatalf("handle created event failed: %v", err)
	}

	lookup := core.MemoryLookup{
		Platform:       platform.PlatformTelegram,
		ConversationID: "chat-1",
		ArticleID:      "msg-1",
	}

	cached, found, err := module.Get(context.Background(), lookup)
	if err != nil {
		t.Fatalf("get article failed: %v", err)
	}
	if !found {
		t.Fatal("expected article cache hit")
	}
	cached.Article.Text = "mutated"
	cached.Article.Tags["llmchat.agent"] = "mutated"

	cachedAgain, found, err := module.Get(context.Background(), lookup)
	if err != nil {
		t.Fatalf("get article second time failed: %v", err)
	}
	if !found {
		t.Fatal("expected article cache hit")
	}
	if cachedAgain.Article.Text != "copy" {
		t.Fatalf("cached text = %q, want copy", cachedAgain.Article.Text)
	}
	if cachedAgain.Article.Tags["llmchat.agent"] != "Otogi" {
		t.Fatalf("cached tags = %+v, want llmchat.agent=Otogi", cachedAgain.Article.Tags)
	}
	cached.History = append(cached.History, platform.Event{ID: "evt-mutated"})

	cachedAgain, found, err = module.Get(context.Background(), lookup)
	if err != nil {
		t.Fatalf("get article third time failed: %v", err)
	}
	if !found {
		t.Fatal("expected article cache hit")
	}
	if len(cachedAgain.History) != 1 {
		t.Fatalf("history length = %d, want 1", len(cachedAgain.History))
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

	lookup := core.MemoryLookup{
		Platform:       platform.PlatformTelegram,
		ConversationID: "chat-1",
		ArticleID:      "msg-1",
	}

	history, found, err := module.getHistory(context.Background(), lookup)
	if err != nil {
		t.Fatalf("get events failed: %v", err)
	}
	if !found {
		t.Fatal("expected event history cache hit")
	}
	history[0].ID = "mutated"

	historyAgain, found, err := module.getHistory(context.Background(), lookup)
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

func TestModuleIgnoresDuplicateEventRedelivery(t *testing.T) {
	t.Parallel()

	module := New(
		WithTTL(24*time.Hour),
		withClock(func() time.Time { return time.Unix(520, 0).UTC() }),
	)

	if err := module.handleEvent(context.Background(), newCreatedEvent("msg-1", "hello", "")); err != nil {
		t.Fatalf("handle created event failed: %v", err)
	}

	reactionEvent := newReactionEvent("msg-1", "👍", platform.ReactionActionAdd)
	if err := module.handleEvent(context.Background(), reactionEvent); err != nil {
		t.Fatalf("handle reaction event failed: %v", err)
	}
	if err := module.handleEvent(context.Background(), reactionEvent); err != nil {
		t.Fatalf("handle duplicate reaction event failed: %v", err)
	}

	lookup := core.MemoryLookup{
		Platform:       platform.PlatformTelegram,
		ConversationID: "chat-1",
		ArticleID:      "msg-1",
	}

	cached, found, err := module.Get(context.Background(), lookup)
	if err != nil {
		t.Fatalf("get article failed: %v", err)
	}
	if !found {
		t.Fatal("expected article cache hit")
	}
	if len(cached.Article.Reactions) != 1 {
		t.Fatalf("reactions len = %d, want 1", len(cached.Article.Reactions))
	}
	if cached.Article.Reactions[0].Count != 1 {
		t.Fatalf("reaction count = %d, want 1", cached.Article.Reactions[0].Count)
	}

	history, found, err := module.getHistory(context.Background(), lookup)
	if err != nil {
		t.Fatalf("get history failed: %v", err)
	}
	if !found {
		t.Fatal("expected history cache hit")
	}
	if len(history) != 2 {
		t.Fatalf("history len = %d, want 2", len(history))
	}
	if history[1].ID != reactionEvent.ID {
		t.Fatalf("history[1].ID = %q, want %q", history[1].ID, reactionEvent.ID)
	}
}

func TestTrimForCommandReplyPreservesUTF8Boundaries(t *testing.T) {
	t.Parallel()

	body := strings.Repeat("你", maxCommandReplyLength+1)
	trimmed := trimForCommandReply(body)

	if !utf8.ValidString(trimmed) {
		t.Fatalf("trimmed body is not valid UTF-8: %q", trimmed)
	}
	if !strings.HasSuffix(trimmed, "\n...(truncated)") {
		t.Fatalf("trimmed body = %q, want truncation suffix", trimmed)
	}

	content := strings.TrimSuffix(trimmed, "\n...(truncated)")
	if got := utf8.RuneCountInString(content); got != maxCommandReplyLength {
		t.Fatalf("trimmed rune count = %d, want %d", got, maxCommandReplyLength)
	}
}

func TestModuleGetArticleContextCancellation(t *testing.T) {
	t.Parallel()

	module := New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := module.Get(ctx, core.MemoryLookup{
		Platform:       platform.PlatformTelegram,
		ConversationID: "chat-1",
		ArticleID:      "msg-1",
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

	_, _, err := module.getHistory(ctx, core.MemoryLookup{
		Platform:       platform.PlatformTelegram,
		ConversationID: "chat-1",
		ArticleID:      "msg-1",
	})
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func newCreatedEvent(articleID string, text string, replyToID string) *platform.Event {
	return newCreatedEventAt(articleID, text, replyToID, time.Unix(10, 0).UTC())
}

func newCommandEvent(articleID string, text string, replyToID string) *platform.Event {
	candidate, matched, err := platform.ParseCommandCandidate(text)
	if err != nil {
		panic(err)
	}
	if !matched {
		panic("newCommandEvent expects command text")
	}

	value := strings.Join(candidate.Tokens, " ")
	commandKind := platform.EventKindCommandReceived
	if candidate.Prefix == platform.CommandPrefixSystem {
		commandKind = platform.EventKindSystemCommandReceived
	}

	return &platform.Event{
		ID:         "evt-command-" + articleID,
		Kind:       commandKind,
		OccurredAt: time.Unix(10, 0).UTC(),
		Source: platform.EventSource{
			Platform: platform.PlatformTelegram,
			ID:       "tg-main",
		},
		Conversation: platform.Conversation{
			ID:   "chat-1",
			Type: platform.ConversationTypeGroup,
		},
		Actor: platform.Actor{ID: "actor-1", DisplayName: "Alice"},
		Article: &platform.Article{
			ID:               articleID,
			ReplyToArticleID: replyToID,
			Text:             text,
		},
		Command: &platform.CommandInvocation{
			Name:            candidate.Name,
			Mention:         candidate.Mention,
			Value:           value,
			SourceEventID:   "evt-created-" + articleID,
			SourceEventKind: platform.EventKindArticleCreated,
			RawInput:        text,
		},
	}
}

func newCreatedEventAt(articleID string, text string, replyToID string, occurredAt time.Time) *platform.Event {
	return &platform.Event{
		ID:         "evt-created-" + articleID,
		Kind:       platform.EventKindArticleCreated,
		OccurredAt: occurredAt,
		Source: platform.EventSource{
			Platform: platform.PlatformTelegram,
			ID:       "tg-main",
		},
		Conversation: platform.Conversation{
			ID:   "chat-1",
			Type: platform.ConversationTypeGroup,
		},
		Actor: platform.Actor{ID: "actor-1", DisplayName: "Alice"},
		Article: &platform.Article{
			ID:               articleID,
			ReplyToArticleID: replyToID,
			Text:             text,
		},
	}
}

func threadedCreatedEvent(
	articleID string,
	text string,
	replyToID string,
	threadID string,
	occurredAt time.Time,
) *platform.Event {
	event := newCreatedEventAt(articleID, text, replyToID, occurredAt)
	event.Article.ThreadID = threadID
	return event
}

func newEditedEvent(targetArticleID string, text string) *platform.Event {
	return &platform.Event{
		ID:         "evt-edit-" + targetArticleID,
		Kind:       platform.EventKindArticleEdited,
		OccurredAt: time.Unix(20, 0).UTC(),
		Source: platform.EventSource{
			Platform: platform.PlatformTelegram,
			ID:       "tg-main",
		},
		Conversation: platform.Conversation{
			ID:   "chat-1",
			Type: platform.ConversationTypeGroup,
		},
		Mutation: &platform.ArticleMutation{
			Type:            platform.MutationTypeEdit,
			TargetArticleID: targetArticleID,
			After: &platform.ArticleSnapshot{
				Text: text,
			},
		},
	}
}

func newArticleMutationEvent(targetArticleID string, text string) *platform.Event {
	return newArticleMutationEventWithTimestamps(
		targetArticleID,
		text,
		time.Unix(20, 0).UTC(),
		time.Unix(20, 0).UTC(),
	)
}

func newArticleMutationEventWithTimestamps(
	targetArticleID string,
	text string,
	occurredAt time.Time,
	changedAt time.Time,
) *platform.Event {
	return &platform.Event{
		ID:         "evt-article-mutation-" + targetArticleID,
		Kind:       platform.EventKindArticleCreated,
		OccurredAt: occurredAt,
		Source: platform.EventSource{
			Platform: platform.PlatformTelegram,
			ID:       "tg-main",
		},
		Conversation: platform.Conversation{
			ID:   "chat-1",
			Type: platform.ConversationTypeGroup,
		},
		Actor: platform.Actor{ID: "actor-1", DisplayName: "Alice"},
		Article: &platform.Article{
			ID:   targetArticleID,
			Text: text,
		},
		Mutation: &platform.ArticleMutation{
			Type:            platform.MutationTypeEdit,
			TargetArticleID: targetArticleID,
			ChangedAt:       &changedAt,
			After: &platform.ArticleSnapshot{
				Text: text,
			},
			Reason: "telegram_edit_update",
		},
	}
}

func newRetractedEvent(targetArticleID string) *platform.Event {
	return &platform.Event{
		ID:         "evt-retract-" + targetArticleID,
		Kind:       platform.EventKindArticleRetracted,
		OccurredAt: time.Unix(30, 0).UTC(),
		Source: platform.EventSource{
			Platform: platform.PlatformTelegram,
			ID:       "tg-main",
		},
		Conversation: platform.Conversation{
			ID:   "chat-1",
			Type: platform.ConversationTypeGroup,
		},
		Mutation: &platform.ArticleMutation{
			Type:            platform.MutationTypeRetraction,
			TargetArticleID: targetArticleID,
		},
	}
}

func newReactionEvent(
	targetArticleID string,
	emoji string,
	action platform.ReactionAction,
) *platform.Event {
	return newReactionEventAt(targetArticleID, emoji, action, time.Unix(25, 0).UTC())
}

func newReactionEventAt(
	targetArticleID string,
	emoji string,
	action platform.ReactionAction,
	occurredAt time.Time,
) *platform.Event {
	kind := platform.EventKindArticleReactionAdded
	if action == platform.ReactionActionRemove {
		kind = platform.EventKindArticleReactionRemoved
	}

	return &platform.Event{
		ID:         "evt-reaction-" + targetArticleID + "-" + string(action) + "-" + emoji,
		Kind:       kind,
		OccurredAt: occurredAt,
		Source: platform.EventSource{
			Platform: platform.PlatformTelegram,
			ID:       "tg-main",
		},
		Conversation: platform.Conversation{
			ID:   "chat-1",
			Type: platform.ConversationTypeGroup,
		},
		Actor: platform.Actor{ID: "actor-2", DisplayName: "Bob"},
		Reaction: &platform.Reaction{
			ArticleID: targetArticleID,
			Emoji:     emoji,
			Action:    action,
		},
	}
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

func newServiceRegistryStub() *serviceRegistryStub {
	return &serviceRegistryStub{values: make(map[string]any)}
}

func (s *serviceRegistryStub) Register(name string, service any) error {
	if name == "" {
		return errors.New("empty service name")
	}
	if _, exists := s.values[name]; exists {
		return core.ErrServiceAlreadyRegistered
	}
	s.values[name] = service

	return nil
}

func (s *serviceRegistryStub) Resolve(name string) (any, error) {
	value, ok := s.values[name]
	if !ok {
		return nil, core.ErrServiceNotFound
	}

	return value, nil
}

type captureDispatcher struct {
	calls       atomic.Int64
	lastRequest platform.SendMessageRequest
	sendErr     error
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

	return &platform.OutboundMessage{ID: "sent-1"}, nil
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
