package core

import (
	"context"
	"testing"
	"time"

	. "ex-otogi/pkg/otogi/platform"
)

func TestMemoryLookupValidate(t *testing.T) {
	tests := []struct {
		name    string
		lookup  MemoryLookup
		wantErr bool
	}{
		{
			name: "valid lookup",
			lookup: MemoryLookup{
				Platform:       PlatformTelegram,
				ConversationID: "chat-1",
				ArticleID:      "msg-1",
			},
		},
		{
			name: "missing platform",
			lookup: MemoryLookup{
				ConversationID: "chat-1",
				ArticleID:      "msg-1",
			},
			wantErr: true,
		},
		{
			name: "missing conversation id",
			lookup: MemoryLookup{
				Platform:  PlatformTelegram,
				ArticleID: "msg-1",
			},
			wantErr: true,
		},
		{
			name: "missing article id",
			lookup: MemoryLookup{
				Platform:       PlatformTelegram,
				ConversationID: "chat-1",
			},
			wantErr: true,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			err := testCase.lookup.Validate()
			if testCase.wantErr && err == nil {
				t.Fatal("expected validation error")
			}
			if !testCase.wantErr && err != nil {
				t.Fatalf("unexpected validation error: %v", err)
			}
		})
	}
}

func TestMemoryLookupFromEvent(t *testing.T) {
	tests := []struct {
		name       string
		event      *Event
		want       MemoryLookup
		wantErr    bool
		assertFunc func(*testing.T, MemoryLookup)
	}{
		{
			name: "valid article event",
			event: &Event{
				ID:         "evt-1",
				Kind:       EventKindArticleCreated,
				OccurredAt: time.Unix(1, 0).UTC(),
				Source: EventSource{
					Platform: PlatformTelegram,
				},
				Conversation: Conversation{
					ID: "chat-1",
				},
				Article: &Article{ID: "msg-1"},
			},
			want: MemoryLookup{
				Platform:       PlatformTelegram,
				ConversationID: "chat-1",
				ArticleID:      "msg-1",
			},
		},
		{
			name:    "nil event",
			event:   nil,
			wantErr: true,
		},
		{
			name: "missing article payload",
			event: &Event{
				Kind:       EventKindArticleCreated,
				OccurredAt: time.Unix(1, 0).UTC(),
				Source: EventSource{
					Platform: PlatformTelegram,
				},
				Conversation: Conversation{
					ID: "chat-1",
				},
			},
			wantErr: true,
		},
		{
			name: "missing article id",
			event: &Event{
				Kind:       EventKindArticleCreated,
				OccurredAt: time.Unix(1, 0).UTC(),
				Source: EventSource{
					Platform: PlatformTelegram,
				},
				Conversation: Conversation{
					ID: "chat-1",
				},
				Article: &Article{},
			},
			wantErr: true,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			got, err := MemoryLookupFromEvent(testCase.event)
			if testCase.wantErr && err == nil {
				t.Fatal("expected error")
			}
			if !testCase.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if testCase.wantErr {
				return
			}

			if got != testCase.want {
				t.Fatalf("lookup = %#v, want %#v", got, testCase.want)
			}
			if testCase.assertFunc != nil {
				testCase.assertFunc(t, got)
			}
		})
	}
}

func TestReplyMemoryLookupFromEvent(t *testing.T) {
	tests := []struct {
		name    string
		event   *Event
		want    MemoryLookup
		wantErr bool
	}{
		{
			name: "valid reply event",
			event: &Event{
				ID:         "evt-1",
				Kind:       EventKindArticleCreated,
				OccurredAt: time.Unix(1, 0).UTC(),
				Source: EventSource{
					Platform: PlatformTelegram,
				},
				Conversation: Conversation{
					ID: "chat-1",
				},
				Article: &Article{ID: "msg-2", ReplyToArticleID: "msg-1"},
			},
			want: MemoryLookup{
				Platform:       PlatformTelegram,
				ConversationID: "chat-1",
				ArticleID:      "msg-1",
			},
		},
		{
			name:    "nil event",
			event:   nil,
			wantErr: true,
		},
		{
			name: "missing article payload",
			event: &Event{
				Kind:       EventKindArticleCreated,
				OccurredAt: time.Unix(1, 0).UTC(),
				Source: EventSource{
					Platform: PlatformTelegram,
				},
				Conversation: Conversation{
					ID: "chat-1",
				},
			},
			wantErr: true,
		},
		{
			name: "missing reply id",
			event: &Event{
				Kind:       EventKindArticleCreated,
				OccurredAt: time.Unix(1, 0).UTC(),
				Source: EventSource{
					Platform: PlatformTelegram,
				},
				Conversation: Conversation{
					ID: "chat-1",
				},
				Article: &Article{ID: "msg-2"},
			},
			wantErr: true,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			got, err := ReplyMemoryLookupFromEvent(testCase.event)
			if testCase.wantErr && err == nil {
				t.Fatal("expected error")
			}
			if !testCase.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if testCase.wantErr {
				return
			}
			if got != testCase.want {
				t.Fatalf("lookup = %#v, want %#v", got, testCase.want)
			}
		})
	}
}

