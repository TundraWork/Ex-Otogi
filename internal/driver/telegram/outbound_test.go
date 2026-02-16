package telegram

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"ex-otogi/pkg/otogi"

	"github.com/gotd/td/tg"
)

func TestOutboundDispatcherSendMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		request     otogi.SendMessageRequest
		rpcErr      error
		wantErr     bool
		wantMessage string
	}{
		{
			name: "successful send",
			request: otogi.SendMessageRequest{
				Target: otogi.OutboundTarget{
					Platform: otogi.PlatformTelegram,
					Conversation: otogi.Conversation{
						ID:   "42",
						Type: otogi.ConversationTypeGroup,
					},
				},
				Text: "pong",
			},
			wantMessage: "901",
		},
		{
			name: "successful send with entities",
			request: otogi.SendMessageRequest{
				Target: otogi.OutboundTarget{
					Platform: otogi.PlatformTelegram,
					Conversation: otogi.Conversation{
						ID:   "42",
						Type: otogi.ConversationTypeGroup,
					},
				},
				Text: "click me",
				Entities: []otogi.TextEntity{
					{
						Type:   otogi.TextEntityTypeTextURL,
						Offset: 0,
						Length: 8,
						URL:    "https://example.com",
					},
				},
			},
			wantMessage: "901",
		},
		{
			name: "invalid request",
			request: otogi.SendMessageRequest{
				Target: otogi.OutboundTarget{
					Platform: otogi.PlatformTelegram,
					Conversation: otogi.Conversation{
						ID:   "42",
						Type: otogi.ConversationTypeGroup,
					},
				},
			},
			wantErr: true,
		},
		{
			name: "unsupported platform",
			request: otogi.SendMessageRequest{
				Target: otogi.OutboundTarget{
					Platform: "discord",
					Conversation: otogi.Conversation{
						ID:   "42",
						Type: otogi.ConversationTypeGroup,
					},
				},
				Text: "pong",
			},
			wantErr: true,
		},
		{
			name: "rpc failure",
			request: otogi.SendMessageRequest{
				Target: otogi.OutboundTarget{
					Platform: otogi.PlatformTelegram,
					Conversation: otogi.Conversation{
						ID:   "42",
						Type: otogi.ConversationTypeGroup,
					},
				},
				Text: "pong",
			},
			rpcErr:  errors.New("send failed"),
			wantErr: true,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			cache := NewPeerCache()
			cache.RememberConversation(
				ChatRef{ID: "42", Type: otogi.ConversationTypeGroup},
				&tg.InputPeerChat{ChatID: 42},
			)

			rpc := &stubOutboundRPC{sendID: 901, sendErr: testCase.rpcErr}
			dispatcher, err := newOutboundDispatcherWithRPC(rpc, cache)
			if err != nil {
				t.Fatalf("new dispatcher failed: %v", err)
			}

			outboundMessage, err := dispatcher.SendMessage(context.Background(), testCase.request)
			if testCase.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if outboundMessage == nil {
				t.Fatal("expected outbound message")
			}
			if outboundMessage.ID != testCase.wantMessage {
				t.Fatalf("message id = %s, want %s", outboundMessage.ID, testCase.wantMessage)
			}
			if rpc.sendCalls != 1 {
				t.Fatalf("send calls = %d, want 1", rpc.sendCalls)
			}
			if len(rpc.lastSendRequest.Entities) != len(testCase.request.Entities) {
				t.Fatalf(
					"entity len = %d, want %d",
					len(rpc.lastSendRequest.Entities),
					len(testCase.request.Entities),
				)
			}
		})
	}
}

