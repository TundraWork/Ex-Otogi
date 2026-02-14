package telegram

import (
	"fmt"
	"strconv"
	"sync"

	"ex-otogi/pkg/otogi"

	"github.com/gotd/td/tg"
)

// PeerCache stores Telegram input peers discovered from inbound updates.
//
// It is used by outbound dispatch to resolve neutral otogi conversation targets
// back into Telegram input peers.
type PeerCache struct {
	mu             sync.RWMutex
	byConversation map[string]tg.InputPeerClass
}

// NewPeerCache creates an empty, concurrency-safe Telegram peer cache.
func NewPeerCache() *PeerCache {
	return &PeerCache{
		byConversation: make(map[string]tg.InputPeerClass),
	}
}

// RememberEnvelope ingests entity data attached to one gotd update envelope.
func (c *PeerCache) RememberEnvelope(envelope gotdUpdateEnvelope) {
	if c == nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	for userID, user := range envelope.usersByID {
		if user == nil {
			continue
		}
		peer := user.AsInputPeer()
		if peer == nil {
			continue
		}
		c.byConversation[conversationKey(otogi.ConversationTypePrivate, strconv.FormatInt(userID, 10))] = cloneInputPeer(peer)
	}

	for id, chat := range envelope.chatsByID {
		if chat.inputPeer == nil {
			continue
		}

		idStr := strconv.FormatInt(id, 10)
		c.byConversation[conversationKey(chat.kind, idStr)] = cloneInputPeer(chat.inputPeer)

		// Megagroups surface as "group" in neutral events but use channel peers for outbound RPC.
		if chat.kind == otogi.ConversationTypeGroup {
			if _, isChannel := chat.inputPeer.(*tg.InputPeerChannel); isChannel {
				c.byConversation[conversationKey(otogi.ConversationTypeChannel, idStr)] = cloneInputPeer(chat.inputPeer)
			}
		}
	}
}

// RememberConversation stores one explicit conversation-to-peer mapping.
func (c *PeerCache) RememberConversation(chat ChatRef, peer tg.InputPeerClass) {
	if c == nil || peer == nil || chat.ID == "" {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.byConversation[conversationKey(chat.Type, chat.ID)] = cloneInputPeer(peer)

	if chat.Type == otogi.ConversationTypeGroup {
		if _, isChannel := peer.(*tg.InputPeerChannel); isChannel {
			c.byConversation[conversationKey(otogi.ConversationTypeChannel, chat.ID)] = cloneInputPeer(peer)
		}
	}
}

// Resolve returns an input peer for an outbound target conversation.
func (c *PeerCache) Resolve(conversation otogi.Conversation) (tg.InputPeerClass, error) {
	if c == nil {
		return nil, fmt.Errorf("resolve peer: nil cache")
	}
	if conversation.ID == "" || conversation.Type == "" {
		return nil, fmt.Errorf("resolve peer: invalid conversation")
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	if peer, ok := c.byConversation[conversationKey(conversation.Type, conversation.ID)]; ok {
		return cloneInputPeer(peer), nil
	}

	switch conversation.Type {
	case otogi.ConversationTypePrivate:
		// No alternate peer kind exists for private conversations.
	case otogi.ConversationTypeGroup:
		if peer, ok := c.byConversation[conversationKey(otogi.ConversationTypeChannel, conversation.ID)]; ok {
			return cloneInputPeer(peer), nil
		}
	case otogi.ConversationTypeChannel:
		if peer, ok := c.byConversation[conversationKey(otogi.ConversationTypeGroup, conversation.ID)]; ok {
			return cloneInputPeer(peer), nil
		}
	default:
		// Unknown conversation kinds have no compatibility fallback.
	}

	return nil, fmt.Errorf("resolve peer: conversation %s/%s not found", conversation.Type, conversation.ID)
}

func conversationKey(conversationType otogi.ConversationType, id string) string {
	return string(conversationType) + ":" + id
}

func cloneInputPeer(peer tg.InputPeerClass) tg.InputPeerClass {
	switch typed := peer.(type) {
	case *tg.InputPeerUser:
		copyPeer := *typed
		return &copyPeer
	case *tg.InputPeerChat:
		copyPeer := *typed
		return &copyPeer
	case *tg.InputPeerChannel:
		copyPeer := *typed
		return &copyPeer
	case *tg.InputPeerSelf:
		copyPeer := *typed
		return &copyPeer
	default:
		return peer
	}
}
