package mapper

import (
	"fmt"

	"github.com/gotd/td/tg"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// PeerID converts a Telegram peer into the canonical protocol-neutral tagged identifier.
func PeerID(peer tg.PeerClass) (string, error) {
	switch value := peer.(type) {
	case *tg.PeerUser:
		if value == nil || value.UserID <= 0 {
			break
		}
		return fmt.Sprintf("user:%d", value.UserID), nil
	case *tg.PeerChat:
		if value == nil || value.ChatID <= 0 {
			break
		}
		return fmt.Sprintf("chat:%d", value.ChatID), nil
	case *tg.PeerChannel:
		if value == nil || value.ChannelID <= 0 {
			break
		}
		return fmt.Sprintf("channel:%d", value.ChannelID), nil
	}
	return "", fmt.Errorf("invalid Telegram peer: %w", otogi.ErrInvalid)
}

func messageFlow(message *tg.Message) (otogi.Direction, string, error) {
	if message == nil {
		return "", "", fmt.Errorf("nil Telegram message: %w", otogi.ErrInvalid)
	}
	direction := otogi.DirectionIncoming
	if message.Out {
		direction = otogi.DirectionOutgoing
	}
	from, ok := message.GetFromID()
	if !ok {
		return direction, "", nil
	}
	actor, err := PeerID(from)
	if err != nil {
		return "", "", err
	}
	return direction, actor, nil
}