func TestOutboundDispatcherEditMessage(t *testing.T) {
	t.Parallel()

	cache := NewPeerCache()
	cache.RememberConversation(
		ChatRef{ID: "42", Type: otogi.ConversationTypeGroup},
		&tg.InputPeerChat{ChatID: 42},
	)

	tests := []struct {
		name    string
		request otogi.EditMessageRequest
		wantErr bool
	}{
		{
			name: "successful edit with entities",
			request: otogi.EditMessageRequest{
				Target: otogi.OutboundTarget{
					Platform: otogi.PlatformTelegram,
					Conversation: otogi.Conversation{
						ID:   "42",
						Type: otogi.ConversationTypeGroup,
					},
				},
				MessageID: "10",
				Text:      "updated",
				Entities: []otogi.TextEntity{
					{
						Type:   otogi.TextEntityTypeBold,
						Offset: 0,
						Length: 7,
					},
				},
			},
		},
		{
			name: "invalid edit entity payload",
			request: otogi.EditMessageRequest{
				Target: otogi.OutboundTarget{
					Platform: otogi.PlatformTelegram,
					Conversation: otogi.Conversation{
						ID:   "42",
						Type: otogi.ConversationTypeGroup,
					},
				},
				MessageID: "10",
				Text:      "updated",
				Entities: []otogi.TextEntity{
					{
						Type:   otogi.TextEntityTypeCustomEmoji,
						Offset: 0,
						Length: 1,
					},
				},
			},
			wantErr: true,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			rpc := &stubOutboundRPC{}
			dispatcher, err := newOutboundDispatcherWithRPC(rpc, cache)
			if err != nil {
				t.Fatalf("new dispatcher failed: %v", err)
			}

			err = dispatcher.EditMessage(context.Background(), testCase.request)
			if testCase.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if rpc.editCalls != 1 {
				t.Fatalf("edit calls = %d, want 1", rpc.editCalls)
			}
			if len(rpc.lastEditRequest.Entities) != len(testCase.request.Entities) {
				t.Fatalf(
					"entity len = %d, want %d",
					len(rpc.lastEditRequest.Entities),
					len(testCase.request.Entities),
				)
			}
		})
	}
}

func TestOutboundDispatcherSetReaction(t *testing.T) {
	t.Parallel()

	cache := NewPeerCache()
	cache.RememberConversation(
		ChatRef{ID: "42", Type: otogi.ConversationTypeGroup},
		&tg.InputPeerChat{ChatID: 42},
	)

	tests := []struct {
		name                 string
		request              otogi.SetReactionRequest
		wantErr              bool
		wantReactionLen      int
		wantReactionType     string
		wantCustomDocument   int64
		wantReactionEmoticon string
	}{
		{
			name: "add emoji reaction",
			request: otogi.SetReactionRequest{
				Target: otogi.OutboundTarget{
					Platform: otogi.PlatformTelegram,
					Conversation: otogi.Conversation{
						ID:   "42",
						Type: otogi.ConversationTypeGroup,
					},
				},
				MessageID: "10",
				Emoji:     "👍",
				Action:    otogi.ReactionActionAdd,
			},
			wantReactionLen:      1,
			wantReactionType:     "*tg.ReactionEmoji",
			wantReactionEmoticon: "👍",
		},
		{
			name: "add custom reaction",
			request: otogi.SetReactionRequest{
				Target: otogi.OutboundTarget{
					Platform: otogi.PlatformTelegram,
					Conversation: otogi.Conversation{
						ID:   "42",
						Type: otogi.ConversationTypeGroup,
					},
				},
				MessageID: "10",
				Emoji:     "custom:123",
				Action:    otogi.ReactionActionAdd,
			},
			wantReactionLen:    1,
			wantReactionType:   "*tg.ReactionCustomEmoji",
			wantCustomDocument: 123,
		},
		{
			name: "remove reaction",
			request: otogi.SetReactionRequest{
				Target: otogi.OutboundTarget{
					Platform: otogi.PlatformTelegram,
					Conversation: otogi.Conversation{
						ID:   "42",
						Type: otogi.ConversationTypeGroup,
					},
				},
				MessageID: "10",
				Action:    otogi.ReactionActionRemove,
			},
			wantReactionLen: 0,
		},
		{
			name: "invalid message id",
			request: otogi.SetReactionRequest{
				Target: otogi.OutboundTarget{
					Platform: otogi.PlatformTelegram,
					Conversation: otogi.Conversation{
						ID:   "42",
						Type: otogi.ConversationTypeGroup,
					},
				},
				MessageID: "bad",
				Emoji:     "👍",
				Action:    otogi.ReactionActionAdd,
			},
			wantErr: true,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			rpc := &stubOutboundRPC{}
			dispatcher, err := newOutboundDispatcherWithRPC(rpc, cache)
			if err != nil {
				t.Fatalf("new dispatcher failed: %v", err)
			}

			err = dispatcher.SetReaction(context.Background(), testCase.request)
			if testCase.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if rpc.reactionCalls != 1 {
				t.Fatalf("reaction calls = %d, want 1", rpc.reactionCalls)
			}
			if len(rpc.reactions) != testCase.wantReactionLen {
				t.Fatalf("reaction len = %d, want %d", len(rpc.reactions), testCase.wantReactionLen)
			}
			if len(rpc.reactions) == 0 {
				return
			}
			gotType := typeName(rpc.reactions[0])
			if gotType != testCase.wantReactionType {
				t.Fatalf("reaction type = %s, want %s", gotType, testCase.wantReactionType)
			}

			if emoji, ok := rpc.reactions[0].(*tg.ReactionEmoji); ok {
				if emoji.Emoticon != testCase.wantReactionEmoticon {
					t.Fatalf("emoticon = %s, want %s", emoji.Emoticon, testCase.wantReactionEmoticon)
				}
			}
			if custom, ok := rpc.reactions[0].(*tg.ReactionCustomEmoji); ok {
				if custom.DocumentID != testCase.wantCustomDocument {
					t.Fatalf("custom document id = %d, want %d", custom.DocumentID, testCase.wantCustomDocument)
				}
			}
		})
	}
}

