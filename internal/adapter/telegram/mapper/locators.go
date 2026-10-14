package mapper

import (
	"encoding/json"
	"fmt"

	"github.com/gotd/td/tg"
)

const (
	// NamespaceCommon is Telegram's account-wide message ID space, used by
	// private chats and basic groups.
	NamespaceCommon = "common"
	// NamespaceChannel is a per-channel message ID space, used by channels and
	// supergroups.
	NamespaceChannel = "channel"
)

// MessageLocator identifies a Telegram message; it is the Data of the
// ProviderLocator on events and relations.
type MessageLocator struct {
	// Namespace is NamespaceCommon or NamespaceChannel.
	Namespace string `json:"namespace"`
	// Peer is the conversation, as returned by PeerID.
	Peer string `json:"peer"`
	// MessageID is Telegram's message ID.
	MessageID int `json:"message_id"`
}

// MediaLocator identifies Telegram media so the driver can download it later.
type MediaLocator struct {
	// Kind is "photo" or "document".
	Kind string `json:"kind"`
	// ID is the media ID.
	ID int64 `json:"id"`
	// AccessHash authorizes access to the media.
	AccessHash int64 `json:"access_hash"`
	// FileReference is Telegram's expiring file reference.
	FileReference []byte `json:"file_reference"`
	// DCID is the data center holding the media.
	DCID int `json:"dc_id"`
	// ThumbSize selects the photo size to download: the largest one.
	ThumbSize string `json:"thumb_size,omitempty"`
}

// EncodeMessageLocator encodes a message locator.
func EncodeMessageLocator(l MessageLocator) ([]byte, error) { return marshal("message locator", l) }

// DecodeMessageLocator decodes a message locator.
func DecodeMessageLocator(data []byte) (MessageLocator, error) {
	var l MessageLocator
	return l, unmarshal("message locator", data, &l)
}

// EncodeMediaLocator encodes a media locator.
func EncodeMediaLocator(l MediaLocator) ([]byte, error) { return marshal("media locator", l) }

// DecodeMediaLocator decodes a media locator.
func DecodeMediaLocator(data []byte) (MediaLocator, error) {
	var l MediaLocator
	return l, unmarshal("media locator", data, &l)
}

func marshal(what string, v any) ([]byte, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("encode %s: %w", what, err)
	}
	return data, nil
}

func unmarshal(what string, data []byte, v any) error {
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("decode %s: %w", what, err)
	}
	return nil
}

// Routed reports whether update is a kind the adapter persists and routes, and
// its position: the box ("common" or "channel:<id>") and pts. (box, pts)
// identifies the update uniquely, so it serves as the inbox dedup key.
func Routed(update tg.UpdateClass) (box string, pts int, ok bool) {
	switch u := update.(type) {
	case *tg.UpdateNewMessage:
		return NamespaceCommon, u.Pts, true
	case *tg.UpdateEditMessage:
		return NamespaceCommon, u.Pts, true
	case *tg.UpdateDeleteMessages:
		return NamespaceCommon, u.Pts, true
	case *tg.UpdateNewChannelMessage:
		return channelBox(u.Message), u.Pts, channelBox(u.Message) != ""
	case *tg.UpdateEditChannelMessage:
		return channelBox(u.Message), u.Pts, channelBox(u.Message) != ""
	case *tg.UpdateDeleteChannelMessages:
		return fmt.Sprintf("channel:%d", u.ChannelID), u.Pts, true
	default:
		return "", 0, false
	}
}

func channelBox(message tg.MessageClass) string {
	var peer tg.PeerClass
	switch m := message.(type) {
	case *tg.Message:
		peer = m.PeerID
	case *tg.MessageService:
		peer = m.PeerID
	case *tg.MessageEmpty:
		peer, _ = m.GetPeerID()
	}
	if channel, ok := peer.(*tg.PeerChannel); ok {
		return fmt.Sprintf("channel:%d", channel.ChannelID)
	}
	return ""
}