func TestMutationMemoryLookupFromEvent(t *testing.T) {
	tests := []struct {
		name    string
		event   *Event
		want    MemoryLookup
		wantErr bool
	}{
		{
			name: "valid mutation event",
			event: &Event{
				Kind:       EventKindArticleEdited,
				OccurredAt: time.Unix(1, 0).UTC(),
				Source: EventSource{
					Platform: PlatformTelegram,
				},
				Conversation: Conversation{
					ID: "chat-1",
				},
				Mutation: &ArticleMutation{
					TargetArticleID: "msg-1",
				},
			},
			want: MemoryLookup{
				Platform:       PlatformTelegram,
				ConversationID: "chat-1",
				ArticleID:      "msg-1",
			},
		},
		{
			name:    "nil event",
			event:   nil,
			wantErr: true,
		},
		{
			name: "missing mutation payload",
			event: &Event{
				Kind:       EventKindArticleEdited,
				OccurredAt: time.Unix(1, 0).UTC(),
				Source: EventSource{
					Platform: PlatformTelegram,
				},
				Conversation: Conversation{
					ID: "chat-1",
				},
			},
			wantErr: true,
		},
		{
			name: "missing target article id",
			event: &Event{
				Kind:       EventKindArticleEdited,
				OccurredAt: time.Unix(1, 0).UTC(),
				Source: EventSource{
					Platform: PlatformTelegram,
				},
				Conversation: Conversation{
					ID: "chat-1",
				},
				Mutation: &ArticleMutation{},
			},
			wantErr: true,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			got, err := MutationMemoryLookupFromEvent(testCase.event)
			if testCase.wantErr && err == nil {
				t.Fatal("expected error")
			}
			if !testCase.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if testCase.wantErr {
				return
			}
			if got != testCase.want {
				t.Fatalf("lookup = %#v, want %#v", got, testCase.want)
			}
		})
	}
}

func TestReactionMemoryLookupFromEvent(t *testing.T) {
	tests := []struct {
		name    string
		event   *Event
		want    MemoryLookup
		wantErr bool
	}{
		{
			name: "valid reaction event",
			event: &Event{
				Kind:       EventKindArticleReactionAdded,
				OccurredAt: time.Unix(1, 0).UTC(),
				Source: EventSource{
					Platform: PlatformTelegram,
				},
				Conversation: Conversation{
					ID: "chat-1",
				},
				Reaction: &Reaction{
					ArticleID: "msg-1",
				},
			},
			want: MemoryLookup{
				Platform:       PlatformTelegram,
				ConversationID: "chat-1",
				ArticleID:      "msg-1",
			},
		},
		{
			name:    "nil event",
			event:   nil,
			wantErr: true,
		},
		{
			name: "missing reaction payload",
			event: &Event{
				Kind:       EventKindArticleReactionAdded,
				OccurredAt: time.Unix(1, 0).UTC(),
				Source: EventSource{
					Platform: PlatformTelegram,
				},
				Conversation: Conversation{
					ID: "chat-1",
				},
			},
			wantErr: true,
		},
		{
			name: "missing reaction article id",
			event: &Event{
				Kind:       EventKindArticleReactionAdded,
				OccurredAt: time.Unix(1, 0).UTC(),
				Source: EventSource{
					Platform: PlatformTelegram,
				},
				Conversation: Conversation{
					ID: "chat-1",
				},
				Reaction: &Reaction{},
			},
			wantErr: true,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			got, err := ReactionMemoryLookupFromEvent(testCase.event)
			if testCase.wantErr && err == nil {
				t.Fatal("expected error")
			}
			if !testCase.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if testCase.wantErr {
				return
			}
			if got != testCase.want {
				t.Fatalf("lookup = %#v, want %#v", got, testCase.want)
			}
		})
	}
}

