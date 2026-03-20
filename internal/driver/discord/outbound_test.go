package discord

import (
	"context"
	"errors"
	"testing"
	"time"

	"ex-otogi/pkg/otogi/platform"

	"github.com/bwmarrin/discordgo"
)

// mockDiscordOutboundRPC is a controllable implementation of discordOutboundRPC.
type mockDiscordOutboundRPC struct {
	sendFn        func(channelID, content string) (*discordgo.Message, error)
	editFn        func(channelID, messageID, content string) (*discordgo.Message, error)
	deleteFn      func(channelID, messageID string) error
	reactionAddFn func(channelID, messageID, emojiID string) error
	reactionRemFn func(channelID, messageID, emojiID, userID string) error
	timeoutFn     func(guildID, userID string, until *time.Time) error
}

func (m *mockDiscordOutboundRPC) ChannelMessageSend(channelID, content string) (*discordgo.Message, error) {
	if m.sendFn != nil {
		return m.sendFn(channelID, content)
	}

	return &discordgo.Message{ID: "msg-1"}, nil
}

func (m *mockDiscordOutboundRPC) ChannelMessageEdit(channelID, messageID, content string) (*discordgo.Message, error) {
	if m.editFn != nil {
		return m.editFn(channelID, messageID, content)
	}

	return &discordgo.Message{ID: messageID}, nil
}

func (m *mockDiscordOutboundRPC) ChannelMessageDelete(channelID, messageID string) error {
	if m.deleteFn != nil {
		return m.deleteFn(channelID, messageID)
	}

	return nil
}

func (m *mockDiscordOutboundRPC) MessageReactionAdd(channelID, messageID, emojiID string) error {
	if m.reactionAddFn != nil {
		return m.reactionAddFn(channelID, messageID, emojiID)
	}

	return nil
}

func (m *mockDiscordOutboundRPC) MessageReactionRemove(channelID, messageID, emojiID, userID string) error {
	if m.reactionRemFn != nil {
		return m.reactionRemFn(channelID, messageID, emojiID, userID)
	}

	return nil
}

func (m *mockDiscordOutboundRPC) GuildMemberTimeout(guildID, userID string, until *time.Time) error {
	if m.timeoutFn != nil {
		return m.timeoutFn(guildID, userID, until)
	}

	return nil
}

func defaultTestTarget() platform.OutboundTarget {
	return platform.OutboundTarget{
		Conversation: platform.Conversation{
			ID:   "chan-1",
			Type: platform.ConversationTypeGroup,
		},
	}
}

func TestSinkDispatcher_SendMessage(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	tests := []struct {
		name          string
		request       platform.SendMessageRequest
		sendFn        func(channelID, content string) (*discordgo.Message, error)
		wantErr       bool
		wantErrSubstr string
		wantMsgID     string
	}{
		{
			name: "successful send returns first message ID",
			request: platform.SendMessageRequest{
				Target: defaultTestTarget(),
				Text:   "hello",
			},
			sendFn: func(_, _ string) (*discordgo.Message, error) {
				return &discordgo.Message{ID: "m-abc"}, nil
			},
			wantMsgID: "m-abc",
		},
		{
			name: "invalid request returns error",
			request: platform.SendMessageRequest{
				Target: platform.OutboundTarget{
					Conversation: platform.Conversation{ID: "", Type: ""},
				},
				Text: "hello",
			},
			wantErr:       true,
			wantErrSubstr: "send message",
		},
		{
			name: "rpc error is wrapped",
			request: platform.SendMessageRequest{
				Target: defaultTestTarget(),
				Text:   "hello",
			},
			sendFn: func(_, _ string) (*discordgo.Message, error) {
				return nil, errors.New("gateway offline")
			},
			wantErr: true,
		},
		{
			name: "tags are echoed in response",
			request: platform.SendMessageRequest{
				Target: defaultTestTarget(),
				Text:   "hi",
				Tags:   map[string]string{"k": "v"},
			},
			wantMsgID: "msg-1",
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			rpc := &mockDiscordOutboundRPC{sendFn: testCase.sendFn}
			d, err := newSinkDispatcherWithRPC(rpc)
			if err != nil {
				t.Fatalf("setup failed: %v", err)
			}

			result, err := d.SendMessage(ctx, testCase.request)
			if testCase.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if testCase.wantErrSubstr != "" && !containsStr(err.Error(), testCase.wantErrSubstr) {
					t.Errorf("error %q does not contain %q", err.Error(), testCase.wantErrSubstr)
				}

				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if testCase.wantMsgID != "" && result.ID != testCase.wantMsgID {
				t.Errorf("ID = %q, want %q", result.ID, testCase.wantMsgID)
			}
			if testCase.request.Tags != nil {
				for k, v := range testCase.request.Tags {
					if result.Tags[k] != v {
						t.Errorf("Tags[%q] = %q, want %q", k, result.Tags[k], v)
					}
				}
			}
		})
	}
}

