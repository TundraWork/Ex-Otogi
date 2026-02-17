package telegram

import (
	"context"
	"testing"
	"time"

	"ex-otogi/pkg/otogi"
)

func TestDefaultDecoderDecodeMessageAndEditPayloads(t *testing.T) {
	t.Parallel()

	decoder := NewDefaultDecoder()
	occurredAt := time.Unix(1_700_000_000, 0).UTC()
	changedAt := time.Unix(1_700_000_111, 0).UTC()

	tests := []struct {
		name   string
		update Update
		assert func(t *testing.T, event *otogi.Event)
	}{
		{
			name: "message update without mutation",
			update: Update{
				ID:         "tg:article.created:100:777",
				Type:       UpdateTypeMessage,
				OccurredAt: occurredAt,
				Chat: ChatRef{
					ID:   "100",
					Type: otogi.ConversationTypeGroup,
				},
				Actor: ActorRef{ID: "42"},
				Article: &ArticlePayload{
					ID:   "777",
					Text: "hello",
					Reactions: []otogi.ArticleReaction{
						{
							Emoji: "❤️",
							Count: 1,
						},
					},
				},
			},
			assert: func(t *testing.T, event *otogi.Event) {
				t.Helper()
				if event.Kind != otogi.EventKindArticleCreated {
					t.Fatalf("kind = %s, want %s", event.Kind, otogi.EventKindArticleCreated)
				}
				if event.Article == nil {
					t.Fatal("expected article payload")
				}
				if event.Article.ID != "777" {
					t.Fatalf("article id = %s, want 777", event.Article.ID)
				}
				if event.Mutation != nil {
					t.Fatalf("mutation = %+v, want nil", event.Mutation)
				}
				if len(event.Article.Reactions) != 1 {
					t.Fatalf("reactions length = %d, want 1", len(event.Article.Reactions))
				}
				if event.Article.Reactions[0].Emoji != "❤️" || event.Article.Reactions[0].Count != 1 {
					t.Fatalf("reactions[0] = %+v, want {Emoji:❤️ Count:1}", event.Article.Reactions[0])
				}
			},
		},
		{
			name: "edit update with explicit changed_at",
			update: Update{
				ID:         "tg:article.edited:100:777:1700000111000000000",
				Type:       UpdateTypeEdit,
				OccurredAt: occurredAt,
				Chat: ChatRef{
					ID:   "100",
					Type: otogi.ConversationTypeGroup,
				},
				Actor: ActorRef{ID: "42"},
				Edit: &ArticleEditPayload{
					ArticleID: "777",
					ChangedAt: &changedAt,
					Before: &ArticleSnapshotPayload{
						Text: "hello",
						Entities: []otogi.TextEntity{
							{Type: otogi.TextEntityTypeBold, Offset: 0, Length: 5},
						},
					},
					After: &ArticleSnapshotPayload{
						Text: "hello edited",
						Entities: []otogi.TextEntity{
							{Type: otogi.TextEntityTypeItalic, Offset: 0, Length: 11},
						},
					},
					Reason: "telegram_edit_update",
				},
			},
			assert: func(t *testing.T, event *otogi.Event) {
				t.Helper()
				if event.Kind != otogi.EventKindArticleEdited {
					t.Fatalf("kind = %s, want %s", event.Kind, otogi.EventKindArticleEdited)
				}
				if event.Mutation == nil {
					t.Fatal("expected mutation payload")
				}
				if event.Mutation.Type != otogi.MutationTypeEdit {
					t.Fatalf("mutation type = %s, want %s", event.Mutation.Type, otogi.MutationTypeEdit)
				}
				if event.Mutation.ChangedAt == nil {
					t.Fatal("expected mutation changed_at")
				}
				if !event.Mutation.ChangedAt.Equal(changedAt) {
					t.Fatalf("mutation changed_at = %v, want %v", event.Mutation.ChangedAt, changedAt)
				}
				if event.Mutation.TargetArticleID != "777" {
					t.Fatalf("target article id = %s, want 777", event.Mutation.TargetArticleID)
				}
				if event.Mutation.Before == nil || event.Mutation.Before.Text != "hello" {
					t.Fatalf("mutation before = %+v, want text hello", event.Mutation.Before)
				}
				if len(event.Mutation.Before.Entities) != 1 || event.Mutation.Before.Entities[0].Type != otogi.TextEntityTypeBold {
					t.Fatalf("mutation before entities = %+v, want one bold entity", event.Mutation.Before.Entities)
				}
				if event.Mutation.After == nil || event.Mutation.After.Text != "hello edited" {
					t.Fatalf("mutation after = %+v, want text hello edited", event.Mutation.After)
				}
				if len(event.Mutation.After.Entities) != 1 || event.Mutation.After.Entities[0].Type != otogi.TextEntityTypeItalic {
					t.Fatalf("mutation after entities = %+v, want one italic entity", event.Mutation.After.Entities)
				}
				if event.Article != nil {
					t.Fatalf("article = %+v, want nil", event.Article)
				}
			},
		},
		{
			name: "edit update falls back changed_at to occurred_at",
			update: Update{
				ID:         "tg:article.edited:100:777:1700000000000000000",
				Type:       UpdateTypeEdit,
				OccurredAt: occurredAt,
				Chat: ChatRef{
					ID:   "100",
					Type: otogi.ConversationTypeGroup,
				},
				Actor: ActorRef{ID: "42"},
				Edit: &ArticleEditPayload{
					ArticleID: "777",
					After: &ArticleSnapshotPayload{
						Text: "legacy edit",
					},
				},
			},
			assert: func(t *testing.T, event *otogi.Event) {
				t.Helper()
				if event.Kind != otogi.EventKindArticleEdited {
					t.Fatalf("kind = %s, want %s", event.Kind, otogi.EventKindArticleEdited)
				}
				if event.Mutation == nil {
					t.Fatal("expected mutation payload")
				}
				if event.Mutation.ChangedAt == nil {
					t.Fatal("expected mutation changed_at")
				}
				if !event.Mutation.ChangedAt.Equal(occurredAt) {
					t.Fatalf("mutation changed_at = %v, want %v", event.Mutation.ChangedAt, occurredAt)
				}
			},
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			event, err := decoder.Decode(context.Background(), testCase.update)
			if err != nil {
				t.Fatalf("decode failed: %v", err)
			}
			testCase.assert(t, event)
		})
	}
}

