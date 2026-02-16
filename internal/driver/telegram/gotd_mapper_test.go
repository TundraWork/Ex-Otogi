package telegram

import (
	"context"
	"errors"
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
				if len(got.Message.Reactions) != 1 {
					t.Fatalf("reactions length = %d, want 1", len(got.Message.Reactions))
				}
				if got.Message.Reactions[0].Emoji != "❤️" || got.Message.Reactions[0].Count != 2 {
					t.Fatalf("reactions[0] = %+v, want {Emoji:❤️ Count:2}", got.Message.Reactions[0])
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
				if got.Message == nil {
					t.Fatal("expected message payload")
				}
				if got.Message.ID != "888" {
					t.Fatalf("message id = %s, want 888", got.Message.ID)
				}
				if got.Message.Text != "updated" {
					t.Fatalf("message text = %s, want updated", got.Message.Text)
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
				if got.Edit.MessageID != "888" {
					t.Fatalf("edit message id = %s, want 888", got.Edit.MessageID)
				}
				if got.Edit.After == nil {
					t.Fatal("expected edit after snapshot")
				}
				if got.Edit.After.Text != "updated" {
					t.Fatalf("edit after text = %s, want updated", got.Edit.After.Text)
				}
				if len(got.ID) < len("tg:message.edited:") || got.ID[:len("tg:message.edited:")] != "tg:message.edited:" {
					t.Fatalf("id = %s, want prefix tg:message.edited:", got.ID)
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
				if got.Reaction.MessageID != "889" {
					t.Fatalf("reaction message id = %s, want 889", got.Reaction.MessageID)
				}
				if got.Reaction.Emoji != "❤️" {
					t.Fatalf("reaction emoji = %s, want ❤️", got.Reaction.Emoji)
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
				if got.Reaction.MessageID != "890" {
					t.Fatalf("reaction message id = %s, want 890", got.Reaction.MessageID)
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
				if got.Reaction.MessageID != "891" {
					t.Fatalf("reaction message id = %s, want 891", got.Reaction.MessageID)
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
				if got.Reaction.MessageID != "893" {
					t.Fatalf("reaction message id = %s, want 893", got.Reaction.MessageID)
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
	if got.Reaction.MessageID != "777" {
		t.Fatalf("reaction message id = %s, want 777", got.Reaction.MessageID)
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
	if !strings.HasPrefix(got.ID, "tg:message.edited:") {
		t.Fatalf("id = %s, want prefix tg:message.edited:", got.ID)
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
	if got.Reaction.MessageID != "777" {
		t.Fatalf("reaction message id = %s, want 777", got.Reaction.MessageID)
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

func TestComposeUpdateIDUsesSemanticEventTokens(t *testing.T) {
	t.Parallel()

	tests := []struct {
		updateType UpdateType
		wantPrefix string
	}{
		{updateType: UpdateTypeMessage, wantPrefix: "tg:message.created:"},
		{updateType: UpdateTypeEdit, wantPrefix: "tg:message.edited:"},
		{updateType: UpdateTypeDelete, wantPrefix: "tg:message.retracted:"},
		{updateType: UpdateTypeReactionAdd, wantPrefix: "tg:reaction.added:"},
		{updateType: UpdateTypeReactionRemove, wantPrefix: "tg:reaction.removed:"},
		{updateType: UpdateTypeMemberJoin, wantPrefix: "tg:member.joined:"},
		{updateType: UpdateTypeMemberLeave, wantPrefix: "tg:member.left:"},
		{updateType: UpdateTypeRole, wantPrefix: "tg:role.updated:"},
		{updateType: UpdateTypeMigration, wantPrefix: "tg:chat.migrated:"},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(string(testCase.updateType), func(t *testing.T) {
			t.Parallel()

			got := composeUpdateID(testCase.updateType, "100", "777", time.Unix(1_700_000_000, 0).UTC())
			if !strings.HasPrefix(got, testCase.wantPrefix) {
				t.Fatalf("composeUpdateID(%s) = %s, want prefix %s", testCase.updateType, got, testCase.wantPrefix)
			}
		})
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