func TestOutboundDispatcherDeleteMessage(t *testing.T) {
	t.Parallel()

	cache := NewPeerCache()
	cache.RememberConversation(
		ChatRef{ID: "42", Type: otogi.ConversationTypeChannel},
		&tg.InputPeerChannel{ChannelID: 42, AccessHash: 100},
	)

	tests := []struct {
		name    string
		request otogi.DeleteMessageRequest
		wantErr bool
	}{
		{
			name: "revoke channel delete succeeds",
			request: otogi.DeleteMessageRequest{
				Target: otogi.OutboundTarget{
					Platform: otogi.PlatformTelegram,
					Conversation: otogi.Conversation{
						ID:   "42",
						Type: otogi.ConversationTypeChannel,
					},
				},
				MessageID: "5",
				Revoke:    true,
			},
		},
		{
			name: "non-revoke channel delete fails",
			request: otogi.DeleteMessageRequest{
				Target: otogi.OutboundTarget{
					Platform: otogi.PlatformTelegram,
					Conversation: otogi.Conversation{
						ID:   "42",
						Type: otogi.ConversationTypeChannel,
					},
				},
				MessageID: "5",
			},
			wantErr: true,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			rpc := &stubOutboundRPC{}
			dispatcher, err := newOutboundDispatcherWithRPC(rpc, cache)
			if err != nil {
				t.Fatalf("new dispatcher failed: %v", err)
			}

			err = dispatcher.DeleteMessage(context.Background(), testCase.request)
			if testCase.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if rpc.deleteCalls != 1 {
				t.Fatalf("delete calls = %d, want 1", rpc.deleteCalls)
			}
		})
	}
}

