package otogi

import (
	"testing"
	"time"
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
				Platform:   PlatformTelegram,
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
				Platform:   PlatformTelegram,
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
				Platform:   PlatformTelegram,
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
				Platform:   PlatformTelegram,
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
				Platform:   PlatformTelegram,
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
				Platform:   PlatformTelegram,
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
				Platform:   PlatformTelegram,
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
				Platform:   PlatformTelegram,
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
				Platform:   PlatformTelegram,
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
				Platform:   PlatformTelegram,
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
				Platform:   PlatformTelegram,
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
				Platform:   PlatformTelegram,
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
				Platform:   PlatformTelegram,
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
				Platform:   PlatformTelegram,
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
				Platform:   PlatformTelegram,
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
				Platform:   PlatformTelegram,
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
				Platform:   PlatformTelegram,
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
				Platform:   PlatformTelegram,
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
