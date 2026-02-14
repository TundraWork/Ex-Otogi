package telegram

import (
	"context"
	"errors"
	"testing"
	"time"

	"ex-otogi/pkg/otogi"

	"github.com/gotd/td/tg"
)

func TestDefaultGotdUpdateMapperMap(t *testing.T) {
	t.Parallel()

	mapper := NewDefaultGotdUpdateMapper()
	occurredAt := time.Unix(1_700_000_000, 0).UTC()

	messageActor := newTGUser(42, "alice", "Alice", "User", false)
	memberActor := newTGUser(99, "bob", "Bob", "Member", false)

	tests := []struct {
		name         string
		raw          any
		wantAccepted bool
		wantType     UpdateType
		assert       func(t *testing.T, got Update)
	}{
		{
			name: "message created",
			raw: gotdUpdateEnvelope{
				update: &tg.UpdateNewMessage{
					Message: &tg.Message{
						ID:      777,
						PeerID:  &tg.PeerChat{ChatID: 100},
						Date:    1_700_000_000,
						Message: "hello",
						FromID:  &tg.PeerUser{UserID: 42},
					},
				},
				occurredAt: occurredAt,
				usersByID: map[int64]*tg.User{
					42: messageActor,
				},
				chatsByID: map[int64]gotdChatInfo{
					100: {title: "group-chat", kind: otogi.ConversationTypeGroup},
				},
				updateClass: "updateNewMessage",
			},
			wantAccepted: true,
			wantType:     UpdateTypeMessage,
			assert: func(t *testing.T, got Update) {
				t.Helper()
				if got.Message == nil {
					t.Fatal("expected message payload")
				}
				if got.Chat.ID != "100" {
					t.Fatalf("chat id = %s, want 100", got.Chat.ID)
				}
				if got.Actor.ID != "42" {
					t.Fatalf("actor id = %s, want 42", got.Actor.ID)
				}
				if got.Message.ID != "777" {
					t.Fatalf("message id = %s, want 777", got.Message.ID)
				}
				if got.Message.Text != "hello" {
					t.Fatalf("message text = %s, want hello", got.Message.Text)
				}
			},
		},
		{
			name: "message edited",
			raw: gotdUpdateEnvelope{
				update: &tg.UpdateEditMessage{
					Message: &tg.Message{
						ID:      888,
						PeerID:  &tg.PeerChat{ChatID: 100},
						Date:    1_700_000_001,
						Message: "updated",
						FromID:  &tg.PeerUser{UserID: 42},
					},
				},
				occurredAt: occurredAt,
				usersByID: map[int64]*tg.User{
					42: messageActor,
				},
				chatsByID: map[int64]gotdChatInfo{
					100: {title: "group-chat", kind: otogi.ConversationTypeGroup},
				},
				updateClass: "updateEditMessage",
			},
			wantAccepted: true,
			wantType:     UpdateTypeEdit,
			assert: func(t *testing.T, got Update) {
				t.Helper()
				if got.Edit == nil {
					t.Fatal("expected edit payload")
				}
				if got.Edit.MessageID != "888" {
					t.Fatalf("edit message id = %s, want 888", got.Edit.MessageID)
				}
				if got.Edit.After == nil {
					t.Fatal("expected edit after snapshot")
				}
				if got.Edit.After.Text != "updated" {
					t.Fatalf("edit after text = %s, want updated", got.Edit.After.Text)
				}
			},
		},
		{
			name: "member joined via participant add",
			raw: gotdUpdateEnvelope{
				update: &tg.UpdateChatParticipantAdd{
					ChatID:    100,
					UserID:    99,
					InviterID: 42,
					Date:      1_700_000_002,
					Version:   1,
				},
				occurredAt: occurredAt,
				usersByID: map[int64]*tg.User{
					42: messageActor,
					99: memberActor,
				},
				chatsByID: map[int64]gotdChatInfo{
					100: {title: "group-chat", kind: otogi.ConversationTypeGroup},
				},
				updateClass: "updateChatParticipantAdd",
			},
			wantAccepted: true,
			wantType:     UpdateTypeMemberJoin,
			assert: func(t *testing.T, got Update) {
				t.Helper()
				if got.Member == nil {
					t.Fatal("expected member payload")
				}
				if got.Member.Member.ID != "99" {
					t.Fatalf("member id = %s, want 99", got.Member.Member.ID)
				}
				if got.Member.Inviter == nil {
					t.Fatal("expected inviter")
				}
				if got.Member.Inviter.ID != "42" {
					t.Fatalf("inviter id = %s, want 42", got.Member.Inviter.ID)
				}
			},
		},
		{
			name: "chat migration via service message",
			raw: gotdUpdateEnvelope{
				update: &tg.UpdateNewMessage{
					Message: &tg.MessageService{
						PeerID: &tg.PeerChat{ChatID: 100},
						FromID: &tg.PeerUser{UserID: 42},
						Date:   1_700_000_003,
						Action: &tg.MessageActionChatMigrateTo{
							ChannelID: 500,
						},
					},
				},
				occurredAt: occurredAt,
				usersByID: map[int64]*tg.User{
					42: messageActor,
				},
				chatsByID: map[int64]gotdChatInfo{
					100: {title: "group-chat", kind: otogi.ConversationTypeGroup},
					500: {title: "supergroup", kind: otogi.ConversationTypeGroup},
				},
				updateClass: "updateNewMessage",
			},
			wantAccepted: true,
			wantType:     UpdateTypeMigration,
			assert: func(t *testing.T, got Update) {
				t.Helper()
				if got.Migration == nil {
					t.Fatal("expected migration payload")
				}
				if got.Migration.FromChatID != "100" {
					t.Fatalf("from chat = %s, want 100", got.Migration.FromChatID)
				}
				if got.Migration.ToChatID != "500" {
					t.Fatalf("to chat = %s, want 500", got.Migration.ToChatID)
				}
			},
		},
		{
			name: "reaction add delta",
			raw: gotdUpdateEnvelope{
				update:     &tg.UpdateBotMessageReaction{},
				occurredAt: occurredAt,
				usersByID: map[int64]*tg.User{
					42: messageActor,
				},
				chatsByID: map[int64]gotdChatInfo{
					500: {title: "channel", kind: otogi.ConversationTypeChannel},
				},
				updateClass: "updateBotMessageReaction",
				reaction: &gotdReactionDelta{
					action:    UpdateTypeReactionAdd,
					messageID: 700,
					emoji:     "👍",
					actor:     &tg.PeerUser{UserID: 42},
					peer:      &tg.PeerChannel{ChannelID: 500},
				},
			},
			wantAccepted: true,
			wantType:     UpdateTypeReactionAdd,
			assert: func(t *testing.T, got Update) {
				t.Helper()
				if got.Reaction == nil {
					t.Fatal("expected reaction payload")
				}
				if got.Reaction.Emoji != "👍" {
					t.Fatalf("emoji = %s, want 👍", got.Reaction.Emoji)
				}
				if got.Reaction.MessageID != "700" {
					t.Fatalf("message id = %s, want 700", got.Reaction.MessageID)
				}
				if got.Chat.ID != "500" {
					t.Fatalf("chat id = %s, want 500", got.Chat.ID)
				}
			},
		},
		{
			name: "unsupported update skipped",
			raw: gotdUpdateEnvelope{
				update:      &tg.UpdatePtsChanged{},
				occurredAt:  occurredAt,
				updateClass: "updatePtsChanged",
			},
			wantAccepted: false,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			got, accepted, err := mapper.Map(context.Background(), testCase.raw)
			if err != nil {
				t.Fatalf("map failed: %v", err)
			}
			if accepted != testCase.wantAccepted {
				t.Fatalf("accepted = %v, want %v", accepted, testCase.wantAccepted)
			}
			if !testCase.wantAccepted {
				return
			}
			if got.Type != testCase.wantType {
				t.Fatalf("type = %s, want %s", got.Type, testCase.wantType)
			}
			if testCase.assert != nil {
				testCase.assert(t, got)
			}
		})
	}
}

