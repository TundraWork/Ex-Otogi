package telegram

import (
	"context"
	"fmt"
	"time"

	gotdtelegram "github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
)

const defaultGotdUpdateBuffer = 1024

// GotdClientAdapter adapts gotd telegram.Client to GotdUserbotClient.
type GotdClientAdapter struct {
	client *gotdtelegram.Client
}

// NewGotdClientAdapter creates a gotd userbot client adapter.
func NewGotdClientAdapter(client *gotdtelegram.Client) (*GotdClientAdapter, error) {
	if client == nil {
		return nil, fmt.Errorf("new gotd client adapter: nil client")
	}

	return &GotdClientAdapter{
		client: client,
	}, nil
}

// Run starts the gotd client lifecycle.
func (c *GotdClientAdapter) Run(ctx context.Context, fn func(runCtx context.Context) error) error {
	if fn == nil {
		return fmt.Errorf("run gotd client adapter: nil callback")
	}
	if err := c.client.Run(ctx, fn); err != nil {
		return fmt.Errorf("run gotd client adapter: %w", err)
	}

	return nil
}

// GotdUpdateChannel is a gotd update handler and raw stream implementation.
type GotdUpdateChannel struct {
	buffer  int
	updates chan any
}

// NewGotdUpdateChannel creates a stream bridge between gotd updates and adapter source.
func NewGotdUpdateChannel(buffer int) (*GotdUpdateChannel, error) {
	if buffer <= 0 {
		buffer = defaultGotdUpdateBuffer
	}

	return &GotdUpdateChannel{
		buffer:  buffer,
		updates: make(chan any, buffer),
	}, nil
}

// Updates returns the active stream channel.
func (s *GotdUpdateChannel) Updates(ctx context.Context) (<-chan any, error) {
	if ctx == nil {
		return nil, fmt.Errorf("gotd update channel: nil context")
	}
	if s.updates == nil {
		return nil, fmt.Errorf("gotd update channel: not initialized")
	}

	return s.updates, nil
}

// Handle flattens gotd update batches and forwards each unit to the active stream.
func (s *GotdUpdateChannel) Handle(ctx context.Context, updates tg.UpdatesClass) error {
	batch, err := flattenGotdUpdates(updates)
	if err != nil {
		return fmt.Errorf("handle gotd updates: %w", err)
	}

	for _, item := range batch {
		if err := s.publish(ctx, item); err != nil {
			return fmt.Errorf("handle gotd updates publish: %w", err)
		}
	}

	return nil
}

func (s *GotdUpdateChannel) publish(ctx context.Context, item gotdUpdateEnvelope) error {
	if s.updates == nil {
		return fmt.Errorf("publish gotd update: stream not initialized")
	}

	select {
	case <-ctx.Done():
		return fmt.Errorf("publish gotd update: %w", ctx.Err())
	case s.updates <- item:
		return nil
	}
}

func flattenGotdUpdates(updates tg.UpdatesClass) ([]gotdUpdateEnvelope, error) {
	if updates == nil {
		return nil, fmt.Errorf("flatten gotd updates: nil updates")
	}

	switch typed := updates.(type) {
	case *tg.Updates:
		return flattenGotdBatch(typed.Updates, typed.Date, typed.Users, typed.Chats)
	case *tg.UpdatesCombined:
		return flattenGotdBatch(typed.Updates, typed.Date, typed.Users, typed.Chats)
	case *tg.UpdateShort:
		return flattenSingleGotdUpdate(typed.Update, intToTimeUTC(typed.Date), nil, nil)
	case *tg.UpdateShortMessage:
		return flattenShortMessage(typed)
	case *tg.UpdateShortChatMessage:
		return flattenShortChatMessage(typed)
	case *tg.UpdatesTooLong:
		return nil, nil
	default:
		return nil, fmt.Errorf("flatten gotd updates %s: unsupported container", updates.TypeName())
	}
}

func flattenGotdBatch(
	updates []tg.UpdateClass,
	date int,
	users []tg.UserClass,
	chats []tg.ChatClass,
) ([]gotdUpdateEnvelope, error) {
	occurredAt := intToTimeUTC(date)
	usersByID := indexGotdUsers(users)
	chatsByID := indexGotdChats(chats)

	batch := make([]gotdUpdateEnvelope, 0, len(updates))
	for _, update := range updates {
		items, err := flattenSingleGotdUpdate(update, occurredAt, usersByID, chatsByID)
		if err != nil {
			return nil, fmt.Errorf("flatten gotd batch: %w", err)
		}

		batch = append(batch, items...)
	}

	return batch, nil
}

