package telegram

import (
	"context"
	"fmt"

	"github.com/gotd/td/tg"
)

type gotdMessagesReactionsAPI interface {
	MessagesGetMessagesReactions(ctx context.Context, request *tg.MessagesGetMessagesReactionsRequest) (tg.UpdatesClass, error)
	MessagesGetMessageReactionsList(
		ctx context.Context,
		request *tg.MessagesGetMessageReactionsListRequest,
	) (*tg.MessagesMessageReactionsList, error)
}

const (
	reactionListPageLimit = 100
	reactionListMaxPages  = 20
)

// GotdMessageReactionResolver resolves full reaction state using Telegram API methods.
type GotdMessageReactionResolver struct {
	api gotdMessagesReactionsAPI
}

// NewGotdMessageReactionResolver creates a resolver backed by a gotd tg.Client-like API.
func NewGotdMessageReactionResolver(api gotdMessagesReactionsAPI) (*GotdMessageReactionResolver, error) {
	if api == nil {
		return nil, fmt.Errorf("new gotd message reaction resolver: nil api")
	}

	return &GotdMessageReactionResolver{
		api: api,
	}, nil
}

// ResolveMessageReactionCounts returns current reaction counts for one message by emoji.
func (r *GotdMessageReactionResolver) ResolveMessageReactionCounts(
	ctx context.Context,
	peer tg.InputPeerClass,
	messageID int,
) (map[string]int, error) {
	if r == nil || r.api == nil {
		return nil, fmt.Errorf("resolve message reaction counts: nil resolver")
	}
	if peer == nil {
		return nil, fmt.Errorf("resolve message reaction counts: nil peer")
	}
	if messageID == 0 {
		return nil, fmt.Errorf("resolve message reaction counts: empty message id")
	}

	updates, err := r.api.MessagesGetMessagesReactions(ctx, &tg.MessagesGetMessagesReactionsRequest{
		Peer: peer,
		ID:   []int{messageID},
	})
	if err != nil {
		return nil, fmt.Errorf("messages.getMessagesReactions: %w", err)
	}

	counts, found := reactionCountsFromUpdatesClass(updates, messageID)
	if found && len(counts) > 0 {
		return counts, nil
	}

	listCounts, listFound, err := r.resolveCountsFromReactionList(ctx, peer, messageID)
	if err != nil {
		if found {
			return counts, nil
		}

		return nil, fmt.Errorf("messages.getMessageReactionsList: %w", err)
	}
	if listFound {
		return listCounts, nil
	}
	if found {
		return counts, nil
	}

	return nil, nil
}

func reactionCountsFromUpdatesClass(updates tg.UpdatesClass, messageID int) (map[string]int, bool) {
	switch typed := updates.(type) {
	case *tg.Updates:
		return reactionCountsFromUpdateSlice(typed.Updates, messageID)
	case *tg.UpdatesCombined:
		return reactionCountsFromUpdateSlice(typed.Updates, messageID)
	case *tg.UpdateShort:
		return reactionCountsFromSingleUpdate(typed.Update, messageID)
	case *tg.UpdatesTooLong:
		return nil, false
	default:
		return nil, false
	}
}

func reactionCountsFromUpdateSlice(updates []tg.UpdateClass, messageID int) (map[string]int, bool) {
	for _, update := range updates {
		counts, found := reactionCountsFromSingleUpdate(update, messageID)
		if found {
			return counts, true
		}
	}

	return nil, false
}

func reactionCountsFromSingleUpdate(update tg.UpdateClass, messageID int) (map[string]int, bool) {
	if update == nil {
		return nil, false
	}

	switch typed := update.(type) {
	case *tg.UpdateMessageReactions:
		if typed.MsgID != messageID {
			return nil, false
		}

		return reactionCountsFromMessageReactions(typed.Reactions), true
	case *tg.UpdateEditMessage:
		return reactionCountsFromMessageClass(typed.Message, messageID)
	case *tg.UpdateEditChannelMessage:
		return reactionCountsFromMessageClass(typed.Message, messageID)
	case *tg.UpdateNewMessage:
		return reactionCountsFromMessageClass(typed.Message, messageID)
	case *tg.UpdateNewChannelMessage:
		return reactionCountsFromMessageClass(typed.Message, messageID)
	default:
		return nil, false
	}
}

func reactionCountsFromMessageClass(message tg.MessageClass, messageID int) (map[string]int, bool) {
	typed, ok := message.(*tg.Message)
	if !ok || typed == nil || typed.ID != messageID {
		return nil, false
	}

	counts, present := reactionCountsFromMessage(typed)
	if !present {
		return nil, false
	}

	return counts, true
}

func reactionCountsFromMessageReactions(reactions tg.MessageReactions) map[string]int {
	counts := make(map[string]int, len(reactions.Results))
	for _, result := range reactions.Results {
		emoji := reactionToEmoji(result.Reaction)
		if emoji == "" || result.Count <= 0 {
			continue
		}
		counts[emoji] = result.Count
	}

	return counts
}

func (r *GotdMessageReactionResolver) resolveCountsFromReactionList(
	ctx context.Context,
	peer tg.InputPeerClass,
	messageID int,
) (map[string]int, bool, error) {
	counts := map[string]int{}
	offset := ""

	for page := 0; page < reactionListMaxPages; page++ {
		request := &tg.MessagesGetMessageReactionsListRequest{
			Peer:  peer,
			ID:    messageID,
			Limit: reactionListPageLimit,
		}
		if offset != "" {
			request.SetOffset(offset)
		}

		list, err := r.api.MessagesGetMessageReactionsList(ctx, request)
		if err != nil {
			return nil, false, fmt.Errorf("get message reactions list page %d: %w", page+1, err)
		}
		for _, reaction := range list.Reactions {
			emoji := reactionToEmoji(reaction.Reaction)
			if emoji == "" {
				continue
			}
			counts[emoji]++
		}

		nextOffset, ok := list.GetNextOffset()
		if !ok || nextOffset == "" {
			return counts, true, nil
		}
		offset = nextOffset
	}

	// Avoid returning partial counts if pagination did not complete.
	return nil, false, nil
}
