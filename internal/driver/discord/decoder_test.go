package discord

import (
	"context"
	"testing"
	"time"

	"ex-otogi/pkg/otogi/platform"
)

func TestDefaultDecoderDecodeMessageAndEditPayloads(t *testing.T) {
	t.Parallel()

	decoder := NewDefaultDecoder()
	occurredAt := time.Unix(1_700_000_000, 0).UTC()
	changedAt := time.Unix(1_700_000_111, 0).UTC()

	tests := []struct {
		name   string
		update Update
		assert func(t *testing.T, event *platform.Event)
	}{
		{
			name: "message update maps to article created",
			update: Update{
				ID:         "dc:article.created:100:777",
				Type:       UpdateTypeMessage,
				OccurredAt: occurredAt,
				Channel: ChannelRef{
					ID:      "100",
					GuildID: "guild1",
					Type:    platform.ConversationTypeGroup,
				},
				Actor: ActorRef{ID: "42", Username: "testuser"},
				Article: &ArticlePayload{
					ID:   "777",
					Text: "hello",
				},
			},
			assert: func(t *testing.T, event *platform.Event) {
				t.Helper()
				if event.Kind != platform.EventKindArticleCreated {
					t.Fatalf("kind = %s, want %s", event.Kind, platform.EventKindArticleCreated)
				}
				if event.Source.Platform != platform.PlatformDiscord {
					t.Fatalf("platform = %s, want %s", event.Source.Platform, platform.PlatformDiscord)
				}
				if event.Article == nil {
					t.Fatal("expected article payload")
				}
				if event.Article.ID != "777" {
					t.Fatalf("article id = %s, want 777", event.Article.ID)
				}
				if event.Article.Text != "hello" {
					t.Fatalf("article text = %s, want hello", event.Article.Text)
				}
				if event.Mutation != nil {
					t.Fatalf("mutation = %+v, want nil", event.Mutation)
				}
				if event.Conversation.ID != "100" {
					t.Fatalf("conversation id = %s, want 100", event.Conversation.ID)
				}
				if event.Actor.Username != "testuser" {
					t.Fatalf("actor username = %s, want testuser", event.Actor.Username)
				}
			},
		},
		{
			name: "message update with media attachments",
			update: Update{
				ID:         "dc:article.created:100:778",
				Type:       UpdateTypeMessage,
				OccurredAt: occurredAt,
				Channel: ChannelRef{
					ID:   "100",
					Type: platform.ConversationTypeGroup,
				},
				Actor: ActorRef{ID: "42"},
				Article: &ArticlePayload{
					ID:   "778",
					Text: "check this out",
					Media: []MediaPayload{
						{
							ID:        "att-1",
							Type:      platform.MediaTypePhoto,
							MIMEType:  "image/png",
							FileName:  "screenshot.png",
							SizeBytes: 1024,
							URI:       "https://cdn.discordapp.com/attachments/100/att-1/screenshot.png",
						},
					},
				},
			},
			assert: func(t *testing.T, event *platform.Event) {
				t.Helper()
				if event.Article == nil {
					t.Fatal("expected article payload")
				}
				if len(event.Article.Media) != 1 {
					t.Fatalf("media length = %d, want 1", len(event.Article.Media))
				}
				media := event.Article.Media[0]
				if media.ID != "att-1" {
					t.Fatalf("media id = %s, want att-1", media.ID)
				}
				if media.Type != platform.MediaTypePhoto {
					t.Fatalf("media type = %s, want %s", media.Type, platform.MediaTypePhoto)
				}
				if media.FileName != "screenshot.png" {
					t.Fatalf("media filename = %s, want screenshot.png", media.FileName)
				}
			},
		},
		{
			name: "message update with reply and thread",
			update: Update{
				ID:         "dc:article.created:100:779",
				Type:       UpdateTypeMessage,
				OccurredAt: occurredAt,
				Channel: ChannelRef{
					ID:   "100",
					Type: platform.ConversationTypeGroup,
				},
				Actor: ActorRef{ID: "42"},
				Article: &ArticlePayload{
					ID:               "779",
					ThreadID:         "thread-1",
					ReplyToArticleID: "778",
					Text:             "replying in thread",
				},
			},
			assert: func(t *testing.T, event *platform.Event) {
				t.Helper()
				if event.Article == nil {
					t.Fatal("expected article payload")
				}
				if event.Article.ThreadID != "thread-1" {
					t.Fatalf("thread id = %s, want thread-1", event.Article.ThreadID)
				}
				if event.Article.ReplyToArticleID != "778" {
					t.Fatalf("reply to = %s, want 778", event.Article.ReplyToArticleID)
				}
			},
		},
		{
			name: "edit update with explicit changed_at",
			update: Update{
				ID:         "dc:article.edited:100:777:1700000111000000000",
				Type:       UpdateTypeEdit,
				OccurredAt: occurredAt,
				Channel: ChannelRef{
					ID:   "100",
					Type: platform.ConversationTypeGroup,
				},
				Actor: ActorRef{ID: "42"},
				Edit: &ArticleEditPayload{
					ArticleID: "777",
					ChangedAt: &changedAt,
					Before: &ArticleSnapshotPayload{
						Text: "hello",
						Entities: []platform.TextEntity{
							{Type: platform.TextEntityTypeBold, Offset: 0, Length: 5},
						},
					},
					After: &ArticleSnapshotPayload{
						Text: "hello edited",
						Entities: []platform.TextEntity{
							{Type: platform.TextEntityTypeItalic, Offset: 0, Length: 12},
						},
					},
					Reason: "discord_edit_update",
				},
			},
			assert: func(t *testing.T, event *platform.Event) {
				t.Helper()
				if event.Kind != platform.EventKindArticleEdited {
					t.Fatalf("kind = %s, want %s", event.Kind, platform.EventKindArticleEdited)
				}
				if event.Mutation == nil {
					t.Fatal("expected mutation payload")
				}
				if event.Mutation.Type != platform.MutationTypeEdit {
					t.Fatalf("mutation type = %s, want %s", event.Mutation.Type, platform.MutationTypeEdit)
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
				if len(event.Mutation.Before.Entities) != 1 || event.Mutation.Before.Entities[0].Type != platform.TextEntityTypeBold {
					t.Fatalf("mutation before entities = %+v, want one bold entity", event.Mutation.Before.Entities)
				}
				if event.Mutation.After == nil || event.Mutation.After.Text != "hello edited" {
					t.Fatalf("mutation after = %+v, want text hello edited", event.Mutation.After)
				}
				if len(event.Mutation.After.Entities) != 1 || event.Mutation.After.Entities[0].Type != platform.TextEntityTypeItalic {
					t.Fatalf("mutation after entities = %+v, want one italic entity", event.Mutation.After.Entities)
				}
				if event.Mutation.Reason != "discord_edit_update" {
					t.Fatalf("mutation reason = %s, want discord_edit_update", event.Mutation.Reason)
				}
				if event.Article != nil {
					t.Fatalf("article = %+v, want nil", event.Article)
				}
			},
		},
		{
			name: "edit update falls back changed_at to occurred_at",
			update: Update{
				ID:         "dc:article.edited:100:777:1700000000000000000",
				Type:       UpdateTypeEdit,
				OccurredAt: occurredAt,
				Channel: ChannelRef{
					ID:   "100",
					Type: platform.ConversationTypeGroup,
				},
				Actor: ActorRef{ID: "42"},
				Edit: &ArticleEditPayload{
					ArticleID: "777",
					After: &ArticleSnapshotPayload{
						Text: "legacy edit",
					},
				},
			},
			assert: func(t *testing.T, event *platform.Event) {
				t.Helper()
				if event.Kind != platform.EventKindArticleEdited {
					t.Fatalf("kind = %s, want %s", event.Kind, platform.EventKindArticleEdited)
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

func TestDefaultDecoderDecodeDeletePayload(t *testing.T) {
	t.Parallel()

	decoder := NewDefaultDecoder()
	occurredAt := time.Unix(1_700_000_000, 0).UTC()

	event, err := decoder.Decode(context.Background(), Update{
		ID:         "dc:article.retracted:100:777",
		Type:       UpdateTypeDelete,
		OccurredAt: occurredAt,
		Channel: ChannelRef{
			ID:   "100",
			Type: platform.ConversationTypeGroup,
		},
		Actor: ActorRef{ID: "42"},
		Delete: &ArticleDeletePayload{
			ArticleID: "777",
			Reason:    "message_delete",
		},
	})
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if event.Kind != platform.EventKindArticleRetracted {
		t.Fatalf("kind = %s, want %s", event.Kind, platform.EventKindArticleRetracted)
	}
	if event.Mutation == nil {
		t.Fatal("expected mutation payload")
	}
	if event.Mutation.Type != platform.MutationTypeRetraction {
		t.Fatalf("mutation type = %s, want %s", event.Mutation.Type, platform.MutationTypeRetraction)
	}
	if event.Mutation.TargetArticleID != "777" {
		t.Fatalf("target article id = %s, want 777", event.Mutation.TargetArticleID)
	}
	if event.Mutation.Reason != "message_delete" {
		t.Fatalf("mutation reason = %s, want message_delete", event.Mutation.Reason)
	}
}

func TestDefaultDecoderDecodeReactionPayloads(t *testing.T) {
	t.Parallel()

	decoder := NewDefaultDecoder()
	occurredAt := time.Unix(1_700_000_000, 0).UTC()

	tests := []struct {
		name           string
		updateType     UpdateType
		expectedKind   platform.EventKind
		expectedAction platform.ReactionAction
	}{
		{
			name:           "reaction add",
			updateType:     UpdateTypeReactionAdd,
			expectedKind:   platform.EventKindArticleReactionAdded,
			expectedAction: platform.ReactionActionAdd,
		},
		{
			name:           "reaction remove",
			updateType:     UpdateTypeReactionRemove,
			expectedKind:   platform.EventKindArticleReactionRemoved,
			expectedAction: platform.ReactionActionRemove,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			event, err := decoder.Decode(context.Background(), Update{
				ID:         "dc:reaction:" + string(testCase.updateType) + ":500:777",
				Type:       testCase.updateType,
				OccurredAt: occurredAt,
				Channel: ChannelRef{
					ID:   "500",
					Type: platform.ConversationTypeChannel,
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
			if event.Kind != testCase.expectedKind {
				t.Fatalf("kind = %s, want %s", event.Kind, testCase.expectedKind)
			}
			if event.Reaction == nil {
				t.Fatal("expected reaction payload")
			}
			if event.Reaction.Action != testCase.expectedAction {
				t.Fatalf("reaction action = %s, want %s", event.Reaction.Action, testCase.expectedAction)
			}
			if event.Reaction.ArticleID != "777" {
				t.Fatalf("reaction article id = %s, want 777", event.Reaction.ArticleID)
			}
			if event.Reaction.Emoji != "❤️" {
				t.Fatalf("reaction emoji = %s, want ❤️", event.Reaction.Emoji)
			}
		})
	}
}

func TestDefaultDecoderDecodeMemberPayloads(t *testing.T) {
	t.Parallel()

	decoder := NewDefaultDecoder()
	occurredAt := time.Unix(1_700_000_000, 0).UTC()
	joinedAt := time.Unix(1_699_999_000, 0).UTC()

	tests := []struct {
		name         string
		updateType   UpdateType
		expectedKind platform.EventKind
		inviter      *ActorRef
	}{
		{
			name:         "member join with inviter",
			updateType:   UpdateTypeMemberJoin,
			expectedKind: platform.EventKindMemberJoined,
			inviter:      &ActorRef{ID: "99", Username: "inviter_user"},
		},
		{
			name:         "member join without inviter",
			updateType:   UpdateTypeMemberJoin,
			expectedKind: platform.EventKindMemberJoined,
			inviter:      nil,
		},
		{
			name:         "member leave",
			updateType:   UpdateTypeMemberLeave,
			expectedKind: platform.EventKindMemberLeft,
			inviter:      nil,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			event, err := decoder.Decode(context.Background(), Update{
				ID:         "dc:member:" + string(testCase.updateType) + ":guild1:42",
				Type:       testCase.updateType,
				OccurredAt: occurredAt,
				Channel: ChannelRef{
					ID:      "guild1",
					GuildID: "guild1",
					Type:    platform.ConversationTypeGroup,
				},
				Actor: ActorRef{ID: "42"},
				Member: &MemberPayload{
					Member:   ActorRef{ID: "55", Username: "new_member", DisplayName: "New Member"},
					Inviter:  testCase.inviter,
					Reason:   "test_reason",
					JoinedAt: joinedAt,
				},
			})
			if err != nil {
				t.Fatalf("decode failed: %v", err)
			}
			if event.Kind != testCase.expectedKind {
				t.Fatalf("kind = %s, want %s", event.Kind, testCase.expectedKind)
			}
			if event.StateChange == nil {
				t.Fatal("expected state change payload")
			}
			if event.StateChange.Type != platform.StateChangeTypeMember {
				t.Fatalf("state change type = %s, want %s", event.StateChange.Type, platform.StateChangeTypeMember)
			}
			member := event.StateChange.Member
			if member == nil {
				t.Fatal("expected member change")
			}
			if member.Action != testCase.expectedKind {
				t.Fatalf("member action = %s, want %s", member.Action, testCase.expectedKind)
			}
			if member.Member.ID != "55" {
				t.Fatalf("member id = %s, want 55", member.Member.ID)
			}
			if member.Member.Username != "new_member" {
				t.Fatalf("member username = %s, want new_member", member.Member.Username)
			}
			if testCase.inviter != nil {
				if member.Inviter == nil {
					t.Fatal("expected inviter")
				}
				if member.Inviter.ID != "99" {
					t.Fatalf("inviter id = %s, want 99", member.Inviter.ID)
				}
			} else {
				if member.Inviter != nil {
					t.Fatalf("inviter = %+v, want nil", member.Inviter)
				}
			}
		})
	}
}

func TestDefaultDecoderDecodeRolePayload(t *testing.T) {
	t.Parallel()

	decoder := NewDefaultDecoder()
	occurredAt := time.Unix(1_700_000_000, 0).UTC()

	event, err := decoder.Decode(context.Background(), Update{
		ID:         "dc:role.updated:guild1:55",
		Type:       UpdateTypeRole,
		OccurredAt: occurredAt,
		Channel: ChannelRef{
			ID:      "guild1",
			GuildID: "guild1",
			Type:    platform.ConversationTypeGroup,
		},
		Actor: ActorRef{ID: "99"},
		Role: &RolePayload{
			MemberID:  "55",
			OldRole:   "member",
			NewRole:   "moderator",
			ChangedBy: ActorRef{ID: "99", Username: "admin_user"},
		},
	})
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if event.Kind != platform.EventKindRoleUpdated {
		t.Fatalf("kind = %s, want %s", event.Kind, platform.EventKindRoleUpdated)
	}
	if event.StateChange == nil {
		t.Fatal("expected state change payload")
	}
	if event.StateChange.Type != platform.StateChangeTypeRole {
		t.Fatalf("state change type = %s, want %s", event.StateChange.Type, platform.StateChangeTypeRole)
	}
	role := event.StateChange.Role
	if role == nil {
		t.Fatal("expected role change")
	}
	if role.MemberID != "55" {
		t.Fatalf("member id = %s, want 55", role.MemberID)
	}
	if role.OldRole != "member" {
		t.Fatalf("old role = %s, want member", role.OldRole)
	}
	if role.NewRole != "moderator" {
		t.Fatalf("new role = %s, want moderator", role.NewRole)
	}
	if role.ChangedBy.ID != "99" {
		t.Fatalf("changed by id = %s, want 99", role.ChangedBy.ID)
	}
}

func TestDefaultDecoderDecodeErrors(t *testing.T) {
	t.Parallel()

	decoder := NewDefaultDecoder()
	occurredAt := time.Unix(1_700_000_000, 0).UTC()

	tests := []struct {
		name   string
		update Update
	}{
		{
			name: "unsupported update type",
			update: Update{
				ID:         "dc:unknown:100",
				Type:       UpdateType("unknown"),
				OccurredAt: occurredAt,
				Channel:    ChannelRef{ID: "100", Type: platform.ConversationTypeGroup},
				Actor:      ActorRef{ID: "42"},
			},
		},
		{
			name: "message update with nil article payload",
			update: Update{
				ID:         "dc:article.created:100:777",
				Type:       UpdateTypeMessage,
				OccurredAt: occurredAt,
				Channel:    ChannelRef{ID: "100", Type: platform.ConversationTypeGroup},
				Actor:      ActorRef{ID: "42"},
				Article:    nil,
			},
		},
		{
			name: "edit update with nil edit payload",
			update: Update{
				ID:         "dc:article.edited:100:777",
				Type:       UpdateTypeEdit,
				OccurredAt: occurredAt,
				Channel:    ChannelRef{ID: "100", Type: platform.ConversationTypeGroup},
				Actor:      ActorRef{ID: "42"},
				Edit:       nil,
			},
		},
		{
			name: "delete update with nil delete payload",
			update: Update{
				ID:         "dc:article.retracted:100:777",
				Type:       UpdateTypeDelete,
				OccurredAt: occurredAt,
				Channel:    ChannelRef{ID: "100", Type: platform.ConversationTypeGroup},
				Actor:      ActorRef{ID: "42"},
				Delete:     nil,
			},
		},
		{
			name: "reaction update with nil reaction payload",
			update: Update{
				ID:         "dc:reaction:100:777",
				Type:       UpdateTypeReactionAdd,
				OccurredAt: occurredAt,
				Channel:    ChannelRef{ID: "100", Type: platform.ConversationTypeGroup},
				Actor:      ActorRef{ID: "42"},
				Reaction:   nil,
			},
		},
		{
			name: "member update with nil member payload",
			update: Update{
				ID:         "dc:member:guild1:42",
				Type:       UpdateTypeMemberJoin,
				OccurredAt: occurredAt,
				Channel:    ChannelRef{ID: "guild1", Type: platform.ConversationTypeGroup},
				Actor:      ActorRef{ID: "42"},
				Member:     nil,
			},
		},
		{
			name: "role update with nil role payload",
			update: Update{
				ID:         "dc:role:guild1:42",
				Type:       UpdateTypeRole,
				OccurredAt: occurredAt,
				Channel:    ChannelRef{ID: "guild1", Type: platform.ConversationTypeGroup},
				Actor:      ActorRef{ID: "42"},
				Role:       nil,
			},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			_, err := decoder.Decode(context.Background(), testCase.update)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

func TestDefaultDecoderMetadataPassthrough(t *testing.T) {
	t.Parallel()

	decoder := NewDefaultDecoder()
	occurredAt := time.Unix(1_700_000_000, 0).UTC()

	metadata := map[string]string{
		"guild_id":   "guild1",
		"channel_id": "100",
	}

	event, err := decoder.Decode(context.Background(), Update{
		ID:         "dc:article.created:100:777",
		Type:       UpdateTypeMessage,
		OccurredAt: occurredAt,
		Channel: ChannelRef{
			ID:   "100",
			Type: platform.ConversationTypeGroup,
		},
		Actor: ActorRef{ID: "42"},
		Article: &ArticlePayload{
			ID:   "777",
			Text: "test",
		},
		Metadata: metadata,
	})
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if event.Metadata == nil {
		t.Fatal("expected metadata")
	}
	if event.Metadata["guild_id"] != "guild1" {
		t.Fatalf("metadata guild_id = %s, want guild1", event.Metadata["guild_id"])
	}
	if event.Metadata["channel_id"] != "100" {
		t.Fatalf("metadata channel_id = %s, want 100", event.Metadata["channel_id"])
	}
}