func TestSinkDispatcher_EditMessage(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	tests := []struct {
		name        string
		request     platform.EditMessageRequest
		editFn      func(channelID, messageID, content string) (*discordgo.Message, error)
		wantErr     bool
		wantContent string
	}{
		{
			name: "successful edit",
			request: platform.EditMessageRequest{
				Target:    defaultTestTarget(),
				MessageID: "m-1",
				Text:      "updated",
			},
			wantContent: "updated",
		},
		{
			name: "invalid request returns error",
			request: platform.EditMessageRequest{
				Target:    platform.OutboundTarget{Conversation: platform.Conversation{ID: "", Type: ""}},
				MessageID: "m-1",
				Text:      "x",
			},
			wantErr: true,
		},
		{
			name: "rpc error is wrapped",
			request: platform.EditMessageRequest{
				Target:    defaultTestTarget(),
				MessageID: "m-1",
				Text:      "x",
			},
			editFn: func(_, _, _ string) (*discordgo.Message, error) {
				return nil, errors.New("not found")
			},
			wantErr: true,
		},
		{
			name: "over-long text is truncated",
			request: platform.EditMessageRequest{
				Target:    defaultTestTarget(),
				MessageID: "m-2",
				Text:      repeatStr("x", discordMaxMessageLength+100),
			},
			editFn: func(_, _, content string) (*discordgo.Message, error) {
				if len([]rune(content)) > discordMaxMessageLength {
					t.Error("content exceeds max message length")
				}

				return &discordgo.Message{ID: "m-2"}, nil
			},
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			var captured string
			rpc := &mockDiscordOutboundRPC{
				editFn: func(channelID, messageID, content string) (*discordgo.Message, error) {
					captured = content
					if testCase.editFn != nil {
						return testCase.editFn(channelID, messageID, content)
					}

					return &discordgo.Message{ID: messageID}, nil
				},
			}
			d, err := newSinkDispatcherWithRPC(rpc)
			if err != nil {
				t.Fatalf("setup failed: %v", err)
			}

			err = d.EditMessage(ctx, testCase.request)
			if testCase.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}

				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if testCase.wantContent != "" && captured != testCase.wantContent {
				t.Errorf("content = %q, want %q", captured, testCase.wantContent)
			}
		})
	}
}

func TestSinkDispatcher_DeleteMessage(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	tests := []struct {
		name    string
		request platform.DeleteMessageRequest
		wantErr bool
	}{
		{
			name: "successful delete",
			request: platform.DeleteMessageRequest{
				Target:    defaultTestTarget(),
				MessageID: "m-del",
			},
		},
		{
			name: "invalid request",
			request: platform.DeleteMessageRequest{
				Target:    platform.OutboundTarget{Conversation: platform.Conversation{ID: "", Type: ""}},
				MessageID: "m-del",
			},
			wantErr: true,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			d, err := newSinkDispatcherWithRPC(&mockDiscordOutboundRPC{})
			if err != nil {
				t.Fatalf("setup failed: %v", err)
			}

			err = d.DeleteMessage(ctx, testCase.request)
			if testCase.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}

				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestSinkDispatcher_SetReaction(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	tests := []struct {
		name            string
		request         platform.SetReactionRequest
		captureAddFn    func(channelID, messageID, emojiID string) error
		captureRemoveFn func(channelID, messageID, emojiID, userID string) error
		wantErr         bool
		checkSelfUser   bool
	}{
		{
			name: "add reaction",
			request: platform.SetReactionRequest{
				Target:    defaultTestTarget(),
				MessageID: "m-1",
				Emoji:     "👍",
				Action:    platform.ReactionActionAdd,
			},
		},
		{
			name: "remove reaction uses @me",
			request: platform.SetReactionRequest{
				Target:    defaultTestTarget(),
				MessageID: "m-2",
				Emoji:     "👍",
				Action:    platform.ReactionActionRemove,
			},
			checkSelfUser: true,
		},
		{
			name: "invalid request",
			request: platform.SetReactionRequest{
				Target: platform.OutboundTarget{Conversation: platform.Conversation{ID: "", Type: ""}},
			},
			wantErr: true,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			var capturedUserID string
			rpc := &mockDiscordOutboundRPC{
				reactionRemFn: func(_, _, _, userID string) error {
					capturedUserID = userID

					return nil
				},
			}

			d, err := newSinkDispatcherWithRPC(rpc)
			if err != nil {
				t.Fatalf("setup failed: %v", err)
			}

			err = d.SetReaction(ctx, testCase.request)
			if testCase.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}

				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if testCase.checkSelfUser && capturedUserID != discordBotSelfUserID {
				t.Errorf("reaction remove userID = %q, want %q", capturedUserID, discordBotSelfUserID)
			}
		})
	}
}