func flattenSingleGotdUpdate(
	update tg.UpdateClass,
	occurredAt time.Time,
	usersByID map[int64]*tg.User,
	chatsByID map[int64]gotdChatInfo,
) ([]gotdUpdateEnvelope, error) {
	if update == nil {
		return nil, fmt.Errorf("flatten gotd update: nil update")
	}

	switch typed := update.(type) {
	case *tg.UpdateDeleteMessages:
		if len(typed.Messages) == 0 {
			return nil, nil
		}
		items := make([]gotdUpdateEnvelope, 0, len(typed.Messages))
		for _, messageID := range typed.Messages {
			clone := *typed
			clone.Messages = []int{messageID}
			items = append(items, gotdUpdateEnvelope{
				update:      &clone,
				occurredAt:  occurredAt,
				usersByID:   usersByID,
				chatsByID:   chatsByID,
				updateClass: update.TypeName(),
			})
		}
		return items, nil
	case *tg.UpdateDeleteChannelMessages:
		if len(typed.Messages) == 0 {
			return nil, nil
		}
		items := make([]gotdUpdateEnvelope, 0, len(typed.Messages))
		for _, messageID := range typed.Messages {
			clone := *typed
			clone.Messages = []int{messageID}
			items = append(items, gotdUpdateEnvelope{
				update:      &clone,
				occurredAt:  occurredAt,
				usersByID:   usersByID,
				chatsByID:   chatsByID,
				updateClass: update.TypeName(),
			})
		}
		return items, nil
	case *tg.UpdateBotMessageReaction:
		return flattenBotReactionUpdate(typed, occurredAt, usersByID, chatsByID)
	default:
		return []gotdUpdateEnvelope{
			{
				update:      update,
				occurredAt:  occurredAt,
				usersByID:   usersByID,
				chatsByID:   chatsByID,
				updateClass: update.TypeName(),
			},
		}, nil
	}
}

func flattenShortMessage(update *tg.UpdateShortMessage) ([]gotdUpdateEnvelope, error) {
	if update == nil {
		return nil, fmt.Errorf("flatten short message: nil update")
	}

	message := &tg.Message{
		ID:      update.ID,
		PeerID:  &tg.PeerUser{UserID: update.UserID},
		Date:    update.Date,
		Message: update.Message,
	}
	message.SetFromID(&tg.PeerUser{UserID: update.UserID})
	if replyTo, ok := update.GetReplyTo(); ok {
		message.SetReplyTo(replyTo)
	}
	if entities, ok := update.GetEntities(); ok {
		message.SetEntities(entities)
	}

	return []gotdUpdateEnvelope{
		{
			update: &tg.UpdateNewMessage{
				Message:  message,
				Pts:      update.Pts,
				PtsCount: update.PtsCount,
			},
			occurredAt:  intToTimeUTC(update.Date),
			updateClass: update.TypeName(),
		},
	}, nil
}

func flattenShortChatMessage(update *tg.UpdateShortChatMessage) ([]gotdUpdateEnvelope, error) {
	if update == nil {
		return nil, fmt.Errorf("flatten short chat message: nil update")
	}

	message := &tg.Message{
		ID:      update.ID,
		PeerID:  &tg.PeerChat{ChatID: update.ChatID},
		Date:    update.Date,
		Message: update.Message,
	}
	message.SetFromID(&tg.PeerUser{UserID: update.FromID})
	if replyTo, ok := update.GetReplyTo(); ok {
		message.SetReplyTo(replyTo)
	}
	if entities, ok := update.GetEntities(); ok {
		message.SetEntities(entities)
	}

	return []gotdUpdateEnvelope{
		{
			update: &tg.UpdateNewMessage{
				Message:  message,
				Pts:      update.Pts,
				PtsCount: update.PtsCount,
			},
			occurredAt:  intToTimeUTC(update.Date),
			updateClass: update.TypeName(),
		},
	}, nil
}

func flattenBotReactionUpdate(
	update *tg.UpdateBotMessageReaction,
	occurredAt time.Time,
	usersByID map[int64]*tg.User,
	chatsByID map[int64]gotdChatInfo,
) ([]gotdUpdateEnvelope, error) {
	if update == nil {
		return nil, fmt.Errorf("flatten bot reaction update: nil update")
	}

	oldSet := mapReactionsToSet(update.OldReactions)
	newSet := mapReactionsToSet(update.NewReactions)

	deltas := make([]gotdReactionDelta, 0)
	for emoji := range newSet {
		if _, exists := oldSet[emoji]; exists {
			continue
		}
		deltas = append(deltas, gotdReactionDelta{
			action:    UpdateTypeReactionAdd,
			messageID: update.MsgID,
			emoji:     emoji,
			actor:     update.Actor,
			peer:      update.Peer,
		})
	}
	for emoji := range oldSet {
		if _, exists := newSet[emoji]; exists {
			continue
		}
		deltas = append(deltas, gotdReactionDelta{
			action:    UpdateTypeReactionRemove,
			messageID: update.MsgID,
			emoji:     emoji,
			actor:     update.Actor,
			peer:      update.Peer,
		})
	}

	items := make([]gotdUpdateEnvelope, 0, len(deltas))
	for _, delta := range deltas {
		delta := delta
		items = append(items, gotdUpdateEnvelope{
			update:      update,
			occurredAt:  occurredAt,
			usersByID:   usersByID,
			chatsByID:   chatsByID,
			updateClass: update.TypeName(),
			reaction:    &delta,
		})
	}

	return items, nil
}

func mapReactionsToSet(reactions []tg.ReactionClass) map[string]struct{} {
	out := make(map[string]struct{}, len(reactions))
	for _, reaction := range reactions {
		emoji := reactionToEmoji(reaction)
		if emoji == "" {
			continue
		}
		out[emoji] = struct{}{}
	}

	return out
}