func TestTargetMemoryLookupFromEvent(t *testing.T) {
	tests := []struct {
		name    string
		event   *Event
		want    MemoryLookup
		wantErr bool
	}{
		{
			name: "article created target lookup",
			event: &Event{
				Kind:       EventKindArticleCreated,
				OccurredAt: time.Unix(1, 0).UTC(),
				Source: EventSource{
					Platform: PlatformTelegram,
				},
				Conversation: Conversation{
					ID: "chat-1",
				},
				Article: &Article{
					ID: "msg-created",
				},
			},
			want: MemoryLookup{
				Platform:       PlatformTelegram,
				ConversationID: "chat-1",
				ArticleID:      "msg-created",
			},
		},
		{
			name: "article edited target lookup",
			event: &Event{
				Kind:       EventKindArticleEdited,
				OccurredAt: time.Unix(1, 0).UTC(),
				Source: EventSource{
					Platform: PlatformTelegram,
				},
				Conversation: Conversation{
					ID: "chat-1",
				},
				Mutation: &ArticleMutation{
					TargetArticleID: "msg-edited",
				},
			},
			want: MemoryLookup{
				Platform:       PlatformTelegram,
				ConversationID: "chat-1",
				ArticleID:      "msg-edited",
			},
		},
		{
			name: "article retracted target lookup",
			event: &Event{
				Kind:       EventKindArticleRetracted,
				OccurredAt: time.Unix(1, 0).UTC(),
				Source: EventSource{
					Platform: PlatformTelegram,
				},
				Conversation: Conversation{
					ID: "chat-1",
				},
				Mutation: &ArticleMutation{
					TargetArticleID: "msg-retracted",
				},
			},
			want: MemoryLookup{
				Platform:       PlatformTelegram,
				ConversationID: "chat-1",
				ArticleID:      "msg-retracted",
			},
		},
		{
			name: "article reaction added target lookup",
			event: &Event{
				Kind:       EventKindArticleReactionAdded,
				OccurredAt: time.Unix(1, 0).UTC(),
				Source: EventSource{
					Platform: PlatformTelegram,
				},
				Conversation: Conversation{
					ID: "chat-1",
				},
				Reaction: &Reaction{
					ArticleID: "msg-react-add",
				},
			},
			want: MemoryLookup{
				Platform:       PlatformTelegram,
				ConversationID: "chat-1",
				ArticleID:      "msg-react-add",
			},
		},
		{
			name: "article reaction removed target lookup",
			event: &Event{
				Kind:       EventKindArticleReactionRemoved,
				OccurredAt: time.Unix(1, 0).UTC(),
				Source: EventSource{
					Platform: PlatformTelegram,
				},
				Conversation: Conversation{
					ID: "chat-1",
				},
				Reaction: &Reaction{
					ArticleID: "msg-react-remove",
				},
			},
			want: MemoryLookup{
				Platform:       PlatformTelegram,
				ConversationID: "chat-1",
				ArticleID:      "msg-react-remove",
			},
		},
		{
			name: "unsupported event kind",
			event: &Event{
				Kind:       EventKindMemberJoined,
				OccurredAt: time.Unix(1, 0).UTC(),
				Source: EventSource{
					Platform: PlatformTelegram,
				},
				Conversation: Conversation{
					ID: "chat-1",
				},
			},
			wantErr: true,
		},
		{
			name:    "nil event",
			event:   nil,
			wantErr: true,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			got, err := TargetMemoryLookupFromEvent(testCase.event)
			if testCase.wantErr && err == nil {
				t.Fatal("expected error")
			}
			if !testCase.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if testCase.wantErr {
				return
			}
			if got != testCase.want {
				t.Fatalf("lookup = %#v, want %#v", got, testCase.want)
			}
		})
	}
}

func TestReplyChainEntryFields(t *testing.T) {
	t.Parallel()

	createdAt := time.Unix(10, 0).UTC()
	updatedAt := time.Unix(20, 0).UTC()
	entry := ReplyChainEntry{
		Conversation: Conversation{ID: "chat-1", Type: ConversationTypeGroup},
		Actor:        Actor{ID: "u-1", Username: "alice"},
		Article:      Article{ID: "m-1", Text: "hello"},
		CreatedAt:    createdAt,
		UpdatedAt:    updatedAt,
		IsCurrent:    true,
	}

	if !entry.IsCurrent {
		t.Fatal("expected IsCurrent to be true")
	}
	if entry.Article.ID != "m-1" {
		t.Fatalf("article id = %q, want m-1", entry.Article.ID)
	}
	if entry.Actor.Username != "alice" {
		t.Fatalf("actor username = %q, want alice", entry.Actor.Username)
	}
	if !entry.CreatedAt.Equal(createdAt) {
		t.Fatalf("created_at = %v, want %v", entry.CreatedAt, createdAt)
	}
	if !entry.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("updated_at = %v, want %v", entry.UpdatedAt, updatedAt)
	}
}

