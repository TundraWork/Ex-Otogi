package telegram

import (
	"testing"

	"ex-otogi/pkg/otogi"

	"github.com/gotd/td/tg"
)

func TestPeerCacheRememberEnvelopeAndResolve(t *testing.T) {
	t.Parallel()

	user := &tg.User{ID: 7}
	user.SetAccessHash(77)

	cache := NewPeerCache()
	cache.RememberEnvelope(gotdUpdateEnvelope{
		usersByID: map[int64]*tg.User{
			7: user,
		},
		chatsByID: map[int64]gotdChatInfo{
			10: {
				kind:      otogi.ConversationTypeGroup,
				inputPeer: &tg.InputPeerChat{ChatID: 10},
			},
			20: {
				kind:      otogi.ConversationTypeGroup,
				inputPeer: &tg.InputPeerChannel{ChannelID: 20, AccessHash: 2020},
			},
			30: {
				kind:      otogi.ConversationTypeChannel,
				inputPeer: &tg.InputPeerChannel{ChannelID: 30, AccessHash: 3030},
			},
		},
	})

	tests := []struct {
		name         string
		conversation otogi.Conversation
		wantType     string
		wantErr      bool
	}{
		{
			name: "resolve private user",
			conversation: otogi.Conversation{
				ID:   "7",
				Type: otogi.ConversationTypePrivate,
			},
			wantType: "*tg.InputPeerUser",
		},
		{
			name: "resolve group chat",
			conversation: otogi.Conversation{
				ID:   "10",
				Type: otogi.ConversationTypeGroup,
			},
			wantType: "*tg.InputPeerChat",
		},
		{
			name: "resolve megagroup channel as group",
			conversation: otogi.Conversation{
				ID:   "20",
				Type: otogi.ConversationTypeGroup,
			},
			wantType: "*tg.InputPeerChannel",
		},
		{
			name: "resolve group fallback from channel key",
			conversation: otogi.Conversation{
				ID:   "20",
				Type: otogi.ConversationTypeChannel,
			},
			wantType: "*tg.InputPeerChannel",
		},
		{
			name: "resolve channel",
			conversation: otogi.Conversation{
				ID:   "30",
				Type: otogi.ConversationTypeChannel,
			},
			wantType: "*tg.InputPeerChannel",
		},
		{
			name: "unknown conversation",
			conversation: otogi.Conversation{
				ID:   "999",
				Type: otogi.ConversationTypeGroup,
			},
			wantErr: true,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			peer, err := cache.Resolve(testCase.conversation)
			if testCase.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := typeName(peer); got != testCase.wantType {
				t.Fatalf("peer type = %s, want %s", got, testCase.wantType)
			}
		})
	}
}

func TestPeerCacheRememberConversation(t *testing.T) {
	t.Parallel()

	cache := NewPeerCache()
	cache.RememberConversation(
		ChatRef{ID: "55", Type: otogi.ConversationTypeGroup},
		&tg.InputPeerChannel{ChannelID: 55, AccessHash: 555},
	)

	groupPeer, err := cache.Resolve(otogi.Conversation{
		ID:   "55",
		Type: otogi.ConversationTypeGroup,
	})
	if err != nil {
		t.Fatalf("resolve group peer failed: %v", err)
	}
	if got := typeName(groupPeer); got != "*tg.InputPeerChannel" {
		t.Fatalf("group peer type = %s, want *tg.InputPeerChannel", got)
	}

	channelPeer, err := cache.Resolve(otogi.Conversation{
		ID:   "55",
		Type: otogi.ConversationTypeChannel,
	})
	if err != nil {
		t.Fatalf("resolve channel peer fallback failed: %v", err)
	}
	if got := typeName(channelPeer); got != "*tg.InputPeerChannel" {
		t.Fatalf("channel peer type = %s, want *tg.InputPeerChannel", got)
	}
}
