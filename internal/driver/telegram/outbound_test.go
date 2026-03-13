package telegram

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"ex-otogi/pkg/otogi/platform"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

func TestOutboundDispatcherSendMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		request     platform.SendMessageRequest
		rpcErr      error
		wantErr     bool
		wantMessage string
	}{
		{
			name: "successful send",
			request: platform.SendMessageRequest{
				Target: platform.OutboundTarget{
					Conversation: platform.Conversation{
						ID:   "42",
						Type: platform.ConversationTypeGroup,
					},
				},
				Text: "pong",
			},
			wantMessage: "901",
		},
		{
			name: "successful send with entities",
			request: platform.SendMessageRequest{
				Target: platform.OutboundTarget{
					Conversation: platform.Conversation{
						ID:   "42",
						Type: platform.ConversationTypeGroup,
					},
				},
				Text: "click me",
				Entities: []platform.TextEntity{
					{
						Type:   platform.TextEntityTypeTextURL,
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
			request: platform.SendMessageRequest{
				Target: platform.OutboundTarget{
					Conversation: platform.Conversation{
						ID:   "42",
						Type: platform.ConversationTypeGroup,
					},
				},
			},
			wantErr: true,
		},
		{
			name: "unsupported platform",
			request: platform.SendMessageRequest{
				Target: platform.OutboundTarget{
					Sink: &platform.EventSink{
						Platform: "discord",
					},
					Conversation: platform.Conversation{
						ID:   "42",
						Type: platform.ConversationTypeGroup,
					},
				},
				Text: "pong",
			},
			wantErr: true,
		},
		{
			name: "rpc failure",
			request: platform.SendMessageRequest{
				Target: platform.OutboundTarget{
					Conversation: platform.Conversation{
						ID:   "42",
						Type: platform.ConversationTypeGroup,
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
				ChatRef{ID: "42", Type: platform.ConversationTypeGroup},
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
		ChatRef{ID: "42", Type: platform.ConversationTypeGroup},
		&tg.InputPeerChat{ChatID: 42},
	)

	tests := []struct {
		name    string
		request platform.EditMessageRequest
		wantErr bool
	}{
		{
			name: "successful edit with entities",
			request: platform.EditMessageRequest{
				Target: platform.OutboundTarget{
					Conversation: platform.Conversation{
						ID:   "42",
						Type: platform.ConversationTypeGroup,
					},
				},
				MessageID: "10",
				Text:      "updated",
				Entities: []platform.TextEntity{
					{
						Type:   platform.TextEntityTypeBold,
						Offset: 0,
						Length: 7,
					},
				},
			},
		},
		{
			name: "invalid edit entity payload",
			request: platform.EditMessageRequest{
				Target: platform.OutboundTarget{
					Conversation: platform.Conversation{
						ID:   "42",
						Type: platform.ConversationTypeGroup,
					},
				},
				MessageID: "10",
				Text:      "updated",
				Entities: []platform.TextEntity{
					{
						Type:   platform.TextEntityTypeCustomEmoji,
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

func TestOutboundDispatcherSendMessageAppliesReadableRender(t *testing.T) {
	t.Parallel()

	cache := NewPeerCache()
	cache.RememberConversation(
		ChatRef{ID: "42", Type: platform.ConversationTypeGroup},
		&tg.InputPeerChat{ChatID: 42},
	)

	rpc := &stubOutboundRPC{sendID: 901}
	dispatcher, err := newOutboundDispatcherWithRPC(rpc, cache)
	if err != nil {
		t.Fatalf("new dispatcher failed: %v", err)
	}

	request := platform.SendMessageRequest{
		Target: platform.OutboundTarget{
			Conversation: platform.Conversation{
				ID:   "42",
				Type: platform.ConversationTypeGroup,
			},
		},
		Text: "# Hi",
		Entities: []platform.TextEntity{
			{
				Type:   platform.TextEntityTypeHeading,
				Offset: 0,
				Length: 4,
				Heading: &platform.TextEntityHeadingMeta{
					Level: 1,
				},
			},
			{
				Type:   platform.TextEntityTypeBold,
				Offset: 2,
				Length: 2,
			},
		},
	}

	if _, err := dispatcher.SendMessage(context.Background(), request); err != nil {
		t.Fatalf("SendMessage failed: %v", err)
	}

	if rpc.lastSendRequest.Text != "Hi" {
		t.Fatalf("rendered text = %q, want %q", rpc.lastSendRequest.Text, "Hi")
	}
	bold, ok := findEntityByType(rpc.lastSendRequest.Entities, platform.TextEntityTypeBold)
	if !ok {
		t.Fatal("bold entity not found")
	}
	if bold.Offset != 0 || bold.Length != 2 {
		t.Fatalf("bold range = [%d,%d), want [0,2)", bold.Offset, bold.Offset+bold.Length)
	}
}

func TestOutboundDispatcherEditMessageAppliesReadableRender(t *testing.T) {
	t.Parallel()

	cache := NewPeerCache()
	cache.RememberConversation(
		ChatRef{ID: "42", Type: platform.ConversationTypeGroup},
		&tg.InputPeerChat{ChatID: 42},
	)

	rpc := &stubOutboundRPC{}
	dispatcher, err := newOutboundDispatcherWithRPC(rpc, cache)
	if err != nil {
		t.Fatalf("new dispatcher failed: %v", err)
	}

	request := platform.EditMessageRequest{
		Target: platform.OutboundTarget{
			Conversation: platform.Conversation{
				ID:   "42",
				Type: platform.ConversationTypeGroup,
			},
		},
		MessageID: "10",
		Text:      "| h1 | h2 |\n| --- | :---: |\n| x | y |",
		Entities: []platform.TextEntity{
			{
				Type:   platform.TextEntityTypeTable,
				Offset: 0,
				Length: runeLen("| h1 | h2 |\n| --- | :---: |\n| x | y |"),
				Table: &platform.TextEntityTableMeta{
					GroupID: "table:1",
				},
			},
			{
				Type:   platform.TextEntityTypeTableRow,
				Offset: 0,
				Length: 11,
				Table: &platform.TextEntityTableMeta{
					GroupID: "table:1",
					Row:     0,
					Header:  true,
				},
			},
			{
				Type:   platform.TextEntityTypeTableRow,
				Offset: runeIndex("| h1 | h2 |\n| --- | :---: |\n| x | y |", "| x | y |"),
				Length: 9,
				Table: &platform.TextEntityTableMeta{
					GroupID: "table:1",
					Row:     1,
					Header:  false,
				},
			},
			{
				Type:   platform.TextEntityTypeBold,
				Offset: runeIndex("| h1 | h2 |\n| --- | :---: |\n| x | y |", "x"),
				Length: 1,
			},
		},
	}

	if err := dispatcher.EditMessage(context.Background(), request); err != nil {
		t.Fatalf("EditMessage failed: %v", err)
	}

	if rpc.lastEditRequest.Text != "h1 | h2\nx | y" {
		t.Fatalf("rendered text = %q, want %q", rpc.lastEditRequest.Text, "h1 | h2\nx | y")
	}
	bold, ok := findEntityByType(rpc.lastEditRequest.Entities, platform.TextEntityTypeBold)
	if !ok {
		t.Fatal("bold entity not found")
	}
	if bold.Offset != 8 || bold.Length != 1 {
		t.Fatalf("bold range = [%d,%d), want [8,9)", bold.Offset, bold.Offset+bold.Length)
	}
}

func TestOutboundDispatcherSendMessageReadabilityFallbackUsesOriginalPayload(t *testing.T) {
	t.Parallel()

	cache := NewPeerCache()
	cache.RememberConversation(
		ChatRef{ID: "42", Type: platform.ConversationTypeGroup},
		&tg.InputPeerChat{ChatID: 42},
	)

	rpc := &stubOutboundRPC{sendID: 901}
	dispatcher, err := newOutboundDispatcherWithRPC(rpc, cache)
	if err != nil {
		t.Fatalf("new dispatcher failed: %v", err)
	}

	request := platform.SendMessageRequest{
		Target: platform.OutboundTarget{
			Conversation: platform.Conversation{
				ID:   "42",
				Type: platform.ConversationTypeGroup,
			},
		},
		Text: "![alt](https://a)",
		Entities: []platform.TextEntity{
			{
				Type:   platform.TextEntityTypeImage,
				Offset: 0,
				Length: 17,
				Image: &platform.TextEntityImageMeta{
					URL: "https://a",
					Alt: "alt",
				},
			},
			{
				Type:   platform.TextEntityTypeImage,
				Offset: 2,
				Length: 5,
				Image: &platform.TextEntityImageMeta{
					URL: "https://b",
					Alt: "dup",
				},
			},
		},
	}

	if _, err := dispatcher.SendMessage(context.Background(), request); err != nil {
		t.Fatalf("SendMessage failed: %v", err)
	}

	if rpc.lastSendRequest.Text != request.Text {
		t.Fatalf("fallback text = %q, want %q", rpc.lastSendRequest.Text, request.Text)
	}
	if len(rpc.lastSendRequest.Entities) != len(request.Entities) {
		t.Fatalf("fallback entities len = %d, want %d", len(rpc.lastSendRequest.Entities), len(request.Entities))
	}
}

func TestOutboundDispatcherEditMessageMapsFloodWaitToTypedRateLimit(t *testing.T) {
	t.Parallel()

	cache := NewPeerCache()
	cache.RememberConversation(
		ChatRef{ID: "42", Type: platform.ConversationTypeGroup},
		&tg.InputPeerChat{ChatID: 42},
	)

	dispatcher, err := newOutboundDispatcherWithRPC(
		&stubOutboundRPC{
			editErr: tgerr.New(420, "FLOOD_WAIT_7"),
		},
		cache,
		WithSinkRef(platform.EventSink{Platform: DriverPlatform, ID: "tg-main"}),
	)
	if err != nil {
		t.Fatalf("new dispatcher failed: %v", err)
	}

	editErr := dispatcher.EditMessage(context.Background(), platform.EditMessageRequest{
		Target: platform.OutboundTarget{
			Conversation: platform.Conversation{
				ID:   "42",
				Type: platform.ConversationTypeGroup,
			},
		},
		MessageID: "10",
		Text:      "updated",
	})
	if editErr == nil {
		t.Fatal("edit error = nil, want outbound error")
	}

	outboundErr, ok := platform.AsOutboundError(editErr)
	if !ok {
		t.Fatalf("AsOutboundError(%v) = false, want true", editErr)
	}
	if outboundErr.Operation != platform.OutboundOperationEditMessage {
		t.Fatalf("operation = %s, want %s", outboundErr.Operation, platform.OutboundOperationEditMessage)
	}
	if outboundErr.Kind != platform.OutboundErrorKindRateLimited {
		t.Fatalf("kind = %s, want %s", outboundErr.Kind, platform.OutboundErrorKindRateLimited)
	}
	if outboundErr.Platform != DriverPlatform {
		t.Fatalf("platform = %s, want %s", outboundErr.Platform, DriverPlatform)
	}
	if outboundErr.SinkID != "tg-main" {
		t.Fatalf("sink_id = %s, want tg-main", outboundErr.SinkID)
	}
	if outboundErr.RetryAfter != 7*time.Second {
		t.Fatalf("retry_after = %s, want %s", outboundErr.RetryAfter, 7*time.Second)
	}
	if outboundErr.Code != 420 {
		t.Fatalf("code = %d, want 420", outboundErr.Code)
	}
	if outboundErr.Type != "FLOOD_WAIT" {
		t.Fatalf("type = %q, want %q", outboundErr.Type, "FLOOD_WAIT")
	}

	retryAfter, isRateLimited := platform.AsOutboundRateLimit(editErr)
	if !isRateLimited {
		t.Fatalf("AsOutboundRateLimit(%v) = false, want true", editErr)
	}
	if retryAfter != 7*time.Second {
		t.Fatalf("retry_after = %s, want %s", retryAfter, 7*time.Second)
	}
}

func TestOutboundDispatcherEditMessageMapsRPCErrorToTypedKind(t *testing.T) {
	t.Parallel()

	cache := NewPeerCache()
	cache.RememberConversation(
		ChatRef{ID: "42", Type: platform.ConversationTypeGroup},
		&tg.InputPeerChat{ChatID: 42},
	)

	rpcErr := tgerr.New(400, "MESSAGE_ID_INVALID")
	dispatcher, err := newOutboundDispatcherWithRPC(
		&stubOutboundRPC{
			editErr: rpcErr,
		},
		cache,
		WithSinkRef(platform.EventSink{Platform: DriverPlatform, ID: "tg-main"}),
	)
	if err != nil {
		t.Fatalf("new dispatcher failed: %v", err)
	}

	editErr := dispatcher.EditMessage(context.Background(), platform.EditMessageRequest{
		Target: platform.OutboundTarget{
			Conversation: platform.Conversation{
				ID:   "42",
				Type: platform.ConversationTypeGroup,
			},
		},
		MessageID: "10",
		Text:      "updated",
	})
	if editErr == nil {
		t.Fatal("edit error = nil, want outbound error")
	}

	outboundErr, ok := platform.AsOutboundError(editErr)
	if !ok {
		t.Fatalf("AsOutboundError(%v) = false, want true", editErr)
	}
	if outboundErr.Kind != platform.OutboundErrorKindPermanent {
		t.Fatalf("kind = %s, want %s", outboundErr.Kind, platform.OutboundErrorKindPermanent)
	}
	if outboundErr.Code != 400 {
		t.Fatalf("code = %d, want 400", outboundErr.Code)
	}
	if outboundErr.Type != "MESSAGE_ID_INVALID" {
		t.Fatalf("type = %q, want %q", outboundErr.Type, "MESSAGE_ID_INVALID")
	}
	if !errors.Is(editErr, rpcErr) {
		t.Fatalf("errors.Is(err, rpcErr) = false, want true (err=%v)", editErr)
	}
}

func TestOutboundDispatcherSetReaction(t *testing.T) {
	t.Parallel()

	cache := NewPeerCache()
	cache.RememberConversation(
		ChatRef{ID: "42", Type: platform.ConversationTypeGroup},
		&tg.InputPeerChat{ChatID: 42},
	)

	tests := []struct {
		name                 string
		request              platform.SetReactionRequest
		wantErr              bool
		wantReactionLen      int
		wantReactionType     string
		wantCustomDocument   int64
		wantReactionEmoticon string
	}{
		{
			name: "add emoji reaction",
			request: platform.SetReactionRequest{
				Target: platform.OutboundTarget{
					Conversation: platform.Conversation{
						ID:   "42",
						Type: platform.ConversationTypeGroup,
					},
				},
				MessageID: "10",
				Emoji:     "👍",
				Action:    platform.ReactionActionAdd,
			},
			wantReactionLen:      1,
			wantReactionType:     "*tg.ReactionEmoji",
			wantReactionEmoticon: "👍",
		},
		{
			name: "add custom reaction",
			request: platform.SetReactionRequest{
				Target: platform.OutboundTarget{
					Conversation: platform.Conversation{
						ID:   "42",
						Type: platform.ConversationTypeGroup,
					},
				},
				MessageID: "10",
				Emoji:     "custom:123",
				Action:    platform.ReactionActionAdd,
			},
			wantReactionLen:    1,
			wantReactionType:   "*tg.ReactionCustomEmoji",
			wantCustomDocument: 123,
		},
		{
			name: "remove reaction",
			request: platform.SetReactionRequest{
				Target: platform.OutboundTarget{
					Conversation: platform.Conversation{
						ID:   "42",
						Type: platform.ConversationTypeGroup,
					},
				},
				MessageID: "10",
				Action:    platform.ReactionActionRemove,
			},
			wantReactionLen: 0,
		},
		{
			name: "invalid message id",
			request: platform.SetReactionRequest{
				Target: platform.OutboundTarget{
					Conversation: platform.Conversation{
						ID:   "42",
						Type: platform.ConversationTypeGroup,
					},
				},
				MessageID: "bad",
				Emoji:     "👍",
				Action:    platform.ReactionActionAdd,
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
		ChatRef{ID: "42", Type: platform.ConversationTypeChannel},
		&tg.InputPeerChannel{ChannelID: 42, AccessHash: 100},
	)

	tests := []struct {
		name    string
		request platform.DeleteMessageRequest
		wantErr bool
	}{
		{
			name: "revoke channel delete succeeds",
			request: platform.DeleteMessageRequest{
				Target: platform.OutboundTarget{
					Conversation: platform.Conversation{
						ID:   "42",
						Type: platform.ConversationTypeChannel,
					},
				},
				MessageID: "5",
				Revoke:    true,
			},
		},
		{
			name: "non-revoke channel delete fails",
			request: platform.DeleteMessageRequest{
				Target: platform.OutboundTarget{
					Conversation: platform.Conversation{
						ID:   "42",
						Type: platform.ConversationTypeChannel,
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

func TestOutboundDispatcherListSinks(t *testing.T) {
	t.Parallel()

	dispatcher, err := newOutboundDispatcherWithRPC(&stubOutboundRPC{}, NewPeerCache())
	if err != nil {
		t.Fatalf("new dispatcher failed: %v", err)
	}

	sinks, err := dispatcher.ListSinks(context.Background())
	if err != nil {
		t.Fatalf("list sinks failed: %v", err)
	}
	if len(sinks) != 1 {
		t.Fatalf("sinks len = %d, want 1", len(sinks))
	}
	if sinks[0].Platform != DriverPlatform {
		t.Fatalf("sink platform = %s, want %s", sinks[0].Platform, DriverPlatform)
	}

	dispatcherWithRef, err := newOutboundDispatcherWithRPC(
		&stubOutboundRPC{},
		NewPeerCache(),
		WithSinkRef(platform.EventSink{Platform: DriverPlatform, ID: "tg-main"}),
	)
	if err != nil {
		t.Fatalf("new dispatcher with ref failed: %v", err)
	}

	sinks, err = dispatcherWithRef.ListSinksByPlatform(context.Background(), DriverPlatform)
	if err != nil {
		t.Fatalf("list sinks by platform failed: %v", err)
	}
	if len(sinks) != 1 {
		t.Fatalf("platform sinks len = %d, want 1", len(sinks))
	}
	if sinks[0].ID != "tg-main" {
		t.Fatalf("sink id = %s, want tg-main", sinks[0].ID)
	}

	sinks, err = dispatcherWithRef.ListSinksByPlatform(context.Background(), platform.Platform("discord"))
	if err != nil {
		t.Fatalf("list sinks by mismatched platform failed: %v", err)
	}
	if len(sinks) != 0 {
		t.Fatalf("mismatched platform sinks len = %d, want 0", len(sinks))
	}
}

func TestOutboundDispatcherListSinksHonorsContext(t *testing.T) {
	t.Parallel()

	dispatcher, err := newOutboundDispatcherWithRPC(&stubOutboundRPC{}, NewPeerCache())
	if err != nil {
		t.Fatalf("new dispatcher failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := dispatcher.ListSinks(ctx); err == nil {
		t.Fatal("expected context cancellation error from list sinks")
	}
	if _, err := dispatcher.ListSinksByPlatform(ctx, DriverPlatform); err == nil {
		t.Fatal("expected context cancellation error from list sinks by platform")
	}
}

func TestMapOutboundTextEntities(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		text         string
		entities     []platform.TextEntity
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
			entities: []platform.TextEntity{
				{Type: platform.TextEntityTypeBold, Offset: 0, Length: 5},
			},
			wantLen:      1,
			wantTypeName: "*tg.MessageEntityBold",
			wantOffset:   0,
			wantLength:   5,
		},
		{
			name: "maps pre language",
			text: "fmt.Println()",
			entities: []platform.TextEntity{
				{Type: platform.TextEntityTypePre, Offset: 0, Length: 12, Language: "go"},
			},
			wantLen:      1,
			wantTypeName: "*tg.MessageEntityPre",
			wantOffset:   0,
			wantLength:   12,
		},
		{
			name: "maps utf16 offsets",
			text: "a😀b",
			entities: []platform.TextEntity{
				{Type: platform.TextEntityTypeBold, Offset: 1, Length: 1},
			},
			wantLen:      1,
			wantTypeName: "*tg.MessageEntityBold",
			wantOffset:   1,
			wantLength:   2,
		},
		{
			name: "invalid range fails",
			text: "hello",
			entities: []platform.TextEntity{
				{Type: platform.TextEntityTypeBold, Offset: 0, Length: 6},
			},
			wantErr: true,
		},
		{
			name: "markdown structural entity skipped",
			text: "## hello",
			entities: []platform.TextEntity{
				{
					Type:   platform.TextEntityTypeHeading,
					Offset: 0,
					Length: 8,
					Heading: &platform.TextEntityHeadingMeta{
						Level: 2,
					},
				},
			},
			wantLen: 0,
		},
		{
			name: "unsupported type fails",
			text: "hello",
			entities: []platform.TextEntity{
				{Type: "fancy", Offset: 0, Length: 5},
			},
			wantErr: true,
		},
		{
			name: "mention_name unsupported",
			text: "alice",
			entities: []platform.TextEntity{
				{Type: platform.TextEntityTypeMentionName, Offset: 0, Length: 5, MentionUserID: "123"},
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
	editErr         error
	deleteErr       error
	reactionErr     error
	lastSendRequest platform.SendMessageRequest
	lastEditRequest platform.EditMessageRequest
	sendCalls       int
	editCalls       int
	deleteCalls     int
	reactionCalls   int
	reactions       []tg.ReactionClass
}

func (s *stubOutboundRPC) SendText(
	_ context.Context,
	_ tg.InputPeerClass,
	request platform.SendMessageRequest,
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
	request platform.EditMessageRequest,
) error {
	s.editCalls++
	s.lastEditRequest = request
	if s.editErr != nil {
		return s.editErr
	}

	return nil
}

func (s *stubOutboundRPC) DeleteMessage(
	_ context.Context,
	peer tg.InputPeerClass,
	_ int,
	revoke bool,
) error {
	s.deleteCalls++
	if s.deleteErr != nil {
		return s.deleteErr
	}
	if _, isChannel := peer.(*tg.InputPeerChannel); isChannel && !revoke {
		return platform.ErrOutboundUnsupported
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
	if s.reactionErr != nil {
		return s.reactionErr
	}

	return nil
}

func (s *stubOutboundRPC) RestrictMember(
	_ context.Context,
	_ tg.InputPeerClass,
	_ tg.InputPeerClass,
	_ tg.ChatBannedRights,
) error {
	return nil
}

func typeName(value any) string {
	return fmt.Sprintf("%T", value)
}