func TestConversationContextBeforeQueryValidate(t *testing.T) {
	tests := []struct {
		name    string
		query   ConversationContextBeforeQuery
		wantErr bool
	}{
		{
			name: "valid query",
			query: ConversationContextBeforeQuery{
				Platform:        PlatformTelegram,
				ConversationID:  "chat-1",
				ThreadID:        "topic-1",
				AnchorArticleID: "msg-3",
				BeforeLimit:     3,
			},
		},
		{
			name: "valid query using anchor time only",
			query: ConversationContextBeforeQuery{
				Platform:         PlatformTelegram,
				ConversationID:   "chat-1",
				AnchorOccurredAt: time.Unix(10, 0).UTC(),
				BeforeLimit:      3,
			},
		},
		{
			name: "missing platform",
			query: ConversationContextBeforeQuery{
				ConversationID:  "chat-1",
				AnchorArticleID: "msg-3",
				BeforeLimit:     3,
			},
			wantErr: true,
		},
		{
			name: "missing conversation id",
			query: ConversationContextBeforeQuery{
				Platform:        PlatformTelegram,
				AnchorArticleID: "msg-3",
				BeforeLimit:     3,
			},
			wantErr: true,
		},
		{
			name: "missing both article id and anchor time",
			query: ConversationContextBeforeQuery{
				Platform:       PlatformTelegram,
				ConversationID: "chat-1",
				BeforeLimit:    3,
			},
			wantErr: true,
		},
		{
			name: "negative before limit",
			query: ConversationContextBeforeQuery{
				Platform:        PlatformTelegram,
				ConversationID:  "chat-1",
				AnchorArticleID: "msg-3",
				BeforeLimit:     -1,
			},
			wantErr: true,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			err := testCase.query.Validate()
			if testCase.wantErr && err == nil {
				t.Fatal("expected validation error")
			}
			if !testCase.wantErr && err != nil {
				t.Fatalf("unexpected validation error: %v", err)
			}
		})
	}
}

func TestConversationContextEntryFields(t *testing.T) {
	t.Parallel()

	createdAt := time.Unix(30, 0).UTC()
	updatedAt := time.Unix(40, 0).UTC()
	entry := ConversationContextEntry{
		Conversation: Conversation{ID: "chat-1", Type: ConversationTypeGroup},
		Actor:        Actor{ID: "u-2", DisplayName: "Bob"},
		Article:      Article{ID: "m-2", Text: "world"},
		CreatedAt:    createdAt,
		UpdatedAt:    updatedAt,
	}

	if entry.Article.ID != "m-2" {
		t.Fatalf("article id = %q, want m-2", entry.Article.ID)
	}
	if entry.Actor.DisplayName != "Bob" {
		t.Fatalf("actor display name = %q, want Bob", entry.Actor.DisplayName)
	}
	if !entry.CreatedAt.Equal(createdAt) {
		t.Fatalf("created_at = %v, want %v", entry.CreatedAt, createdAt)
	}
	if !entry.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("updated_at = %v, want %v", entry.UpdatedAt, updatedAt)
	}
}

type memoryServiceContractStub struct{}

func (memoryServiceContractStub) Get(context.Context, MemoryLookup) (Memory, bool, error) {
	return Memory{}, false, nil
}

func (memoryServiceContractStub) GetBatch(context.Context, []MemoryLookup) (map[MemoryLookup]Memory, error) {
	return nil, nil
}

func (memoryServiceContractStub) GetReplied(context.Context, *Event) (Memory, bool, error) {
	return Memory{}, false, nil
}

func (memoryServiceContractStub) GetReplyChain(context.Context, *Event) ([]ReplyChainEntry, error) {
	return nil, nil
}

func (memoryServiceContractStub) ListConversationContextBefore(
	context.Context,
	ConversationContextBeforeQuery,
) ([]ConversationContextEntry, error) {
	return nil, nil
}

var _ MemoryService = (*memoryServiceContractStub)(nil)
