package discord

import (
	"context"
	"testing"
	"time"

	"ex-otogi/pkg/otogi/platform"

	"github.com/bwmarrin/discordgo"
)

func TestDefaultDiscordgoMapper_Map(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC().Truncate(time.Second)
	editedAt := now.Add(30 * time.Second)

	mapper := NewDefaultDiscordgoMapper()
	ctx := context.Background()

	tests := []struct {
		name         string
		raw          any
		wantAccepted bool
		wantType     UpdateType
		check        func(t *testing.T, u Update)
	}{
		{
			name:         "unsupported type returns not accepted",
			raw:          "plain string",
			wantAccepted: false,
		},
		{
			name:         "nil MessageCreate inner returns not accepted",
			raw:          &discordgo.MessageCreate{Message: nil},
			wantAccepted: false,
		},
		{
			name: "MessageCreate in DM maps to message update",
			raw: &discordgo.MessageCreate{Message: &discordgo.Message{
				ID:        "m1",
				ChannelID: "c1",
				GuildID:   "",
				Content:   "hello",
				Timestamp: now,
				Author:    &discordgo.User{ID: "u1", Username: "alice", GlobalName: "Alice"},
			}},
			wantAccepted: true,
			wantType:     UpdateTypeMessage,
			check: func(t *testing.T, u Update) {
				t.Helper()
				if u.ID != "m1" {
					t.Errorf("ID = %q, want m1", u.ID)
				}
				if u.Channel.Type != platform.ConversationTypePrivate {
					t.Errorf("Channel.Type = %v, want private", u.Channel.Type)
				}
				if u.Channel.GuildID != "" {
					t.Errorf("Channel.GuildID = %q, want empty", u.Channel.GuildID)
				}
				if u.Actor.Username != "alice" {
					t.Errorf("Actor.Username = %q, want alice", u.Actor.Username)
				}
				if u.Actor.DisplayName != "Alice" {
					t.Errorf("Actor.DisplayName = %q, want Alice", u.Actor.DisplayName)
				}
				if u.Article == nil {
					t.Fatal("Article is nil")
				}
				if u.Article.Text != "hello" {
					t.Errorf("Article.Text = %q, want hello", u.Article.Text)
				}
			},
		},
		{
			name: "MessageCreate in guild maps to group conversation",
			raw: &discordgo.MessageCreate{Message: &discordgo.Message{
				ID:        "m2",
				ChannelID: "c2",
				GuildID:   "g2",
				Content:   "hi guild",
				Timestamp: now,
				Author:    &discordgo.User{ID: "u2", Username: "bob"},
			}},
			wantAccepted: true,
			wantType:     UpdateTypeMessage,
			check: func(t *testing.T, u Update) {
				t.Helper()
				if u.Channel.Type != platform.ConversationTypeGroup {
					t.Errorf("Channel.Type = %v, want group", u.Channel.Type)
				}
				if u.Channel.GuildID != "g2" {
					t.Errorf("Channel.GuildID = %q, want g2", u.Channel.GuildID)
				}
			},
		},
		{
			name: "MessageCreate with reply maps ReplyToArticleID",
			raw: &discordgo.MessageCreate{Message: &discordgo.Message{
				ID:               "m3",
				ChannelID:        "c3",
				Content:          "reply",
				Timestamp:        now,
				MessageReference: &discordgo.MessageReference{MessageID: "orig-1"},
			}},
			wantAccepted: true,
			wantType:     UpdateTypeMessage,
			check: func(t *testing.T, u Update) {
				t.Helper()
				if u.Article == nil {
					t.Fatal("Article is nil")
				}
				if u.Article.ReplyToArticleID != "orig-1" {
					t.Errorf("ReplyToArticleID = %q, want orig-1", u.Article.ReplyToArticleID)
				}
			},
		},
		{
			name: "MessageCreate with attachment maps media",
			raw: &discordgo.MessageCreate{Message: &discordgo.Message{
				ID:        "m4",
				ChannelID: "c4",
				Timestamp: now,
				Attachments: []*discordgo.MessageAttachment{
					{
						ID: "a1", URL: "https://cdn.discord.com/a1.png", ContentType: "image/png",
						Filename: "a1.png", Size: 1024,
					},
					{
						ID: "a2", URL: "https://cdn.discord.com/a2.pdf", ContentType: "application/pdf",
						Filename: "a2.pdf", Size: 2048,
					},
				},
			}},
			wantAccepted: true,
			wantType:     UpdateTypeMessage,
			check: func(t *testing.T, u Update) {
				t.Helper()
				if u.Article == nil || len(u.Article.Media) != 2 {
					t.Fatalf("expected 2 media items, got %v", u.Article)
				}
				if u.Article.Media[0].Type != platform.MediaTypePhoto {
					t.Errorf("Media[0].Type = %v, want photo", u.Article.Media[0].Type)
				}
				if u.Article.Media[0].URI != "https://cdn.discord.com/a1.png" {
					t.Errorf("Media[0].URI = %q, unexpected", u.Article.Media[0].URI)
				}
				if u.Article.Media[1].Type != platform.MediaTypeDocument {
					t.Errorf("Media[1].Type = %v, want document", u.Article.Media[1].Type)
				}
			},
		},
		{
			name:         "nil MessageUpdate inner returns not accepted",
			raw:          &discordgo.MessageUpdate{Message: nil},
			wantAccepted: false,
		},
		{
			name: "MessageUpdate maps to edit update",
			raw: &discordgo.MessageUpdate{
				Message: &discordgo.Message{
					ID:              "m5",
					ChannelID:       "c5",
					GuildID:         "g5",
					Content:         "edited content",
					Timestamp:       now,
					EditedTimestamp: &editedAt,
				},
				BeforeUpdate: &discordgo.Message{
					Content: "original content",
				},
			},
			wantAccepted: true,
			wantType:     UpdateTypeEdit,
			check: func(t *testing.T, u Update) {
				t.Helper()
				if u.Edit == nil {
					t.Fatal("Edit is nil")
				}
				if u.Edit.ArticleID != "m5" {
					t.Errorf("Edit.ArticleID = %q, want m5", u.Edit.ArticleID)
				}
				if u.Edit.Before == nil || u.Edit.Before.Text != "original content" {
					t.Errorf("Edit.Before.Text unexpected: %v", u.Edit.Before)
				}
				if u.Edit.After == nil || u.Edit.After.Text != "edited content" {
					t.Errorf("Edit.After.Text unexpected: %v", u.Edit.After)
				}
			},
		},
		{
			name:         "nil MessageDelete inner returns not accepted",
			raw:          &discordgo.MessageDelete{Message: nil},
			wantAccepted: false,
		},
		{
			name: "MessageDelete maps to delete update",
			raw: &discordgo.MessageDelete{
				Message: &discordgo.Message{
					ID:        "m6",
					ChannelID: "c6",
					GuildID:   "g6",
				},
			},
			wantAccepted: true,
			wantType:     UpdateTypeDelete,
			check: func(t *testing.T, u Update) {
				t.Helper()
				if u.Delete == nil {
					t.Fatal("Delete is nil")
				}
				if u.Delete.ArticleID != "m6" {
					t.Errorf("Delete.ArticleID = %q, want m6", u.Delete.ArticleID)
				}
			},
		},
		{
			name:         "nil MessageReactionAdd inner returns not accepted",
			raw:          &discordgo.MessageReactionAdd{MessageReaction: nil},
			wantAccepted: false,
		},
		{
			name: "MessageReactionAdd maps to reaction add update",
			raw: &discordgo.MessageReactionAdd{
				MessageReaction: &discordgo.MessageReaction{
					UserID:    "u7",
					MessageID: "m7",
					ChannelID: "c7",
					GuildID:   "g7",
					Emoji:     discordgo.Emoji{Name: "👍"},
				},
			},
			wantAccepted: true,
			wantType:     UpdateTypeReactionAdd,
			check: func(t *testing.T, u Update) {
				t.Helper()
				if u.Reaction == nil {
					t.Fatal("Reaction is nil")
				}
				if u.Reaction.ArticleID != "m7" {
					t.Errorf("Reaction.ArticleID = %q, want m7", u.Reaction.ArticleID)
				}
				if u.Reaction.Emoji != "👍" {
					t.Errorf("Reaction.Emoji = %q, want 👍", u.Reaction.Emoji)
				}
				if u.Actor.ID != "u7" {
					t.Errorf("Actor.ID = %q, want u7", u.Actor.ID)
				}
			},
		},
		{
			name: "MessageReactionAdd with custom emoji uses name:id format",
			raw: &discordgo.MessageReactionAdd{
				MessageReaction: &discordgo.MessageReaction{
					UserID:    "u8",
					MessageID: "m8",
					ChannelID: "c8",
					Emoji:     discordgo.Emoji{Name: "pepe", ID: "12345"},
				},
			},
			wantAccepted: true,
			wantType:     UpdateTypeReactionAdd,
			check: func(t *testing.T, u Update) {
				t.Helper()
				if u.Reaction.Emoji != "pepe:12345" {
					t.Errorf("Reaction.Emoji = %q, want pepe:12345", u.Reaction.Emoji)
				}
			},
		},
		{
			name: "MessageReactionAdd with member populates actor display name",
			raw: &discordgo.MessageReactionAdd{
				MessageReaction: &discordgo.MessageReaction{
					UserID:    "u9",
					MessageID: "m9",
					ChannelID: "c9",
					GuildID:   "g9",
					Emoji:     discordgo.Emoji{Name: "❤️"},
				},
				Member: &discordgo.Member{
					User:    &discordgo.User{ID: "u9", Username: "carol", GlobalName: "Carol"},
					GuildID: "g9",
				},
			},
			wantAccepted: true,
			wantType:     UpdateTypeReactionAdd,
			check: func(t *testing.T, u Update) {
				t.Helper()
				if u.Actor.Username != "carol" {
					t.Errorf("Actor.Username = %q, want carol", u.Actor.Username)
				}
			},
		},
		{
			name:         "nil MessageReactionRemove inner returns not accepted",
			raw:          &discordgo.MessageReactionRemove{MessageReaction: nil},
			wantAccepted: false,
		},
		{
			name: "MessageReactionRemove maps to reaction remove update",
			raw: &discordgo.MessageReactionRemove{
				MessageReaction: &discordgo.MessageReaction{
					UserID:    "u10",
					MessageID: "m10",
					ChannelID: "c10",
					Emoji:     discordgo.Emoji{Name: "🔥"},
				},
			},
			wantAccepted: true,
			wantType:     UpdateTypeReactionRemove,
			check: func(t *testing.T, u Update) {
				t.Helper()
				if u.Reaction == nil {
					t.Fatal("Reaction is nil")
				}
				if u.Reaction.Emoji != "🔥" {
					t.Errorf("Reaction.Emoji = %q, want 🔥", u.Reaction.Emoji)
				}
			},
		},
		{
			name:         "nil GuildMemberAdd inner returns not accepted",
			raw:          &discordgo.GuildMemberAdd{Member: nil},
			wantAccepted: false,
		},
		{
			name: "GuildMemberAdd maps to member join update",
			raw: &discordgo.GuildMemberAdd{
				Member: &discordgo.Member{
					GuildID:  "g11",
					JoinedAt: now,
					User:     &discordgo.User{ID: "u11", Username: "dave", GlobalName: "Dave"},
				},
			},
			wantAccepted: true,
			wantType:     UpdateTypeMemberJoin,
			check: func(t *testing.T, u Update) {
				t.Helper()
				if u.Member == nil {
					t.Fatal("Member is nil")
				}
				if u.Member.Member.ID != "u11" {
					t.Errorf("Member.Member.ID = %q, want u11", u.Member.Member.ID)
				}
				if u.Channel.GuildID != "g11" {
					t.Errorf("Channel.GuildID = %q, want g11", u.Channel.GuildID)
				}
				if u.Channel.Type != platform.ConversationTypeGroup {
					t.Errorf("Channel.Type = %v, want group", u.Channel.Type)
				}
			},
		},
		{
			name: "GuildMemberAdd with nick uses nick as display name",
			raw: &discordgo.GuildMemberAdd{
				Member: &discordgo.Member{
					GuildID:  "g12",
					JoinedAt: now,
					Nick:     "DaveNick",
					User:     &discordgo.User{ID: "u12", Username: "dave", GlobalName: "Dave"},
				},
			},
			wantAccepted: true,
			wantType:     UpdateTypeMemberJoin,
			check: func(t *testing.T, u Update) {
				t.Helper()
				if u.Member.Member.DisplayName != "DaveNick" {
					t.Errorf("DisplayName = %q, want DaveNick", u.Member.Member.DisplayName)
				}
			},
		},
		{
			name:         "nil GuildMemberRemove inner returns not accepted",
			raw:          &discordgo.GuildMemberRemove{Member: nil},
			wantAccepted: false,
		},
		{
			name: "GuildMemberRemove maps to member leave update",
			raw: &discordgo.GuildMemberRemove{
				Member: &discordgo.Member{
					GuildID: "g13",
					User:    &discordgo.User{ID: "u13", Username: "eve"},
				},
			},
			wantAccepted: true,
			wantType:     UpdateTypeMemberLeave,
			check: func(t *testing.T, u Update) {
				t.Helper()
				if u.Member == nil {
					t.Fatal("Member is nil")
				}
				if u.Member.Member.ID != "u13" {
					t.Errorf("Member.Member.ID = %q, want u13", u.Member.Member.ID)
				}
				if u.Channel.GuildID != "g13" {
					t.Errorf("Channel.GuildID = %q, want g13", u.Channel.GuildID)
				}
			},
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			update, accepted, err := mapper.Map(ctx, testCase.raw)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if accepted != testCase.wantAccepted {
				t.Errorf("accepted = %v, want %v", accepted, testCase.wantAccepted)
			}
			if !testCase.wantAccepted {
				return
			}
			if update.Type != testCase.wantType {
				t.Errorf("Type = %v, want %v", update.Type, testCase.wantType)
			}
			if testCase.check != nil {
				testCase.check(t, update)
			}
		})
	}
}

