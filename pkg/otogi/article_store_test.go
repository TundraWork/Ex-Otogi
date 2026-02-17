package otogi

import (
	"testing"
	"time"
)

func TestArticleLookupValidate(t *testing.T) {
	tests := []struct {
		name    string
		lookup  ArticleLookup
		wantErr bool
	}{
		{
			name: "valid lookup",
			lookup: ArticleLookup{
				Platform:       PlatformTelegram,
				ConversationID: "chat-1",
				ArticleID:      "msg-1",
			},
		},
		{
			name: "missing platform",
			lookup: ArticleLookup{
				ConversationID: "chat-1",
				ArticleID:      "msg-1",
			},
			wantErr: true,
		},
		{
			name: "missing conversation id",
			lookup: ArticleLookup{
				Platform:  PlatformTelegram,
				ArticleID: "msg-1",
			},
			wantErr: true,
		},
		{
			name: "missing article id",
			lookup: ArticleLookup{
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

func TestArticleLookupFromEvent(t *testing.T) {
	tests := []struct {
		name       string
		event      *Event
		want       ArticleLookup
		wantErr    bool
		assertFunc func(*testing.T, ArticleLookup)
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
			want: ArticleLookup{
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

			got, err := ArticleLookupFromEvent(testCase.event)
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

func TestReplyArticleLookupFromEvent(t *testing.T) {
	tests := []struct {
		name    string
		event   *Event
		want    ArticleLookup
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
			want: ArticleLookup{
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

			got, err := ReplyArticleLookupFromEvent(testCase.event)
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

func TestMutationArticleLookupFromEvent(t *testing.T) {
	tests := []struct {
		name    string
		event   *Event
		want    ArticleLookup
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
			want: ArticleLookup{
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

			got, err := MutationArticleLookupFromEvent(testCase.event)
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

func TestReactionArticleLookupFromEvent(t *testing.T) {
	tests := []struct {
		name    string
		event   *Event
		want    ArticleLookup
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
			want: ArticleLookup{
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

			got, err := ReactionArticleLookupFromEvent(testCase.event)
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

func TestTargetArticleLookupFromEvent(t *testing.T) {
	tests := []struct {
		name    string
		event   *Event
		want    ArticleLookup
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
			want: ArticleLookup{
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
			want: ArticleLookup{
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
			want: ArticleLookup{
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
			want: ArticleLookup{
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
			want: ArticleLookup{
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

			got, err := TargetArticleLookupFromEvent(testCase.event)
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