func TestDefaultGotdUpdateMapperMapContextCancel(t *testing.T) {
	t.Parallel()

	mapper := NewDefaultGotdUpdateMapper()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, accepted, err := mapper.Map(ctx, gotdUpdateEnvelope{
		update: &tg.UpdateNewMessage{
			Message: &tg.Message{
				ID:      1,
				PeerID:  &tg.PeerUser{UserID: 1},
				Date:    1,
				Message: "ignored",
			},
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", err)
	}
	if accepted {
		t.Fatal("expected accepted=false on canceled context")
	}
}

func TestDefaultGotdUpdateMapperMapRecordsPeerCache(t *testing.T) {
	t.Parallel()

	cache := NewPeerCache()
	mapper := NewDefaultGotdUpdateMapper(WithPeerCache(cache))

	occurredAt := time.Unix(1_700_000_000, 0).UTC()
	_, accepted, err := mapper.Map(context.Background(), gotdUpdateEnvelope{
		update: &tg.UpdateNewMessage{
			Message: &tg.Message{
				ID:      123,
				PeerID:  &tg.PeerChat{ChatID: 100},
				FromID:  &tg.PeerUser{UserID: 42},
				Date:    1_700_000_000,
				Message: "hello",
			},
		},
		occurredAt: occurredAt,
		usersByID: map[int64]*tg.User{
			42: newTGUser(42, "alice", "Alice", "User", false),
		},
		chatsByID: map[int64]gotdChatInfo{
			100: {
				title:     "group-chat",
				kind:      otogi.ConversationTypeGroup,
				inputPeer: &tg.InputPeerChat{ChatID: 100},
			},
		},
		updateClass: "updateNewMessage",
	})
	if err != nil {
		t.Fatalf("map failed: %v", err)
	}
	if !accepted {
		t.Fatal("expected accepted update")
	}

	peer, err := cache.Resolve(otogi.Conversation{
		ID:   "100",
		Type: otogi.ConversationTypeGroup,
	})
	if err != nil {
		t.Fatalf("resolve cached peer failed: %v", err)
	}
	if got := typeName(peer); got != "*tg.InputPeerChat" {
		t.Fatalf("peer type = %s, want *tg.InputPeerChat", got)
	}
}

func newTGUser(id int64, username, firstName, lastName string, isBot bool) *tg.User {
	user := &tg.User{ID: id}
	user.Bot = isBot
	if username != "" {
		user.SetUsername(username)
	}
	if firstName != "" {
		user.SetFirstName(firstName)
	}
	if lastName != "" {
		user.SetLastName(lastName)
	}
	return user
}