func TestDiscordgoMediaTypeFromAttachment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		contentType string
		wantType    platform.MediaType
	}{
		{"image/png", platform.MediaTypePhoto},
		{"image/jpeg", platform.MediaTypePhoto},
		{"image/gif", platform.MediaTypePhoto},
		{"video/mp4", platform.MediaTypeVideo},
		{"video/webm", platform.MediaTypeVideo},
		{"audio/mpeg", platform.MediaTypeAudio},
		{"audio/ogg", platform.MediaTypeAudio},
		{"application/pdf", platform.MediaTypeDocument},
		{"text/plain", platform.MediaTypeDocument},
		{"", platform.MediaTypeDocument},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.contentType, func(t *testing.T) {
			t.Parallel()
			att := &discordgo.MessageAttachment{ContentType: tc.contentType}
			got := discordMediaTypeFromAttachment(att)
			if got != tc.wantType {
				t.Errorf("discordMediaTypeFromAttachment(%q) = %v, want %v", tc.contentType, got, tc.wantType)
			}
		})
	}
}

func TestDiscordgoNormalizeEmoji(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		emoji discordgo.Emoji
		want  string
	}{
		{
			name:  "standard emoji uses name",
			emoji: discordgo.Emoji{Name: "👍"},
			want:  "👍",
		},
		{
			name:  "custom emoji uses name:id",
			emoji: discordgo.Emoji{Name: "pepe", ID: "123456"},
			want:  "pepe:123456",
		},
		{
			name:  "empty emoji",
			emoji: discordgo.Emoji{},
			want:  "",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := discordNormalizeEmoji(tc.emoji)
			if got != tc.want {
				t.Errorf("discordNormalizeEmoji = %q, want %q", got, tc.want)
			}
		})
	}
}
