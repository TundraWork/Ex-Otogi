package telegram

import (
	"context"
	"errors"
	"strconv"
	"strings"
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
					Message: func() tg.MessageClass {
						message := &tg.Message{
							ID:      777,
							PeerID:  &tg.PeerChat{ChatID: 100},
							Date:    1_700_000_000,
							Message: "hello",
							FromID:  &tg.PeerUser{UserID: 42},
						}
						message.SetReactions(tg.MessageReactions{
							Results: []tg.ReactionCount{
								{
									Reaction: &tg.ReactionEmoji{Emoticon: "❤️"},
									Count:    2,
								},
							},
						})

						return message
					}(),
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
				if got.Article == nil {
					t.Fatal("expected message payload")
				}
				if got.Chat.ID != "100" {
					t.Fatalf("chat id = %s, want 100", got.Chat.ID)
				}
				if got.Actor.ID != "42" {
					t.Fatalf("actor id = %s, want 42", got.Actor.ID)
				}
				if got.Article.ID != "777" {
					t.Fatalf("message id = %s, want 777", got.Article.ID)
				}
				if got.Article.Text != "hello" {
					t.Fatalf("message text = %s, want hello", got.Article.Text)
				}
				if len(got.Article.Reactions) != 1 {
					t.Fatalf("reactions length = %d, want 1", len(got.Article.Reactions))
				}
				if got.Article.Reactions[0].Emoji != "❤️" || got.Article.Reactions[0].Count != 2 {
					t.Fatalf("reactions[0] = %+v, want {Emoji:❤️ Count:2}", got.Article.Reactions[0])
				}
			},
		},
		{
			name: "message edited",
			raw: gotdUpdateEnvelope{
				update: &tg.UpdateEditMessage{
					Message: func() tg.MessageClass {
						message := &tg.Message{
							ID:      888,
							PeerID:  &tg.PeerChat{ChatID: 100},
							Date:    1_700_000_001,
							Message: "updated",
							FromID:  &tg.PeerUser{UserID: 42},
						}
						message.SetEditDate(1_700_000_111)

						return message
					}(),
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
				if got.Article == nil {
					t.Fatal("expected message payload")
				}
				if got.Article.ID != "888" {
					t.Fatalf("message id = %s, want 888", got.Article.ID)
				}
				if got.Article.Text != "updated" {
					t.Fatalf("message text = %s, want updated", got.Article.Text)
				}
				if got.Edit == nil {
					t.Fatal("expected edit payload")
				}
				if !got.OccurredAt.Equal(time.Unix(1_700_000_111, 0).UTC()) {
					t.Fatalf("occurred_at = %v, want %v", got.OccurredAt, time.Unix(1_700_000_111, 0).UTC())
				}
				if got.Edit.ChangedAt == nil {
					t.Fatal("expected edit changed_at")
				}
				if !got.Edit.ChangedAt.Equal(time.Unix(1_700_000_111, 0).UTC()) {
					t.Fatalf("changed_at = %v, want %v", got.Edit.ChangedAt, time.Unix(1_700_000_111, 0).UTC())
				}
				if got.Edit.ArticleID != "888" {
					t.Fatalf("edit message id = %s, want 888", got.Edit.ArticleID)
				}
				if got.Edit.After == nil {
					t.Fatal("expected edit after snapshot")
				}
				if got.Edit.After.Text != "updated" {
					t.Fatalf("edit after text = %s, want updated", got.Edit.After.Text)
				}
				if len(got.ID) < len("tg:article.edited:") || got.ID[:len("tg:article.edited:")] != "tg:article.edited:" {
					t.Fatalf("id = %s, want prefix tg:article.edited:", got.ID)
				}
			},
		},
		{
			name: "reaction add surfaced from edit channel message",
			raw: gotdUpdateEnvelope{
				update: &tg.UpdateEditChannelMessage{
					Message: func() tg.MessageClass {
						message := &tg.Message{
							ID:      889,
							PeerID:  &tg.PeerChannel{ChannelID: 500},
							Date:    1_700_000_004,
							Message: "no text edit",
							FromID:  &tg.PeerUser{UserID: 42},
						}
						message.EditHide = true
						message.SetEditDate(1_700_000_222)
						reactions := tg.MessageReactions{
							Results: []tg.ReactionCount{},
						}
						reactions.SetRecentReactions([]tg.MessagePeerReaction{
							{
								My:       true,
								PeerID:   &tg.PeerUser{UserID: 42},
								Reaction: &tg.ReactionEmoji{Emoticon: "❤️"},
								Date:     1_700_000_004,
							},
						})
						message.SetReactions(reactions)

						return message
					}(),
				},
				occurredAt: occurredAt,
				usersByID: map[int64]*tg.User{
					42: messageActor,
				},
				chatsByID: map[int64]gotdChatInfo{
					500: {title: "channel", kind: otogi.ConversationTypeChannel},
				},
				updateClass: "updateEditChannelMessage",
			},
			wantAccepted: true,
			wantType:     UpdateTypeReactionAdd,
			assert: func(t *testing.T, got Update) {
				t.Helper()
				if got.Reaction == nil {
					t.Fatal("expected reaction payload")
				}
				if got.Reaction.ArticleID != "889" {
					t.Fatalf("reaction message id = %s, want 889", got.Reaction.ArticleID)
				}
				if got.Reaction.Emoji != "❤️" {
					t.Fatalf("reaction emoji = %s, want ❤️", got.Reaction.Emoji)
				}
				assertEventIDSegments(
					t,
					got.ID,
					"article.reaction.added",
					"500",
					"889",
					time.Unix(1_700_000_222, 0).UTC(),
				)
				if strings.Contains(got.ID, "❤️") {
					t.Fatalf("reaction id = %s, should not contain emoji", got.ID)
				}
			},
		},
		{
			name: "reaction add surfaced from edit channel message chosen results fallback",
			raw: gotdUpdateEnvelope{
				update: &tg.UpdateEditChannelMessage{
					Message: func() tg.MessageClass {
						message := &tg.Message{
							ID:      890,
							PeerID:  &tg.PeerChannel{ChannelID: 500},
							Date:    1_700_000_005,
							Message: "no text edit",
						}
						message.EditHide = true
						message.SetEditDate(1_700_000_223)
						chosen := tg.ReactionCount{
							Reaction: &tg.ReactionEmoji{Emoticon: "❤️"},
							Count:    1,
						}
						chosen.SetChosenOrder(1)
						reactions := tg.MessageReactions{
							Results: []tg.ReactionCount{chosen},
						}
						message.SetReactions(reactions)

						return message
					}(),
				},
				occurredAt: occurredAt,
				chatsByID: map[int64]gotdChatInfo{
					500: {title: "channel", kind: otogi.ConversationTypeChannel},
				},
				updateClass: "updateEditChannelMessage",
			},
			wantAccepted: true,
			wantType:     UpdateTypeReactionAdd,
			assert: func(t *testing.T, got Update) {
				t.Helper()
				if got.Reaction == nil {
					t.Fatal("expected reaction payload")
				}
				if got.Reaction.ArticleID != "890" {
					t.Fatalf("reaction message id = %s, want 890", got.Reaction.ArticleID)
				}
				if got.Reaction.Emoji != "❤️" {
					t.Fatalf("reaction emoji = %s, want ❤️", got.Reaction.Emoji)
				}
			},
		},
		{
			name: "reaction add surfaced from edit channel message recent reactions fallback",
			raw: gotdUpdateEnvelope{
				update: &tg.UpdateEditChannelMessage{
					Message: func() tg.MessageClass {
						message := &tg.Message{
							ID:      891,
							PeerID:  &tg.PeerChannel{ChannelID: 500},
							Date:    1_700_000_006,
							Message: "no text edit",
							FromID:  &tg.PeerUser{UserID: 42},
						}
						message.EditHide = true
						message.SetEditDate(1_700_000_224)
						reactions := tg.MessageReactions{
							Results: []tg.ReactionCount{},
						}
						reactions.SetRecentReactions([]tg.MessagePeerReaction{
							{
								PeerID:   &tg.PeerUser{UserID: 99},
								Reaction: &tg.ReactionEmoji{Emoticon: "❤️"},
								Date:     1_700_000_224,
							},
						})
						message.SetReactions(reactions)

						return message
					}(),
				},
				occurredAt: occurredAt,
				usersByID: map[int64]*tg.User{
					42: messageActor,
					99: memberActor,
				},
				chatsByID: map[int64]gotdChatInfo{
					500: {title: "channel", kind: otogi.ConversationTypeChannel},
				},
				updateClass: "updateEditChannelMessage",
			},
			wantAccepted: true,
			wantType:     UpdateTypeReactionAdd,
			assert: func(t *testing.T, got Update) {
				t.Helper()
				if got.Reaction == nil {
					t.Fatal("expected reaction payload")
				}
				if got.Reaction.ArticleID != "891" {
					t.Fatalf("reaction message id = %s, want 891", got.Reaction.ArticleID)
				}
				if got.Reaction.Emoji != "❤️" {
					t.Fatalf("reaction emoji = %s, want ❤️", got.Reaction.Emoji)
				}
				if got.Actor.ID != "99" {
					t.Fatalf("actor id = %s, want 99", got.Actor.ID)
				}
			},
		},
		{
			name: "reaction add surfaced from edit channel message single result fallback",
			raw: gotdUpdateEnvelope{
				update: &tg.UpdateEditChannelMessage{
					Message: func() tg.MessageClass {
						message := &tg.Message{
							ID:      893,
							PeerID:  &tg.PeerChannel{ChannelID: 500},
							Date:    1_700_000_008,
							Message: "no text edit",
						}
						message.EditHide = true
						message.SetEditDate(1_700_000_226)
						reactions := tg.MessageReactions{
							Results: []tg.ReactionCount{
								{
									Reaction: &tg.ReactionEmoji{Emoticon: "❤️"},
									Count:    1,
								},
							},
						}
						message.SetReactions(reactions)

						return message
					}(),
				},
				occurredAt: occurredAt,
				chatsByID: map[int64]gotdChatInfo{
					500: {title: "channel", kind: otogi.ConversationTypeChannel},
				},
				updateClass: "updateEditChannelMessage",
			},
			wantAccepted: true,
			wantType:     UpdateTypeReactionAdd,
			assert: func(t *testing.T, got Update) {
				t.Helper()
				if got.Reaction == nil {
					t.Fatal("expected reaction payload")
				}
				if got.Reaction.ArticleID != "893" {
					t.Fatalf("reaction message id = %s, want 893", got.Reaction.ArticleID)
				}
				if got.Reaction.Emoji != "❤️" {
					t.Fatalf("reaction emoji = %s, want ❤️", got.Reaction.Emoji)
				}
			},
		},
		{
			name: "message edit with reactions keeps edit semantics",
			raw: gotdUpdateEnvelope{
				update: &tg.UpdateEditMessage{
					Message: func() tg.MessageClass {
						message := &tg.Message{
							ID:      892,
							PeerID:  &tg.PeerChat{ChatID: 100},
							Date:    1_700_000_007,
							Message: "edited",
							FromID:  &tg.PeerUser{UserID: 42},
						}
						message.SetEditDate(1_700_000_225)
						reactions := tg.MessageReactions{
							Results: []tg.ReactionCount{},
						}
						reactions.SetRecentReactions([]tg.MessagePeerReaction{
							{
								PeerID:   &tg.PeerUser{UserID: 99},
								Reaction: &tg.ReactionEmoji{Emoticon: "❤️"},
								Date:     1_700_000_224,
							},
						})
						message.SetReactions(reactions)

						return message
					}(),
				},
				occurredAt: occurredAt,
				usersByID: map[int64]*tg.User{
					42: messageActor,
					99: memberActor,
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
				if got.Reaction != nil {
					t.Fatalf("reaction = %+v, want nil", got.Reaction)
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
				if got.Reaction.ArticleID != "700" {
					t.Fatalf("message id = %s, want 700", got.Reaction.ArticleID)
				}
				if got.Chat.ID != "500" {
					t.Fatalf("chat id = %s, want 500", got.Chat.ID)
				}
				assertEventIDSegments(
					t,
					got.ID,
					"article.reaction.added",
					"500",
					"700",
					occurredAt,
				)
				if strings.Contains(got.ID, "👍") {
					t.Fatalf("reaction id = %s, should not contain emoji", got.ID)
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

func TestDefaultGotdUpdateMapperMapInfersReactionDeltaFromCountsOnEdit(t *testing.T) {
	t.Parallel()

	mapper := NewDefaultGotdUpdateMapper()
	occurredAt := time.Unix(1_700_000_000, 0).UTC()

	initialMessage := &tg.Message{
		ID:      777,
		PeerID:  &tg.PeerChannel{ChannelID: 500},
		Date:    1_700_000_000,
		Message: "114514",
		FromID:  &tg.PeerUser{UserID: 42},
	}
	initialMessage.SetReactions(tg.MessageReactions{
		Results: []tg.ReactionCount{
			{
				Reaction: &tg.ReactionEmoji{Emoticon: "😀"},
				Count:    1,
			},
		},
	})

	_, accepted, err := mapper.Map(context.Background(), gotdUpdateEnvelope{
		update: &tg.UpdateNewChannelMessage{
			Message: initialMessage,
		},
		occurredAt: occurredAt,
		usersByID: map[int64]*tg.User{
			42: newTGUser(42, "alice", "Alice", "User", false),
		},
		chatsByID: map[int64]gotdChatInfo{
			500: {title: "channel", kind: otogi.ConversationTypeChannel},
		},
		updateClass: "updateNewChannelMessage",
	})
	if err != nil {
		t.Fatalf("map baseline message failed: %v", err)
	}
	if !accepted {
		t.Fatal("expected accepted baseline message")
	}

	reactionOnlyEdit := &tg.Message{
		ID:      777,
		PeerID:  &tg.PeerChannel{ChannelID: 500},
		Date:    1_700_000_000,
		Message: "114514",
		FromID:  &tg.PeerUser{UserID: 42},
	}
	reactionOnlyEdit.SetEditDate(1_700_000_100)
	reactionOnlyEdit.SetReactions(tg.MessageReactions{
		Results: []tg.ReactionCount{
			{
				Reaction: &tg.ReactionEmoji{Emoticon: "😀"},
				Count:    1,
			},
			{
				Reaction: &tg.ReactionEmoji{Emoticon: "❤️"},
				Count:    1,
			},
		},
	})

	got, accepted, err := mapper.Map(context.Background(), gotdUpdateEnvelope{
		update: &tg.UpdateEditChannelMessage{
			Message: reactionOnlyEdit,
		},
		occurredAt: occurredAt.Add(100 * time.Second),
		usersByID: map[int64]*tg.User{
			42: newTGUser(42, "alice", "Alice", "User", false),
		},
		chatsByID: map[int64]gotdChatInfo{
			500: {title: "channel", kind: otogi.ConversationTypeChannel},
		},
		updateClass: "updateEditChannelMessage",
	})
	if err != nil {
		t.Fatalf("map reaction-only edit failed: %v", err)
	}
	if !accepted {
		t.Fatal("expected accepted reaction-only edit")
	}
	if got.Type != UpdateTypeReactionAdd {
		t.Fatalf("type = %s, want %s", got.Type, UpdateTypeReactionAdd)
	}
	if got.Reaction == nil {
		t.Fatal("expected reaction payload")
	}
	if got.Reaction.ArticleID != "777" {
		t.Fatalf("reaction message id = %s, want 777", got.Reaction.ArticleID)
	}
	if got.Reaction.Emoji != "❤️" {
		t.Fatalf("reaction emoji = %s, want ❤️", got.Reaction.Emoji)
	}
	if got.Edit != nil {
		t.Fatalf("edit = %+v, want nil", got.Edit)
	}
}

func TestDefaultGotdUpdateMapperMapEditCarriesBeforeSnapshotFromCache(t *testing.T) {
	t.Parallel()

	mapper := NewDefaultGotdUpdateMapper()
	occurredAt := time.Unix(1_700_000_000, 0).UTC()

	_, accepted, err := mapper.Map(context.Background(), gotdUpdateEnvelope{
		update: &tg.UpdateNewMessage{
			Message: &tg.Message{
				ID:      901,
				PeerID:  &tg.PeerChat{ChatID: 100},
				Date:    1_700_000_000,
				Message: "before",
				FromID:  &tg.PeerUser{UserID: 42},
			},
		},
		occurredAt: occurredAt,
		usersByID: map[int64]*tg.User{
			42: newTGUser(42, "alice", "Alice", "User", false),
		},
		chatsByID: map[int64]gotdChatInfo{
			100: {title: "group-chat", kind: otogi.ConversationTypeGroup},
		},
		updateClass: "updateNewMessage",
	})
	if err != nil {
		t.Fatalf("map baseline message failed: %v", err)
	}
	if !accepted {
		t.Fatal("expected accepted baseline message")
	}

	edited := &tg.Message{
		ID:      901,
		PeerID:  &tg.PeerChat{ChatID: 100},
		Date:    1_700_000_050,
		Message: "after",
		FromID:  &tg.PeerUser{UserID: 42},
	}
	edited.SetEditDate(1_700_000_060)

	got, accepted, err := mapper.Map(context.Background(), gotdUpdateEnvelope{
		update: &tg.UpdateEditMessage{
			Message: edited,
		},
		occurredAt: occurredAt.Add(60 * time.Second),
		usersByID: map[int64]*tg.User{
			42: newTGUser(42, "alice", "Alice", "User", false),
		},
		chatsByID: map[int64]gotdChatInfo{
			100: {title: "group-chat", kind: otogi.ConversationTypeGroup},
		},
		updateClass: "updateEditMessage",
	})
	if err != nil {
		t.Fatalf("map edit message failed: %v", err)
	}
	if !accepted {
		t.Fatal("expected accepted edit message")
	}
	if got.Type != UpdateTypeEdit {
		t.Fatalf("type = %s, want %s", got.Type, UpdateTypeEdit)
	}
	if got.Edit == nil {
		t.Fatal("expected edit payload")
	}
	if got.Edit.Before == nil {
		t.Fatal("expected edit before snapshot")
	}
	if got.Edit.Before.Text != "before" {
		t.Fatalf("before text = %q, want before", got.Edit.Before.Text)
	}
	if got.Edit.After == nil {
		t.Fatal("expected edit after snapshot")
	}
	if got.Edit.After.Text != "after" {
		t.Fatalf("after text = %q, want after", got.Edit.After.Text)
	}
	if !strings.HasPrefix(got.ID, "tg:article.edited:") {
		t.Fatalf("id = %s, want prefix tg:article.edited:", got.ID)
	}
}

func TestDefaultGotdUpdateMapperMapEntityOnlyEditKeepsEditSemantics(t *testing.T) {
	t.Parallel()

	mapper := NewDefaultGotdUpdateMapper()
	occurredAt := time.Unix(1_700_000_000, 0).UTC()

	_, accepted, err := mapper.Map(context.Background(), gotdUpdateEnvelope{
		update: &tg.UpdateNewMessage{
			Message: &tg.Message{
				ID:       902,
				PeerID:   &tg.PeerChat{ChatID: 100},
				Date:     1_700_000_000,
				Message:  "hello",
				FromID:   &tg.PeerUser{UserID: 42},
				Entities: []tg.MessageEntityClass{&tg.MessageEntityBold{Offset: 0, Length: 5}},
			},
		},
		occurredAt: occurredAt,
		usersByID: map[int64]*tg.User{
			42: newTGUser(42, "alice", "Alice", "User", false),
		},
		chatsByID: map[int64]gotdChatInfo{
			100: {title: "group-chat", kind: otogi.ConversationTypeGroup},
		},
		updateClass: "updateNewMessage",
	})
	if err != nil {
		t.Fatalf("map baseline message failed: %v", err)
	}
	if !accepted {
		t.Fatal("expected accepted baseline message")
	}

	edited := &tg.Message{
		ID:       902,
		PeerID:   &tg.PeerChat{ChatID: 100},
		Date:     1_700_000_010,
		Message:  "hello",
		FromID:   &tg.PeerUser{UserID: 42},
		Entities: []tg.MessageEntityClass{&tg.MessageEntityItalic{Offset: 0, Length: 5}},
	}
	edited.SetEditDate(1_700_000_020)

	got, accepted, err := mapper.Map(context.Background(), gotdUpdateEnvelope{
		update: &tg.UpdateEditMessage{
			Message: edited,
		},
		occurredAt: occurredAt.Add(20 * time.Second),
		usersByID: map[int64]*tg.User{
			42: newTGUser(42, "alice", "Alice", "User", false),
		},
		chatsByID: map[int64]gotdChatInfo{
			100: {title: "group-chat", kind: otogi.ConversationTypeGroup},
		},
		updateClass: "updateEditMessage",
	})
	if err != nil {
		t.Fatalf("map entity-only edit failed: %v", err)
	}
	if !accepted {
		t.Fatal("expected accepted entity-only edit")
	}
	if got.Type != UpdateTypeEdit {
		t.Fatalf("type = %s, want %s", got.Type, UpdateTypeEdit)
	}
	if got.Reaction != nil {
		t.Fatalf("reaction = %+v, want nil", got.Reaction)
	}
	if got.Edit == nil {
		t.Fatal("expected edit payload")
	}
	if got.Edit.Before == nil || len(got.Edit.Before.Entities) != 1 {
		t.Fatalf("before entities = %+v, want 1 entity", got.Edit.Before)
	}
	if got.Edit.Before.Entities[0].Type != otogi.TextEntityTypeBold {
		t.Fatalf("before entity type = %s, want %s", got.Edit.Before.Entities[0].Type, otogi.TextEntityTypeBold)
	}
	if got.Edit.After == nil || len(got.Edit.After.Entities) != 1 {
		t.Fatalf("after entities = %+v, want 1 entity", got.Edit.After)
	}
	if got.Edit.After.Entities[0].Type != otogi.TextEntityTypeItalic {
		t.Fatalf("after entity type = %s, want %s", got.Edit.After.Entities[0].Type, otogi.TextEntityTypeItalic)
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

func TestDefaultGotdUpdateMapperMapResolvesReactionFromAPIOnAmbiguousEdit(t *testing.T) {
	t.Parallel()

	reactionResolver := &stubMessageReactionResolver{
		countsByMessage: map[int]map[string]int{
			777: {
				"❤️": 1,
			},
		},
	}
	mapper := NewDefaultGotdUpdateMapper(
		WithMessageReactionResolver(reactionResolver),
		WithReactionResolveTimeout(3*time.Second),
	)

	occurredAt := time.Unix(1_700_000_000, 0).UTC()
	chatInputPeer := &tg.InputPeerChannel{ChannelID: 500, AccessHash: 99}

	_, accepted, err := mapper.Map(context.Background(), gotdUpdateEnvelope{
		update: &tg.UpdateNewChannelMessage{
			Message: &tg.Message{
				ID:      777,
				PeerID:  &tg.PeerChannel{ChannelID: 500},
				Date:    1_700_000_000,
				Message: "test",
				FromID:  &tg.PeerUser{UserID: 42},
			},
		},
		occurredAt: occurredAt,
		usersByID: map[int64]*tg.User{
			42: newTGUser(42, "alice", "Alice", "User", false),
		},
		chatsByID: map[int64]gotdChatInfo{
			500: {
				title:     "channel",
				kind:      otogi.ConversationTypeChannel,
				inputPeer: chatInputPeer,
			},
		},
		updateClass: "updateNewChannelMessage",
	})
	if err != nil {
		t.Fatalf("map baseline message failed: %v", err)
	}
	if !accepted {
		t.Fatal("expected accepted baseline message")
	}

	editMessage := &tg.Message{
		ID:      777,
		PeerID:  &tg.PeerChannel{ChannelID: 500},
		Date:    1_700_000_000,
		Message: "test",
		FromID:  &tg.PeerUser{UserID: 42},
	}
	editMessage.EditHide = true
	editMessage.SetEditDate(1_700_000_050)
	editMessage.SetReactions(tg.MessageReactions{
		Results: []tg.ReactionCount{},
	})

	got, accepted, err := mapper.Map(context.Background(), gotdUpdateEnvelope{
		update: &tg.UpdateEditChannelMessage{
			Message: editMessage,
		},
		occurredAt: occurredAt.Add(50 * time.Second),
		usersByID: map[int64]*tg.User{
			42: newTGUser(42, "alice", "Alice", "User", false),
		},
		chatsByID: map[int64]gotdChatInfo{
			500: {
				title:     "channel",
				kind:      otogi.ConversationTypeChannel,
				inputPeer: chatInputPeer,
			},
		},
		updateClass: "updateEditChannelMessage",
	})
	if err != nil {
		t.Fatalf("map ambiguous edit failed: %v", err)
	}
	if !accepted {
		t.Fatal("expected accepted reaction update")
	}
	if got.Type != UpdateTypeReactionAdd {
		t.Fatalf("type = %s, want %s", got.Type, UpdateTypeReactionAdd)
	}
	if got.Reaction == nil {
		t.Fatal("expected reaction payload")
	}
	if got.Reaction.ArticleID != "777" {
		t.Fatalf("reaction message id = %s, want 777", got.Reaction.ArticleID)
	}
	if got.Reaction.Emoji != "❤️" {
		t.Fatalf("reaction emoji = %s, want ❤️", got.Reaction.Emoji)
	}
	if got.Edit != nil {
		t.Fatalf("edit = %+v, want nil", got.Edit)
	}
	if !reactionResolver.called {
		t.Fatal("expected resolver to be called")
	}
}

func TestDefaultGotdUpdateMapperMapSuppressesAmbiguousReactionEditWithoutSignal(t *testing.T) {
	t.Parallel()

	mapper := NewDefaultGotdUpdateMapper()
	occurredAt := time.Unix(1_700_000_000, 0).UTC()

	_, accepted, err := mapper.Map(context.Background(), gotdUpdateEnvelope{
		update: &tg.UpdateNewChannelMessage{
			Message: &tg.Message{
				ID:      778,
				PeerID:  &tg.PeerChannel{ChannelID: 500},
				Date:    1_700_000_000,
				Message: "test",
				FromID:  &tg.PeerUser{UserID: 42},
			},
		},
		occurredAt: occurredAt,
		usersByID: map[int64]*tg.User{
			42: newTGUser(42, "alice", "Alice", "User", false),
		},
		chatsByID: map[int64]gotdChatInfo{
			500: {title: "channel", kind: otogi.ConversationTypeChannel},
		},
		updateClass: "updateNewChannelMessage",
	})
	if err != nil {
		t.Fatalf("map baseline message failed: %v", err)
	}
	if !accepted {
		t.Fatal("expected accepted baseline message")
	}

	editMessage := &tg.Message{
		ID:      778,
		PeerID:  &tg.PeerChannel{ChannelID: 500},
		Date:    1_700_000_000,
		Message: "test",
		FromID:  &tg.PeerUser{UserID: 42},
	}
	editMessage.EditHide = true
	editMessage.SetEditDate(1_700_000_050)
	editMessage.SetReactions(tg.MessageReactions{
		Results: []tg.ReactionCount{},
	})

	_, accepted, err = mapper.Map(context.Background(), gotdUpdateEnvelope{
		update: &tg.UpdateEditChannelMessage{
			Message: editMessage,
		},
		occurredAt:  occurredAt.Add(50 * time.Second),
		updateClass: "updateEditChannelMessage",
		chatsByID: map[int64]gotdChatInfo{
			500: {title: "channel", kind: otogi.ConversationTypeChannel},
		},
	})
	if err != nil {
		t.Fatalf("map ambiguous edit failed: %v", err)
	}
	if accepted {
		t.Fatal("expected ambiguous reaction-like edit to be suppressed")
	}
}

func TestDefaultGotdUpdateMapperMapResolvesReactionAfterResolverRetry(t *testing.T) {
	t.Parallel()

	reactionResolver := &sequenceMessageReactionResolver{
		responses: []map[string]int{
			{},
			{"❤️": 1},
		},
	}
	mapper := NewDefaultGotdUpdateMapper(
		WithMessageReactionResolver(reactionResolver),
		WithReactionResolveTimeout(1*time.Second),
	)
	occurredAt := time.Unix(1_700_000_000, 0).UTC()

	_, accepted, err := mapper.Map(context.Background(), gotdUpdateEnvelope{
		update: &tg.UpdateNewChannelMessage{
			Message: &tg.Message{
				ID:      779,
				PeerID:  &tg.PeerChannel{ChannelID: 500},
				Date:    1_700_000_000,
				Message: "test",
				FromID:  &tg.PeerUser{UserID: 42},
			},
		},
		occurredAt: occurredAt,
		usersByID: map[int64]*tg.User{
			42: newTGUser(42, "alice", "Alice", "User", false),
		},
		chatsByID: map[int64]gotdChatInfo{
			500: {
				title:     "channel",
				kind:      otogi.ConversationTypeChannel,
				inputPeer: &tg.InputPeerChannel{ChannelID: 500, AccessHash: 99},
			},
		},
		updateClass: "updateNewChannelMessage",
	})
	if err != nil {
		t.Fatalf("map baseline message failed: %v", err)
	}
	if !accepted {
		t.Fatal("expected accepted baseline message")
	}

	editMessage := &tg.Message{
		ID:      779,
		PeerID:  &tg.PeerChannel{ChannelID: 500},
		Date:    1_700_000_000,
		Message: "test",
		FromID:  &tg.PeerUser{UserID: 42},
	}
	editMessage.EditHide = true
	editMessage.SetEditDate(1_700_000_050)
	editMessage.SetReactions(tg.MessageReactions{
		Results: []tg.ReactionCount{},
	})

	got, accepted, err := mapper.Map(context.Background(), gotdUpdateEnvelope{
		update: &tg.UpdateEditChannelMessage{
			Message: editMessage,
		},
		occurredAt:  occurredAt.Add(50 * time.Second),
		updateClass: "updateEditChannelMessage",
		chatsByID: map[int64]gotdChatInfo{
			500: {
				title:     "channel",
				kind:      otogi.ConversationTypeChannel,
				inputPeer: &tg.InputPeerChannel{ChannelID: 500, AccessHash: 99},
			},
		},
	})
	if err != nil {
		t.Fatalf("map ambiguous edit failed: %v", err)
	}
	if !accepted {
		t.Fatal("expected accepted reaction update after retry")
	}
	if got.Type != UpdateTypeReactionAdd {
		t.Fatalf("type = %s, want %s", got.Type, UpdateTypeReactionAdd)
	}
	if got.Reaction == nil || got.Reaction.Emoji != "❤️" {
		t.Fatalf("reaction = %+v, want ❤️", got.Reaction)
	}
	if reactionResolver.calls < 2 {
		t.Fatalf("resolver calls = %d, want >= 2", reactionResolver.calls)
	}
}

func TestDefaultGotdUpdateMapperMapResolvesReactionOnNonLikelyUnchangedEdit(t *testing.T) {
	t.Parallel()

	reactionResolver := &stubMessageReactionResolver{
		countsByMessage: map[int]map[string]int{
			781: {
				"❤️": 1,
			},
		},
	}
	mapper := NewDefaultGotdUpdateMapper(
		WithMessageReactionResolver(reactionResolver),
		WithReactionResolveTimeout(3*time.Second),
	)
	occurredAt := time.Unix(1_700_000_000, 0).UTC()
	chatInputPeer := &tg.InputPeerChannel{ChannelID: 500, AccessHash: 99}

	_, accepted, err := mapper.Map(context.Background(), gotdUpdateEnvelope{
		update: &tg.UpdateNewChannelMessage{
			Message: &tg.Message{
				ID:      781,
				PeerID:  &tg.PeerChannel{ChannelID: 500},
				Date:    1_700_000_000,
				Message: "before",
				FromID:  &tg.PeerUser{UserID: 42},
			},
		},
		occurredAt: occurredAt,
		usersByID: map[int64]*tg.User{
			42: newTGUser(42, "alice", "Alice", "User", false),
		},
		chatsByID: map[int64]gotdChatInfo{
			500: {
				title:     "channel",
				kind:      otogi.ConversationTypeChannel,
				inputPeer: chatInputPeer,
			},
		},
		updateClass: "updateNewChannelMessage",
	})
	if err != nil {
		t.Fatalf("map baseline message failed: %v", err)
	}
	if !accepted {
		t.Fatal("expected accepted baseline message")
	}

	firstEdit := &tg.Message{
		ID:      781,
		PeerID:  &tg.PeerChannel{ChannelID: 500},
		Date:    1_700_000_000,
		Message: "edited",
		FromID:  &tg.PeerUser{UserID: 42},
	}
	firstEdit.SetEditDate(1_700_000_050)

	first, accepted, err := mapper.Map(context.Background(), gotdUpdateEnvelope{
		update: &tg.UpdateEditChannelMessage{
			Message: firstEdit,
		},
		occurredAt: occurredAt.Add(50 * time.Second),
		usersByID: map[int64]*tg.User{
			42: newTGUser(42, "alice", "Alice", "User", false),
		},
		chatsByID: map[int64]gotdChatInfo{
			500: {
				title:     "channel",
				kind:      otogi.ConversationTypeChannel,
				inputPeer: chatInputPeer,
			},
		},
		updateClass: "updateEditChannelMessage",
	})
	if err != nil {
		t.Fatalf("map first edit failed: %v", err)
	}
	if !accepted {
		t.Fatal("expected accepted first text edit")
	}
	if first.Type != UpdateTypeEdit {
		t.Fatalf("type = %s, want %s", first.Type, UpdateTypeEdit)
	}

	secondEdit := &tg.Message{
		ID:      781,
		PeerID:  &tg.PeerChannel{ChannelID: 500},
		Date:    1_700_000_000,
		Message: "edited",
		FromID:  &tg.PeerUser{UserID: 42},
	}
	secondEdit.SetEditDate(1_700_000_070)

	second, accepted, err := mapper.Map(context.Background(), gotdUpdateEnvelope{
		update: &tg.UpdateEditChannelMessage{
			Message: secondEdit,
		},
		occurredAt: occurredAt.Add(70 * time.Second),
		usersByID: map[int64]*tg.User{
			42: newTGUser(42, "alice", "Alice", "User", false),
		},
		chatsByID: map[int64]gotdChatInfo{
			500: {
				title:     "channel",
				kind:      otogi.ConversationTypeChannel,
				inputPeer: chatInputPeer,
			},
		},
		updateClass: "updateEditChannelMessage",
	})
	if err != nil {
		t.Fatalf("map second edit failed: %v", err)
	}
	if !accepted {
		t.Fatal("expected accepted second update")
	}
	if second.Type != UpdateTypeReactionAdd {
		t.Fatalf("type = %s, want %s", second.Type, UpdateTypeReactionAdd)
	}
	if second.Reaction == nil || second.Reaction.Emoji != "❤️" {
		t.Fatalf("reaction = %+v, want ❤️", second.Reaction)
	}
	if reactionResolver.calls != 1 {
		t.Fatalf("resolver calls = %d, want 1", reactionResolver.calls)
	}
}

func TestDefaultGotdUpdateMapperMapResolvesReactionSequenceOnAmbiguousEdits(t *testing.T) {
	t.Parallel()

	reactionResolver := &mutableMessageReactionResolver{}
	mapper := NewDefaultGotdUpdateMapper(
		WithMessageReactionResolver(reactionResolver),
		WithReactionResolveTimeout(1*time.Second),
	)
	occurredAt := time.Unix(1_700_000_000, 0).UTC()
	chatInputPeer := &tg.InputPeerChannel{ChannelID: 500, AccessHash: 99}

	baseline := &tg.Message{
		ID:      782,
		PeerID:  &tg.PeerChannel{ChannelID: 500},
		Date:    1_700_000_000,
		Message: "test",
		FromID:  &tg.PeerUser{UserID: 42},
	}
	baseline.SetReactions(tg.MessageReactions{Results: []tg.ReactionCount{}})

	_, accepted, err := mapper.Map(context.Background(), gotdUpdateEnvelope{
		update: &tg.UpdateNewChannelMessage{
			Message: baseline,
		},
		occurredAt: occurredAt,
		usersByID: map[int64]*tg.User{
			42: newTGUser(42, "alice", "Alice", "User", false),
		},
		chatsByID: map[int64]gotdChatInfo{
			500: {
				title:     "channel",
				kind:      otogi.ConversationTypeChannel,
				inputPeer: chatInputPeer,
			},
		},
		updateClass: "updateNewChannelMessage",
	})
	if err != nil {
		t.Fatalf("map baseline message failed: %v", err)
	}
	if !accepted {
		t.Fatal("expected accepted baseline message")
	}

	mapAmbiguous := func(editDate int, counts map[string]int) (Update, bool, error) {
		reactionResolver.SetCounts(counts)

		message := &tg.Message{
			ID:      782,
			PeerID:  &tg.PeerChannel{ChannelID: 500},
			Date:    1_700_000_000,
			Message: "test",
			FromID:  &tg.PeerUser{UserID: 42},
		}
		message.EditHide = true
		message.SetEditDate(editDate)
		message.SetReactions(tg.MessageReactions{
			Results: []tg.ReactionCount{},
		})

		return mapper.Map(context.Background(), gotdUpdateEnvelope{
			update: &tg.UpdateEditChannelMessage{
				Message: message,
			},
			occurredAt: occurredAt.Add(time.Duration(editDate-1_700_000_000) * time.Second),
			usersByID: map[int64]*tg.User{
				42: newTGUser(42, "alice", "Alice", "User", false),
			},
			chatsByID: map[int64]gotdChatInfo{
				500: {
					title:     "channel",
					kind:      otogi.ConversationTypeChannel,
					inputPeer: chatInputPeer,
				},
			},
			updateClass: "updateEditChannelMessage",
		})
	}

	first, accepted, err := mapAmbiguous(1_700_000_050, map[string]int{"❤️": 1})
	if err != nil {
		t.Fatalf("map first ambiguous edit failed: %v", err)
	}
	if !accepted {
		t.Fatal("expected first ambiguous edit to map as reaction")
	}
	if first.Type != UpdateTypeReactionAdd {
		t.Fatalf("first type = %s, want %s", first.Type, UpdateTypeReactionAdd)
	}
	if first.Reaction == nil || first.Reaction.Emoji != "❤️" {
		t.Fatalf("first reaction = %+v, want add ❤️", first.Reaction)
	}

	second, accepted, err := mapAmbiguous(1_700_000_070, map[string]int{})
	if err != nil {
		t.Fatalf("map second ambiguous edit failed: %v", err)
	}
	if !accepted {
		t.Fatal("expected second ambiguous edit to map as reaction remove")
	}
	if second.Type != UpdateTypeReactionRemove {
		t.Fatalf("second type = %s, want %s", second.Type, UpdateTypeReactionRemove)
	}
	if second.Reaction == nil || second.Reaction.Emoji != "❤️" {
		t.Fatalf("second reaction = %+v, want remove ❤️", second.Reaction)
	}

	third, accepted, err := mapAmbiguous(1_700_000_090, map[string]int{"❤️": 1})
	if err != nil {
		t.Fatalf("map third ambiguous edit failed: %v", err)
	}
	if !accepted {
		t.Fatal("expected third ambiguous edit to map as reaction add")
	}
	if third.Type != UpdateTypeReactionAdd {
		t.Fatalf("third type = %s, want %s", third.Type, UpdateTypeReactionAdd)
	}
	if third.Reaction == nil || third.Reaction.Emoji != "❤️" {
		t.Fatalf("third reaction = %+v, want add ❤️", third.Reaction)
	}
}

func TestDefaultGotdUpdateMapperPollReactionUpdatesBackfillsMissingReactionEdits(t *testing.T) {
	t.Parallel()

	reactionResolver := &mutableMessageReactionResolver{}
	mapper := NewDefaultGotdUpdateMapper(
		WithMessageReactionResolver(reactionResolver),
		WithReactionResolveTimeout(1*time.Second),
	)
	occurredAt := time.Unix(1_700_000_000, 0).UTC()
	chatInputPeer := &tg.InputPeerChannel{ChannelID: 500, AccessHash: 99}

	baseline := &tg.Message{
		ID:      783,
		PeerID:  &tg.PeerChannel{ChannelID: 500},
		Date:    1_700_000_000,
		Message: "test",
		FromID:  &tg.PeerUser{UserID: 42},
	}
	baseline.SetReactions(tg.MessageReactions{Results: []tg.ReactionCount{}})

	_, accepted, err := mapper.Map(context.Background(), gotdUpdateEnvelope{
		update: &tg.UpdateNewChannelMessage{
			Message: baseline,
		},
		occurredAt: occurredAt,
		usersByID: map[int64]*tg.User{
			42: newTGUser(42, "alice", "Alice", "User", false),
		},
		chatsByID: map[int64]gotdChatInfo{
			500: {
				title:     "channel",
				kind:      otogi.ConversationTypeChannel,
				inputPeer: chatInputPeer,
			},
		},
		updateClass: "updateNewChannelMessage",
	})
	if err != nil {
		t.Fatalf("map baseline message failed: %v", err)
	}
	if !accepted {
		t.Fatal("expected accepted baseline message")
	}

	reactionResolver.SetCounts(map[string]int{"❤️": 1})
	ambiguous := &tg.Message{
		ID:      783,
		PeerID:  &tg.PeerChannel{ChannelID: 500},
		Date:    1_700_000_000,
		Message: "test",
		FromID:  &tg.PeerUser{UserID: 42},
	}
	ambiguous.EditHide = true
	ambiguous.SetEditDate(1_700_000_050)
	ambiguous.SetReactions(tg.MessageReactions{
		Results: []tg.ReactionCount{},
	})
	first, accepted, err := mapper.Map(context.Background(), gotdUpdateEnvelope{
		update: &tg.UpdateEditChannelMessage{
			Message: ambiguous,
		},
		occurredAt: occurredAt.Add(50 * time.Second),
		usersByID: map[int64]*tg.User{
			42: newTGUser(42, "alice", "Alice", "User", false),
		},
		chatsByID: map[int64]gotdChatInfo{
			500: {
				title:     "channel",
				kind:      otogi.ConversationTypeChannel,
				inputPeer: chatInputPeer,
			},
		},
		updateClass: "updateEditChannelMessage",
	})
	if err != nil {
		t.Fatalf("map ambiguous edit failed: %v", err)
	}
	if !accepted {
		t.Fatal("expected accepted reaction update")
	}
	if first.Type != UpdateTypeReactionAdd {
		t.Fatalf("first type = %s, want %s", first.Type, UpdateTypeReactionAdd)
	}

	reactionResolver.SetCounts(map[string]int{})
	polled, err := mapper.PollReactionUpdates(context.Background())
	if err != nil {
		t.Fatalf("poll reaction updates failed: %v", err)
	}
	if len(polled) != 1 {
		t.Fatalf("polled updates length = %d, want 1", len(polled))
	}
	if polled[0].Type != UpdateTypeReactionRemove {
		t.Fatalf("polled type = %s, want %s", polled[0].Type, UpdateTypeReactionRemove)
	}
	if polled[0].Reaction == nil || polled[0].Reaction.Emoji != "❤️" {
		t.Fatalf("polled reaction = %+v, want remove ❤️", polled[0].Reaction)
	}
	if polled[0].Metadata["gotd_update"] != "reaction_poll" {
		t.Fatalf("polled metadata gotd_update = %q, want reaction_poll", polled[0].Metadata["gotd_update"])
	}

	reactionResolver.SetCounts(map[string]int{"❤️": 1})
	polled, err = mapper.PollReactionUpdates(context.Background())
	if err != nil {
		t.Fatalf("poll reaction updates second pass failed: %v", err)
	}
	if len(polled) != 1 {
		t.Fatalf("second polled updates length = %d, want 1", len(polled))
	}
	if polled[0].Type != UpdateTypeReactionAdd {
		t.Fatalf("second polled type = %s, want %s", polled[0].Type, UpdateTypeReactionAdd)
	}
	if polled[0].Reaction == nil || polled[0].Reaction.Emoji != "❤️" {
		t.Fatalf("second polled reaction = %+v, want add ❤️", polled[0].Reaction)
	}
}

func TestDefaultGotdUpdateMapperMapPreservesBaselineAcrossAmbiguousLikelyEdit(t *testing.T) {
	t.Parallel()

	mapper := NewDefaultGotdUpdateMapper()
	occurredAt := time.Unix(1_700_000_000, 0).UTC()

	baseline := &tg.Message{
		ID:      780,
		PeerID:  &tg.PeerChannel{ChannelID: 500},
		Date:    1_700_000_000,
		Message: "test",
		FromID:  &tg.PeerUser{UserID: 42},
	}
	baseline.SetReactions(tg.MessageReactions{
		Results: []tg.ReactionCount{
			{
				Reaction: &tg.ReactionEmoji{Emoticon: "❤️"},
				Count:    1,
			},
			{
				Reaction: &tg.ReactionEmoji{Emoticon: "😀"},
				Count:    1,
			},
		},
	})

	_, accepted, err := mapper.Map(context.Background(), gotdUpdateEnvelope{
		update: &tg.UpdateNewChannelMessage{
			Message: baseline,
		},
		occurredAt: occurredAt,
		usersByID: map[int64]*tg.User{
			42: newTGUser(42, "alice", "Alice", "User", false),
		},
		chatsByID: map[int64]gotdChatInfo{
			500: {title: "channel", kind: otogi.ConversationTypeChannel},
		},
		updateClass: "updateNewChannelMessage",
	})
	if err != nil {
		t.Fatalf("map baseline message failed: %v", err)
	}
	if !accepted {
		t.Fatal("expected accepted baseline message")
	}

	ambiguous := &tg.Message{
		ID:      780,
		PeerID:  &tg.PeerChannel{ChannelID: 500},
		Date:    1_700_000_000,
		Message: "test",
		FromID:  &tg.PeerUser{UserID: 42},
	}
	ambiguous.EditHide = true
	ambiguous.SetEditDate(1_700_000_050)
	ambiguous.SetReactions(tg.MessageReactions{
		Results: []tg.ReactionCount{},
	})

	_, accepted, err = mapper.Map(context.Background(), gotdUpdateEnvelope{
		update: &tg.UpdateEditChannelMessage{
			Message: ambiguous,
		},
		occurredAt:  occurredAt.Add(50 * time.Second),
		updateClass: "updateEditChannelMessage",
		chatsByID: map[int64]gotdChatInfo{
			500: {title: "channel", kind: otogi.ConversationTypeChannel},
		},
	})
	if err != nil {
		t.Fatalf("map ambiguous edit failed: %v", err)
	}
	if accepted {
		t.Fatal("expected ambiguous reaction-like edit to be suppressed")
	}

	later := &tg.Message{
		ID:      780,
		PeerID:  &tg.PeerChannel{ChannelID: 500},
		Date:    1_700_000_000,
		Message: "test",
		FromID:  &tg.PeerUser{UserID: 42},
	}
	later.EditHide = true
	later.SetEditDate(1_700_000_080)
	later.SetReactions(tg.MessageReactions{
		Results: []tg.ReactionCount{
			{
				Reaction: &tg.ReactionEmoji{Emoticon: "❤️"},
				Count:    2,
			},
			{
				Reaction: &tg.ReactionEmoji{Emoticon: "😀"},
				Count:    1,
			},
		},
	})

	got, accepted, err := mapper.Map(context.Background(), gotdUpdateEnvelope{
		update: &tg.UpdateEditChannelMessage{
			Message: later,
		},
		occurredAt:  occurredAt.Add(80 * time.Second),
		updateClass: "updateEditChannelMessage",
		chatsByID: map[int64]gotdChatInfo{
			500: {title: "channel", kind: otogi.ConversationTypeChannel},
		},
	})
	if err != nil {
		t.Fatalf("map follow-up edit failed: %v", err)
	}
	if !accepted {
		t.Fatal("expected accepted reaction update")
	}
	if got.Type != UpdateTypeReactionAdd {
		t.Fatalf("type = %s, want %s", got.Type, UpdateTypeReactionAdd)
	}
	if got.Reaction == nil || got.Reaction.Emoji != "❤️" {
		t.Fatalf("reaction = %+v, want ❤️", got.Reaction)
	}
}

func TestDefaultGotdUpdateMapperMapBatchFromMessageReactionsRemovesReaction(t *testing.T) {
	t.Parallel()

	mapper := NewDefaultGotdUpdateMapper()
	occurredAt := time.Unix(1_700_000_000, 0).UTC()
	baseline := &tg.Message{
		ID:      777,
		PeerID:  &tg.PeerChannel{ChannelID: 500},
		Date:    1_700_000_000,
		Message: "baseline",
		FromID:  &tg.PeerUser{UserID: 42},
	}
	baseline.SetReactions(tg.MessageReactions{
		Results: []tg.ReactionCount{
			{
				Reaction: &tg.ReactionEmoji{Emoticon: "❤️"},
				Count:    1,
			},
		},
	})

	_, accepted, err := mapper.Map(context.Background(), gotdUpdateEnvelope{
		update: &tg.UpdateNewChannelMessage{
			Message: baseline,
		},
		occurredAt: occurredAt,
		usersByID: map[int64]*tg.User{
			42: newTGUser(42, "alice", "Alice", "User", false),
		},
		chatsByID: map[int64]gotdChatInfo{
			500: {title: "channel", kind: otogi.ConversationTypeChannel},
		},
		updateClass: "updateNewChannelMessage",
	})
	if err != nil {
		t.Fatalf("map baseline message failed: %v", err)
	}
	if !accepted {
		t.Fatal("expected accepted baseline message")
	}

	updates, err := mapper.MapBatch(context.Background(), gotdUpdateEnvelope{
		update: &tg.UpdateMessageReactions{
			Peer:  &tg.PeerChannel{ChannelID: 500},
			MsgID: 777,
			Reactions: tg.MessageReactions{
				Results: []tg.ReactionCount{},
			},
		},
		occurredAt: occurredAt.Add(10 * time.Second),
		chatsByID: map[int64]gotdChatInfo{
			500: {title: "channel", kind: otogi.ConversationTypeChannel},
		},
		updateClass: "updateMessageReactions",
	})
	if err != nil {
		t.Fatalf("map message reactions failed: %v", err)
	}
	if len(updates) != 1 {
		t.Fatalf("updates length = %d, want 1", len(updates))
	}
	if updates[0].Type != UpdateTypeReactionRemove {
		t.Fatalf("type = %s, want %s", updates[0].Type, UpdateTypeReactionRemove)
	}
	if updates[0].Reaction == nil {
		t.Fatal("expected reaction payload")
	}
	if updates[0].Reaction.ArticleID != "777" {
		t.Fatalf("reaction article id = %s, want 777", updates[0].Reaction.ArticleID)
	}
	if updates[0].Reaction.Emoji != "❤️" {
		t.Fatalf("reaction emoji = %s, want ❤️", updates[0].Reaction.Emoji)
	}
}

func TestDefaultGotdUpdateMapperMapBatchFromMessageReactionsSplitsAllDeltas(t *testing.T) {
	t.Parallel()

	mapper := NewDefaultGotdUpdateMapper()
	occurredAt := time.Unix(1_700_000_000, 0).UTC()
	baseline := &tg.Message{
		ID:      778,
		PeerID:  &tg.PeerChannel{ChannelID: 500},
		Date:    1_700_000_000,
		Message: "baseline",
		FromID:  &tg.PeerUser{UserID: 42},
	}
	baseline.SetReactions(tg.MessageReactions{
		Results: []tg.ReactionCount{
			{
				Reaction: &tg.ReactionEmoji{Emoticon: "❤️"},
				Count:    2,
			},
			{
				Reaction: &tg.ReactionEmoji{Emoticon: "👍"},
				Count:    1,
			},
		},
	})

	_, accepted, err := mapper.Map(context.Background(), gotdUpdateEnvelope{
		update: &tg.UpdateNewChannelMessage{
			Message: baseline,
		},
		occurredAt: occurredAt,
		usersByID: map[int64]*tg.User{
			42: newTGUser(42, "alice", "Alice", "User", false),
		},
		chatsByID: map[int64]gotdChatInfo{
			500: {title: "channel", kind: otogi.ConversationTypeChannel},
		},
		updateClass: "updateNewChannelMessage",
	})
	if err != nil {
		t.Fatalf("map baseline message failed: %v", err)
	}
	if !accepted {
		t.Fatal("expected accepted baseline message")
	}

	updates, err := mapper.MapBatch(context.Background(), gotdUpdateEnvelope{
		update: &tg.UpdateMessageReactions{
			Peer:  &tg.PeerChannel{ChannelID: 500},
			MsgID: 778,
			Reactions: tg.MessageReactions{
				Results: []tg.ReactionCount{
					{
						Reaction: &tg.ReactionEmoji{Emoticon: "❤️"},
						Count:    1,
					},
					{
						Reaction: &tg.ReactionEmoji{Emoticon: "😀"},
						Count:    2,
					},
				},
			},
		},
		occurredAt: occurredAt.Add(20 * time.Second),
		chatsByID: map[int64]gotdChatInfo{
			500: {title: "channel", kind: otogi.ConversationTypeChannel},
		},
		updateClass: "updateMessageReactions",
	})
	if err != nil {
		t.Fatalf("map message reactions failed: %v", err)
	}
	if len(updates) != 4 {
		t.Fatalf("updates length = %d, want 4", len(updates))
	}

	signatureCount := make(map[string]int, len(updates))
	ids := make(map[string]struct{}, len(updates))
	suffixedIDFound := false
	for _, update := range updates {
		if update.Reaction == nil {
			t.Fatalf("update reaction payload = nil, type=%s", update.Type)
		}
		signature := string(update.Type) + "|" + update.Reaction.Emoji
		signatureCount[signature]++

		if _, exists := ids[update.ID]; exists {
			t.Fatalf("duplicate id detected: %s", update.ID)
		}
		ids[update.ID] = struct{}{}
		if strings.Contains(update.ID, ":778~1:") {
			suffixedIDFound = true
		}
	}

	if signatureCount[string(UpdateTypeReactionRemove)+"|❤️"] != 1 {
		t.Fatalf("remove ❤️ count = %d, want 1", signatureCount[string(UpdateTypeReactionRemove)+"|❤️"])
	}
	if signatureCount[string(UpdateTypeReactionRemove)+"|👍"] != 1 {
		t.Fatalf("remove 👍 count = %d, want 1", signatureCount[string(UpdateTypeReactionRemove)+"|👍"])
	}
	if signatureCount[string(UpdateTypeReactionAdd)+"|😀"] != 2 {
		t.Fatalf("add 😀 count = %d, want 2", signatureCount[string(UpdateTypeReactionAdd)+"|😀"])
	}
	if !suffixedIDFound {
		t.Fatal("expected at least one deduplicated subject id suffix")
	}
}

func TestDefaultGotdUpdateMapperMapPrefersCountDiffOverHeuristicOnEditedMessage(t *testing.T) {
	t.Parallel()

	mapper := NewDefaultGotdUpdateMapper()
	occurredAt := time.Unix(1_700_000_000, 0).UTC()
	baseline := &tg.Message{
		ID:      779,
		PeerID:  &tg.PeerChannel{ChannelID: 500},
		Date:    1_700_000_000,
		Message: "baseline",
		FromID:  &tg.PeerUser{UserID: 42},
	}
	baseline.SetReactions(tg.MessageReactions{
		Results: []tg.ReactionCount{
			{
				Reaction: &tg.ReactionEmoji{Emoticon: "❤️"},
				Count:    1,
			},
			{
				Reaction: &tg.ReactionEmoji{Emoticon: "👍"},
				Count:    1,
			},
		},
	})

	_, accepted, err := mapper.Map(context.Background(), gotdUpdateEnvelope{
		update: &tg.UpdateNewChannelMessage{
			Message: baseline,
		},
		occurredAt: occurredAt,
		usersByID: map[int64]*tg.User{
			42: newTGUser(42, "alice", "Alice", "User", false),
		},
		chatsByID: map[int64]gotdChatInfo{
			500: {title: "channel", kind: otogi.ConversationTypeChannel},
		},
		updateClass: "updateNewChannelMessage",
	})
	if err != nil {
		t.Fatalf("map baseline message failed: %v", err)
	}
	if !accepted {
		t.Fatal("expected accepted baseline message")
	}

	edit := &tg.Message{
		ID:      779,
		PeerID:  &tg.PeerChannel{ChannelID: 500},
		Date:    1_700_000_000,
		Message: "baseline",
		FromID:  &tg.PeerUser{UserID: 42},
	}
	edit.EditHide = true
	edit.SetEditDate(1_700_000_050)
	editReactions := tg.MessageReactions{
		Results: []tg.ReactionCount{
			{
				Reaction: &tg.ReactionEmoji{Emoticon: "👍"},
				Count:    1,
			},
		},
	}
	editReactions.SetRecentReactions([]tg.MessagePeerReaction{
		{
			My:       true,
			PeerID:   &tg.PeerUser{UserID: 42},
			Reaction: &tg.ReactionEmoji{Emoticon: "❤️"},
			Date:     1_700_000_050,
		},
	})
	edit.SetReactions(editReactions)

	got, accepted, err := mapper.Map(context.Background(), gotdUpdateEnvelope{
		update: &tg.UpdateEditChannelMessage{
			Message: edit,
		},
		occurredAt: occurredAt.Add(50 * time.Second),
		usersByID: map[int64]*tg.User{
			42: newTGUser(42, "alice", "Alice", "User", false),
		},
		chatsByID: map[int64]gotdChatInfo{
			500: {title: "channel", kind: otogi.ConversationTypeChannel},
		},
		updateClass: "updateEditChannelMessage",
	})
	if err != nil {
		t.Fatalf("map edited message failed: %v", err)
	}
	if !accepted {
		t.Fatal("expected accepted reaction update")
	}
	if got.Type != UpdateTypeReactionRemove {
		t.Fatalf("type = %s, want %s", got.Type, UpdateTypeReactionRemove)
	}
	if got.Reaction == nil || got.Reaction.Emoji != "❤️" {
		t.Fatalf("reaction = %+v, want remove ❤️", got.Reaction)
	}
}

func TestDefaultGotdUpdateMapperMapBatchFanOutsServiceChatAddUser(t *testing.T) {
	t.Parallel()

	mapper := NewDefaultGotdUpdateMapper()
	occurredAt := time.Unix(1_700_000_000, 0).UTC()

	updates, err := mapper.MapBatch(context.Background(), gotdUpdateEnvelope{
		update: &tg.UpdateNewMessage{
			Message: &tg.MessageService{
				PeerID: &tg.PeerChat{ChatID: 100},
				FromID: &tg.PeerUser{UserID: 42},
				Date:   1_700_000_001,
				Action: &tg.MessageActionChatAddUser{
					Users: []int64{101, 102, 103},
				},
			},
		},
		occurredAt: occurredAt,
		usersByID: map[int64]*tg.User{
			42:  newTGUser(42, "alice", "Alice", "User", false),
			101: newTGUser(101, "u101", "User", "One", false),
			102: newTGUser(102, "u102", "User", "Two", false),
			103: newTGUser(103, "u103", "User", "Three", false),
		},
		chatsByID: map[int64]gotdChatInfo{
			100: {title: "group-chat", kind: otogi.ConversationTypeGroup},
		},
		updateClass: "updateNewMessage",
	})
	if err != nil {
		t.Fatalf("map service message failed: %v", err)
	}
	if len(updates) != 3 {
		t.Fatalf("updates length = %d, want 3", len(updates))
	}

	gotMembers := map[string]struct{}{}
	for _, update := range updates {
		if update.Type != UpdateTypeMemberJoin {
			t.Fatalf("update type = %s, want %s", update.Type, UpdateTypeMemberJoin)
		}
		if update.Member == nil {
			t.Fatal("expected member payload")
		}
		gotMembers[update.Member.Member.ID] = struct{}{}
	}
	for _, expected := range []string{"101", "102", "103"} {
		if _, ok := gotMembers[expected]; !ok {
			t.Fatalf("missing member id %s in fan-out updates", expected)
		}
	}
}

func TestDefaultGotdUpdateMapperMapBatchUsesHeuristicWhenNoBaseline(t *testing.T) {
	t.Parallel()

	mapper := NewDefaultGotdUpdateMapper()
	occurredAt := time.Unix(1_700_000_000, 0).UTC()

	reactions := tg.MessageReactions{
		Results: []tg.ReactionCount{},
	}
	reactions.SetRecentReactions([]tg.MessagePeerReaction{
		{
			My:       true,
			PeerID:   &tg.PeerUser{UserID: 42},
			Reaction: &tg.ReactionEmoji{Emoticon: "❤️"},
			Date:     1_700_000_100,
		},
	})

	updates, err := mapper.MapBatch(context.Background(), gotdUpdateEnvelope{
		update: &tg.UpdateMessageReactions{
			Peer:      &tg.PeerChannel{ChannelID: 500},
			MsgID:     900,
			Reactions: reactions,
		},
		occurredAt: occurredAt,
		usersByID: map[int64]*tg.User{
			42: newTGUser(42, "alice", "Alice", "User", false),
		},
		chatsByID: map[int64]gotdChatInfo{
			500: {title: "channel", kind: otogi.ConversationTypeChannel},
		},
		updateClass: "updateMessageReactions",
	})
	if err != nil {
		t.Fatalf("map message reactions failed: %v", err)
	}
	if len(updates) != 1 {
		t.Fatalf("updates length = %d, want 1", len(updates))
	}
	if updates[0].Type != UpdateTypeReactionAdd {
		t.Fatalf("type = %s, want %s", updates[0].Type, UpdateTypeReactionAdd)
	}
	if updates[0].Reaction == nil || updates[0].Reaction.Emoji != "❤️" {
		t.Fatalf("reaction = %+v, want add ❤️", updates[0].Reaction)
	}
}

func TestDefaultGotdUpdateMapperMapReturnsFirstFromBatch(t *testing.T) {
	t.Parallel()

	mapperBatch := NewDefaultGotdUpdateMapper()
	mapperSingle := NewDefaultGotdUpdateMapper()
	occurredAt := time.Unix(1_700_000_000, 0).UTC()

	baseline := &tg.Message{
		ID:      901,
		PeerID:  &tg.PeerChannel{ChannelID: 500},
		Date:    1_700_000_000,
		Message: "baseline",
		FromID:  &tg.PeerUser{UserID: 42},
	}
	baseline.SetReactions(tg.MessageReactions{
		Results: []tg.ReactionCount{
			{
				Reaction: &tg.ReactionEmoji{Emoticon: "😀"},
				Count:    2,
			},
			{
				Reaction: &tg.ReactionEmoji{Emoticon: "👍"},
				Count:    1,
			},
		},
	})
	baselineEnvelope := gotdUpdateEnvelope{
		update: &tg.UpdateNewChannelMessage{
			Message: baseline,
		},
		occurredAt: occurredAt,
		usersByID: map[int64]*tg.User{
			42: newTGUser(42, "alice", "Alice", "User", false),
		},
		chatsByID: map[int64]gotdChatInfo{
			500: {title: "channel", kind: otogi.ConversationTypeChannel},
		},
		updateClass: "updateNewChannelMessage",
	}

	if _, accepted, err := mapperBatch.Map(context.Background(), baselineEnvelope); err != nil {
		t.Fatalf("map batch baseline failed: %v", err)
	} else if !accepted {
		t.Fatal("expected accepted batch baseline message")
	}
	if _, accepted, err := mapperSingle.Map(context.Background(), baselineEnvelope); err != nil {
		t.Fatalf("map single baseline failed: %v", err)
	} else if !accepted {
		t.Fatal("expected accepted single baseline message")
	}

	reactionsEnvelope := gotdUpdateEnvelope{
		update: &tg.UpdateMessageReactions{
			Peer:  &tg.PeerChannel{ChannelID: 500},
			MsgID: 901,
			Reactions: tg.MessageReactions{
				Results: []tg.ReactionCount{
					{
						Reaction: &tg.ReactionEmoji{Emoticon: "😀"},
						Count:    1,
					},
				},
			},
		},
		occurredAt: occurredAt.Add(10 * time.Second),
		chatsByID: map[int64]gotdChatInfo{
			500: {title: "channel", kind: otogi.ConversationTypeChannel},
		},
		updateClass: "updateMessageReactions",
	}

	batchUpdates, err := mapperBatch.MapBatch(context.Background(), reactionsEnvelope)
	if err != nil {
		t.Fatalf("map batch failed: %v", err)
	}
	if len(batchUpdates) < 2 {
		t.Fatalf("batch updates length = %d, want >=2", len(batchUpdates))
	}

	singleUpdate, accepted, err := mapperSingle.Map(context.Background(), reactionsEnvelope)
	if err != nil {
		t.Fatalf("map single failed: %v", err)
	}
	if !accepted {
		t.Fatal("expected accepted single update")
	}

	if singleUpdate.Type != batchUpdates[0].Type {
		t.Fatalf("single type = %s, want %s", singleUpdate.Type, batchUpdates[0].Type)
	}
	if singleUpdate.Reaction == nil || batchUpdates[0].Reaction == nil {
		t.Fatalf("reaction payload mismatch: single=%+v batch0=%+v", singleUpdate.Reaction, batchUpdates[0].Reaction)
	}
	if singleUpdate.Reaction.ArticleID != batchUpdates[0].Reaction.ArticleID {
		t.Fatalf("single article id = %s, want %s", singleUpdate.Reaction.ArticleID, batchUpdates[0].Reaction.ArticleID)
	}
	if singleUpdate.Reaction.Emoji != batchUpdates[0].Reaction.Emoji {
		t.Fatalf("single emoji = %s, want %s", singleUpdate.Reaction.Emoji, batchUpdates[0].Reaction.Emoji)
	}
}

func TestComposeUpdateIDUsesSemanticEventTokens(t *testing.T) {
	t.Parallel()

	tests := []struct {
		updateType UpdateType
		wantToken  string
	}{
		{updateType: UpdateTypeMessage, wantToken: "article.created"},
		{updateType: UpdateTypeEdit, wantToken: "article.edited"},
		{updateType: UpdateTypeDelete, wantToken: "article.retracted"},
		{updateType: UpdateTypeReactionAdd, wantToken: "article.reaction.added"},
		{updateType: UpdateTypeReactionRemove, wantToken: "article.reaction.removed"},
		{updateType: UpdateTypeMemberJoin, wantToken: "member.joined"},
		{updateType: UpdateTypeMemberLeave, wantToken: "member.left"},
		{updateType: UpdateTypeRole, wantToken: "role.updated"},
		{updateType: UpdateTypeMigration, wantToken: "chat.migrated"},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(string(testCase.updateType), func(t *testing.T) {
			t.Parallel()

			occurredAt := time.Unix(1_700_000_000, 0).UTC()
			got := composeUpdateID(testCase.updateType, "100", "777", occurredAt)
			assertEventIDSegments(t, got, testCase.wantToken, "100", "777", occurredAt)
		})
	}
}

func assertEventIDSegments(
	t *testing.T,
	eventID string,
	wantToken string,
	wantChatID string,
	wantSubjectID string,
	wantOccurredAt time.Time,
) {
	t.Helper()

	parts := strings.Split(eventID, ":")
	if len(parts) != 5 {
		t.Fatalf("event id = %s, want 5 segments", eventID)
	}
	if parts[0] != "tg" {
		t.Fatalf("event id namespace = %s, want tg", parts[0])
	}
	if parts[1] != wantToken {
		t.Fatalf("event id token = %s, want %s", parts[1], wantToken)
	}
	if parts[2] != wantChatID {
		t.Fatalf("event id chat segment = %s, want %s", parts[2], wantChatID)
	}
	if parts[3] != wantSubjectID {
		t.Fatalf("event id subject segment = %s, want %s", parts[3], wantSubjectID)
	}
	if parts[4] != strconv.FormatInt(wantOccurredAt.UnixNano(), 10) {
		t.Fatalf(
			"event id timestamp segment = %s, want %d",
			parts[4],
			wantOccurredAt.UnixNano(),
		)
	}
}

type stubMessageReactionResolver struct {
	countsByMessage map[int]map[string]int
	err             error
	called          bool
	calls           int
}

func (s *stubMessageReactionResolver) ResolveMessageReactionCounts(
	_ context.Context,
	_ tg.InputPeerClass,
	messageID int,
) (map[string]int, error) {
	s.called = true
	s.calls++
	if s.err != nil {
		return nil, s.err
	}

	counts, ok := s.countsByMessage[messageID]
	if !ok {
		return nil, nil
	}

	return cloneReactionCounts(counts), nil
}

type sequenceMessageReactionResolver struct {
	responses []map[string]int
	calls     int
}

func (s *sequenceMessageReactionResolver) ResolveMessageReactionCounts(
	_ context.Context,
	_ tg.InputPeerClass,
	_ int,
) (map[string]int, error) {
	if len(s.responses) == 0 {
		s.calls++
		return nil, nil
	}

	idx := s.calls
	s.calls++
	if idx >= len(s.responses) {
		idx = len(s.responses) - 1
	}

	return cloneReactionCounts(s.responses[idx]), nil
}

type mutableMessageReactionResolver struct {
	counts map[string]int
	calls  int
}

func (s *mutableMessageReactionResolver) SetCounts(counts map[string]int) {
	s.counts = cloneReactionCounts(counts)
}

func (s *mutableMessageReactionResolver) ResolveMessageReactionCounts(
	_ context.Context,
	_ tg.InputPeerClass,
	_ int,
) (map[string]int, error) {
	s.calls++

	return cloneReactionCounts(s.counts), nil
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
