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
				ID:         "tg:message.created:100:777",
				Type:       UpdateTypeMessage,
				OccurredAt: occurredAt,
				Chat: ChatRef{
					ID:   "100",
					Type: otogi.ConversationTypeGroup,
				},
				Actor: ActorRef{ID: "42"},
				Message: &MessagePayload{
					ID:   "777",
					Text: "hello",
					Reactions: []otogi.MessageReaction{
						{
							Emoji: "❤️",
							Count: 1,
						},
					},
				},
			},
			assert: func(t *testing.T, event *otogi.Event) {
				t.Helper()
				if event.Kind != otogi.EventKindMessageCreated {
					t.Fatalf("kind = %s, want %s", event.Kind, otogi.EventKindMessageCreated)
				}
				if event.Message == nil {
					t.Fatal("expected message payload")
				}
				if event.Message.ID != "777" {
					t.Fatalf("message id = %s, want 777", event.Message.ID)
				}
				if event.Mutation != nil {
					t.Fatalf("mutation = %+v, want nil", event.Mutation)
				}
				if len(event.Message.Reactions) != 1 {
					t.Fatalf("reactions length = %d, want 1", len(event.Message.Reactions))
				}
				if event.Message.Reactions[0].Emoji != "❤️" || event.Message.Reactions[0].Count != 1 {
					t.Fatalf("reactions[0] = %+v, want {Emoji:❤️ Count:1}", event.Message.Reactions[0])
				}
			},
		},
		{
			name: "edit update with explicit changed_at",
			update: Update{
				ID:         "tg:message.edited:100:777:1700000111000000000",
				Type:       UpdateTypeEdit,
				OccurredAt: occurredAt,
				Chat: ChatRef{
					ID:   "100",
					Type: otogi.ConversationTypeGroup,
				},
				Actor: ActorRef{ID: "42"},
				Edit: &EditPayload{
					MessageID: "777",
					ChangedAt: &changedAt,
					Before: &SnapshotPayload{
						Text: "hello",
					},
					After: &SnapshotPayload{
						Text: "hello edited",
					},
					Reason: "telegram_edit_update",
				},
			},
			assert: func(t *testing.T, event *otogi.Event) {
				t.Helper()
				if event.Kind != otogi.EventKindMessageEdited {
					t.Fatalf("kind = %s, want %s", event.Kind, otogi.EventKindMessageEdited)
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
				if event.Mutation.TargetMessageID != "777" {
					t.Fatalf("target message id = %s, want 777", event.Mutation.TargetMessageID)
				}
				if event.Mutation.Before == nil || event.Mutation.Before.Text != "hello" {
					t.Fatalf("mutation before = %+v, want text hello", event.Mutation.Before)
				}
				if event.Mutation.After == nil || event.Mutation.After.Text != "hello edited" {
					t.Fatalf("mutation after = %+v, want text hello edited", event.Mutation.After)
				}
				if event.Message != nil {
					t.Fatalf("message = %+v, want nil", event.Message)
				}
			},
		},
		{
			name: "edit update falls back changed_at to occurred_at",
			update: Update{
				ID:         "tg:message.edited:100:777:1700000000000000000",
				Type:       UpdateTypeEdit,
				OccurredAt: occurredAt,
				Chat: ChatRef{
					ID:   "100",
					Type: otogi.ConversationTypeGroup,
				},
				Actor: ActorRef{ID: "42"},
				Edit: &EditPayload{
					MessageID: "777",
					After: &SnapshotPayload{
						Text: "legacy edit",
					},
				},
			},
			assert: func(t *testing.T, event *otogi.Event) {
				t.Helper()
				if event.Kind != otogi.EventKindMessageEdited {
					t.Fatalf("kind = %s, want %s", event.Kind, otogi.EventKindMessageEdited)
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