func TestSinkDispatcher_ListSinks(t *testing.T) {
	t.Parallel()

	d, err := newSinkDispatcherWithRPC(&mockDiscordOutboundRPC{}, WithOutboundSinkRef(platform.EventSink{
		Platform: DriverPlatform,
		ID:       "test-bot",
	}))
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	sinks, err := d.ListSinks(context.Background())
	if err != nil {
		t.Fatalf("ListSinks error: %v", err)
	}
	if len(sinks) != 1 {
		t.Fatalf("ListSinks returned %d sinks, want 1", len(sinks))
	}
	if sinks[0].ID != "test-bot" {
		t.Errorf("sink ID = %q, want test-bot", sinks[0].ID)
	}

	sinksByPlatform, err := d.ListSinksByPlatform(context.Background(), DriverPlatform)
	if err != nil {
		t.Fatalf("ListSinksByPlatform error: %v", err)
	}
	if len(sinksByPlatform) != 1 {
		t.Errorf("ListSinksByPlatform returned %d sinks, want 1", len(sinksByPlatform))
	}

	sinksByOther, err := d.ListSinksByPlatform(context.Background(), "telegram")
	if err != nil {
		t.Fatalf("ListSinksByPlatform (other) error: %v", err)
	}
	if len(sinksByOther) != 0 {
		t.Errorf("ListSinksByPlatform (other) returned %d sinks, want 0", len(sinksByOther))
	}
}

func TestModerationDispatcher_RestrictMember(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	tests := []struct {
		name       string
		request    platform.RestrictMemberRequest
		checkClear bool
		wantErr    bool
	}{
		{
			name: "restrict member applies timeout",
			request: platform.RestrictMemberRequest{
				Target: platform.OutboundTarget{
					Conversation: platform.Conversation{ID: "guild-1", Type: platform.ConversationTypeGroup},
				},
				MemberID:    "user-1",
				Permissions: platform.NoPermissionsGranted(),
				UntilDate:   time.Now().Add(time.Hour),
			},
		},
		{
			name: "full permissions clears timeout",
			request: platform.RestrictMemberRequest{
				Target: platform.OutboundTarget{
					Conversation: platform.Conversation{ID: "guild-1", Type: platform.ConversationTypeGroup},
				},
				MemberID:    "user-2",
				Permissions: platform.AllPermissionsGranted(),
			},
			checkClear: true,
		},
		{
			name: "invalid request returns error",
			request: platform.RestrictMemberRequest{
				Target: platform.OutboundTarget{
					Conversation: platform.Conversation{ID: "", Type: ""},
				},
			},
			wantErr: true,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			var capturedUntil *time.Time
			rpc := &mockDiscordOutboundRPC{
				timeoutFn: func(_, _ string, until *time.Time) error {
					capturedUntil = until

					return nil
				},
			}

			d, err := newModerationDispatcherWithRPC(rpc)
			if err != nil {
				t.Fatalf("setup failed: %v", err)
			}

			err = d.RestrictMember(ctx, testCase.request)
			if testCase.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}

				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if testCase.checkClear && capturedUntil != nil {
				t.Errorf("expected nil until for clear timeout, got %v", capturedUntil)
			}
			if !testCase.checkClear && capturedUntil == nil {
				t.Error("expected non-nil until for restrict, got nil")
			}
		})
	}
}