func TestMapOutboundTextEntities(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		text         string
		entities     []otogi.TextEntity
		wantErr      bool
		wantLen      int
		wantTypeName string
		wantOffset   int
		wantLength   int
	}{
		{
			name: "empty entities",
			text: "hello",
		},
		{
			name: "maps bold",
			text: "hello",
			entities: []otogi.TextEntity{
				{Type: otogi.TextEntityTypeBold, Offset: 0, Length: 5},
			},
			wantLen:      1,
			wantTypeName: "*tg.MessageEntityBold",
			wantOffset:   0,
			wantLength:   5,
		},
		{
			name: "maps pre language",
			text: "fmt.Println()",
			entities: []otogi.TextEntity{
				{Type: otogi.TextEntityTypePre, Offset: 0, Length: 12, Language: "go"},
			},
			wantLen:      1,
			wantTypeName: "*tg.MessageEntityPre",
			wantOffset:   0,
			wantLength:   12,
		},
		{
			name: "maps utf16 offsets",
			text: "a😀b",
			entities: []otogi.TextEntity{
				{Type: otogi.TextEntityTypeBold, Offset: 1, Length: 1},
			},
			wantLen:      1,
			wantTypeName: "*tg.MessageEntityBold",
			wantOffset:   1,
			wantLength:   2,
		},
		{
			name: "invalid range fails",
			text: "hello",
			entities: []otogi.TextEntity{
				{Type: otogi.TextEntityTypeBold, Offset: 0, Length: 6},
			},
			wantErr: true,
		},
		{
			name: "unsupported type fails",
			text: "hello",
			entities: []otogi.TextEntity{
				{Type: "fancy", Offset: 0, Length: 5},
			},
			wantErr: true,
		},
		{
			name: "mention_name unsupported",
			text: "alice",
			entities: []otogi.TextEntity{
				{Type: otogi.TextEntityTypeMentionName, Offset: 0, Length: 5, MentionUserID: "123"},
			},
			wantErr: true,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			converted, err := mapOutboundTextEntities(testCase.text, testCase.entities)
			if testCase.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(converted) != testCase.wantLen {
				t.Fatalf("converted len = %d, want %d", len(converted), testCase.wantLen)
			}
			if len(converted) == 0 {
				return
			}

			if gotType := typeName(converted[0]); gotType != testCase.wantTypeName {
				t.Fatalf("type = %s, want %s", gotType, testCase.wantTypeName)
			}
			if converted[0].GetOffset() != testCase.wantOffset {
				t.Fatalf("offset = %d, want %d", converted[0].GetOffset(), testCase.wantOffset)
			}
			if converted[0].GetLength() != testCase.wantLength {
				t.Fatalf("length = %d, want %d", converted[0].GetLength(), testCase.wantLength)
			}
		})
	}
}

type stubOutboundRPC struct {
	sendID          int
	sendErr         error
	lastSendRequest otogi.SendMessageRequest
	lastEditRequest otogi.EditMessageRequest
	sendCalls       int
	editCalls       int
	deleteCalls     int
	reactionCalls   int
	reactions       []tg.ReactionClass
}

func (s *stubOutboundRPC) SendText(
	_ context.Context,
	_ tg.InputPeerClass,
	request otogi.SendMessageRequest,
) (int, error) {
	s.sendCalls++
	s.lastSendRequest = request
	if s.sendErr != nil {
		return 0, s.sendErr
	}

	return s.sendID, nil
}

func (s *stubOutboundRPC) EditText(
	_ context.Context,
	_ tg.InputPeerClass,
	_ int,
	request otogi.EditMessageRequest,
) error {
	s.editCalls++
	s.lastEditRequest = request
	return nil
}

func (s *stubOutboundRPC) DeleteMessage(
	_ context.Context,
	peer tg.InputPeerClass,
	_ int,
	revoke bool,
) error {
	s.deleteCalls++
	if _, isChannel := peer.(*tg.InputPeerChannel); isChannel && !revoke {
		return otogi.ErrOutboundUnsupported
	}

	return nil
}

func (s *stubOutboundRPC) SetReaction(
	_ context.Context,
	_ tg.InputPeerClass,
	_ int,
	reactions []tg.ReactionClass,
) error {
	s.reactionCalls++
	s.reactions = reactions
	return nil
}

func typeName(value any) string {
	return fmt.Sprintf("%T", value)
}
