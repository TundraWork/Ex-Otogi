package telegram

import (
	"testing"

	"ex-otogi/pkg/otogi/platform"

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
				kind:      platform.ConversationTypeGroup,
				inputPeer: &tg.InputPeerChat{ChatID: 10},
			},
			20: {
				kind:      platform.ConversationTypeGroup,
				inputPeer: &tg.InputPeerChannel{ChannelID: 20, AccessHash: 2020},
			},
			30: {
				kind:      platform.ConversationTypeChannel,
				inputPeer: &tg.InputPeerChannel{ChannelID: 30, AccessHash: 3030},
			},
		},
	})

	tests := []struct {
		name         string
		conversation platform.Conversation
		wantType     string
		wantErr      bool
	}{
		{
			name: "resolve private user",
			conversation: platform.Conversation{
				ID:   "7",
				Type: platform.ConversationTypePrivate,
			},
			wantType: "*tg.InputPeerUser",
		},
		{
			name: "resolve group chat",
			conversation: platform.Conversation{
				ID:   "10",
				Type: platform.ConversationTypeGroup,
			},
			wantType: "*tg.InputPeerChat",
		},
		{
			name: "resolve megagroup channel as group",
			conversation: platform.Conversation{
				ID:   "20",
				Type: platform.ConversationTypeGroup,
			},
			wantType: "*tg.InputPeerChannel",
		},
		{
			name: "resolve group fallback from channel key",
			conversation: platform.Conversation{
				ID:   "20",
				Type: platform.ConversationTypeChannel,
			},
			wantType: "*tg.InputPeerChannel",
		},
		{
			name: "resolve channel",
			conversation: platform.Conversation{
				ID:   "30",
				Type: platform.ConversationTypeChannel,
			},
			wantType: "*tg.InputPeerChannel",
		},
		{
			name: "unknown conversation",
			conversation: platform.Conversation{
				ID:   "999",
				Type: platform.ConversationTypeGroup,
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
		ChatRef{ID: "55", Type: platform.ConversationTypeGroup},
		&tg.InputPeerChannel{ChannelID: 55, AccessHash: 555},
	)

	groupPeer, err := cache.Resolve(platform.Conversation{
		ID:   "55",
		Type: platform.ConversationTypeGroup,
	})
	if err != nil {
		t.Fatalf("resolve group peer failed: %v", err)
	}
	if got := typeName(groupPeer); got != "*tg.InputPeerChannel" {
		t.Fatalf("group peer type = %s, want *tg.InputPeerChannel", got)
	}

	channelPeer, err := cache.Resolve(platform.Conversation{
		ID:   "55",
		Type: platform.ConversationTypeChannel,
	})
	if err != nil {
		t.Fatalf("resolve channel peer fallback failed: %v", err)
	}
	if got := typeName(channelPeer); got != "*tg.InputPeerChannel" {
		t.Fatalf("channel peer type = %s, want *tg.InputPeerChannel", got)
	}
}