func TestDefaultDecoderDecodeReactionRemove(t *testing.T) {
	t.Parallel()

	decoder := NewDefaultDecoder()
	occurredAt := time.Unix(1_700_000_000, 0).UTC()

	event, err := decoder.Decode(context.Background(), Update{
		ID:         "tg:article.reaction.removed:500:777:1700000000000000000",
		Type:       UpdateTypeReactionRemove,
		OccurredAt: occurredAt,
		Chat: ChatRef{
			ID:   "500",
			Type: otogi.ConversationTypeChannel,
		},
		Actor: ActorRef{ID: "42"},
		Reaction: &ArticleReactionPayload{
			ArticleID: "777",
			Emoji:     "❤️",
		},
	})
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if event.Kind != otogi.EventKindArticleReactionRemoved {
		t.Fatalf("kind = %s, want %s", event.Kind, otogi.EventKindArticleReactionRemoved)
	}
	if event.Reaction == nil {
		t.Fatal("expected reaction payload")
	}
	if event.Reaction.Action != otogi.ReactionActionRemove {
		t.Fatalf("reaction action = %s, want %s", event.Reaction.Action, otogi.ReactionActionRemove)
	}
	if event.Reaction.ArticleID != "777" {
		t.Fatalf("reaction article id = %s, want 777", event.Reaction.ArticleID)
	}
	if event.Reaction.Emoji != "❤️" {
		t.Fatalf("reaction emoji = %s, want ❤️", event.Reaction.Emoji)
	}
}
