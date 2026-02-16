package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"ex-otogi/pkg/otogi"

	"github.com/gotd/td/tg"
)

const (
	gotdUnknownConversationID     = "unknown"
	gotdUnknownActorID            = "unknown"
	defaultReactionResolveTimeout = 2 * time.Second
	reactionResolveMaxAttempts    = 3
	reactionResolveRetryDelay     = 120 * time.Millisecond
)

// DefaultGotdUpdateMapper maps gotd updates into adapter DTO updates.
type DefaultGotdUpdateMapper struct {
	peerCache              *PeerCache
	reactionCache          *reactionCountCache
	messageSnapshotCache   *messageSnapshotCache
	reactionResolver       MessageReactionResolver
	reactionResolveTimeout time.Duration
	logger                 *slog.Logger
}

// GotdUpdateMapperOption mutates DefaultGotdUpdateMapper behavior.
type GotdUpdateMapperOption func(*DefaultGotdUpdateMapper)

// WithPeerCache records entity-derived peer mappings for outbound dispatch.
func WithPeerCache(cache *PeerCache) GotdUpdateMapperOption {
	return func(mapper *DefaultGotdUpdateMapper) {
		if cache != nil {
			mapper.peerCache = cache
		}
	}
}

// WithMapperLogger configures mapper-level structured diagnostics.
func WithMapperLogger(logger *slog.Logger) GotdUpdateMapperOption {
	return func(mapper *DefaultGotdUpdateMapper) {
		if logger != nil {
			mapper.logger = logger
		}
	}
}

// WithMessageReactionResolver configures reaction lookup for ambiguous reaction edits.
func WithMessageReactionResolver(resolver MessageReactionResolver) GotdUpdateMapperOption {
	return func(mapper *DefaultGotdUpdateMapper) {
		if resolver != nil {
			mapper.reactionResolver = resolver
		}
	}
}

// WithReactionResolveTimeout configures the timeout used for reaction resolver calls.
func WithReactionResolveTimeout(timeout time.Duration) GotdUpdateMapperOption {
	return func(mapper *DefaultGotdUpdateMapper) {
		if timeout > 0 {
			mapper.reactionResolveTimeout = timeout
		}
	}
}

// NewDefaultGotdUpdateMapper creates the default gotd mapper.
func NewDefaultGotdUpdateMapper(options ...GotdUpdateMapperOption) DefaultGotdUpdateMapper {
	mapper := DefaultGotdUpdateMapper{
		reactionCache:          newReactionCountCache(),
		messageSnapshotCache:   newMessageSnapshotCache(),
		reactionResolveTimeout: defaultReactionResolveTimeout,
	}
	for _, option := range options {
		option(&mapper)
	}

	return mapper
}

// Map converts a gotd raw update value into an adapter update.
func (m DefaultGotdUpdateMapper) Map(ctx context.Context, raw any) (Update, bool, error) {
	select {
	case <-ctx.Done():
		return Update{}, false, fmt.Errorf("map gotd update context: %w", ctx.Err())
	default:
	}

	envelope, err := normalizeGotdRaw(raw)
	if err != nil {
		return Update{}, false, fmt.Errorf("map gotd raw update: %w", err)
	}
	m.rememberEnvelope(envelope)

	if envelope.reaction != nil {
		return m.mapReactionDelta(ctx, envelope)
	}

	switch update := envelope.update.(type) {
	case *tg.UpdateNewMessage:
		return m.mapNewMessage(update, envelope)
	case *tg.UpdateNewChannelMessage:
		return m.mapNewMessage(&tg.UpdateNewMessage{
			Message:  update.Message,
			Pts:      update.Pts,
			PtsCount: update.PtsCount,
		}, envelope)
	case *tg.UpdateEditMessage:
		return m.mapEditMessage(ctx, update.Message, envelope)
	case *tg.UpdateEditChannelMessage:
		return m.mapEditMessage(ctx, update.Message, envelope)
	case *tg.UpdateDeleteMessages:
		return m.mapDeleteMessages(update, envelope)
	case *tg.UpdateDeleteChannelMessages:
		return m.mapDeleteChannelMessages(update, envelope)
	case *tg.UpdateChatParticipantAdd:
		return m.mapChatParticipantAdd(update, envelope)
	case *tg.UpdateChatParticipantDelete:
		return m.mapChatParticipantDelete(update, envelope)
	case *tg.UpdateChatParticipantAdmin:
		return m.mapChatParticipantAdmin(update, envelope)
	case *tg.UpdateChatParticipant:
		return m.mapChatParticipant(update, envelope)
	case *tg.UpdateChannelParticipant:
		return m.mapChannelParticipant(update, envelope)
	default:
		return Update{}, false, nil
	}
}

func (m DefaultGotdUpdateMapper) rememberEnvelope(envelope gotdUpdateEnvelope) {
	if m.peerCache != nil {
		m.peerCache.RememberEnvelope(envelope)
	}
}

func (m DefaultGotdUpdateMapper) rememberConversationPeer(chat ChatRef, peer tg.InputPeerClass) {
	if m.peerCache != nil {
		m.peerCache.RememberConversation(chat, peer)
	}
}

func (m DefaultGotdUpdateMapper) logDebug(ctx context.Context, msg string, args ...any) {
	if m.logger == nil {
		return
	}

	m.logger.DebugContext(ctx, msg, args...)
}

func normalizeGotdRaw(raw any) (gotdUpdateEnvelope, error) {
	switch typed := raw.(type) {
	case gotdUpdateEnvelope:
		return typed, nil
	case *gotdUpdateEnvelope:
		if typed == nil {
			return gotdUpdateEnvelope{}, fmt.Errorf("nil envelope")
		}
		return *typed, nil
	case tg.UpdateClass:
		if typed == nil {
			return gotdUpdateEnvelope{}, fmt.Errorf("nil update class")
		}
		return gotdUpdateEnvelope{
			update:      typed,
			occurredAt:  time.Now().UTC(),
			updateClass: typed.TypeName(),
		}, nil
	default:
		return gotdUpdateEnvelope{}, fmt.Errorf("unsupported raw type %T", raw)
	}
}

func (m DefaultGotdUpdateMapper) mapNewMessage(
	update *tg.UpdateNewMessage,
	envelope gotdUpdateEnvelope,
) (Update, bool, error) {
	if update == nil {
		return Update{}, false, fmt.Errorf("map new message: nil update")
	}

	switch message := update.Message.(type) {
	case *tg.Message:
		return m.mapMessage(message, envelope)
	case *tg.MessageService:
		return m.mapServiceMessage(message, envelope)
	default:
		return Update{}, false, nil
	}
}

func (m DefaultGotdUpdateMapper) mapMessage(
	message *tg.Message,
	envelope gotdUpdateEnvelope,
) (Update, bool, error) {
	if message == nil {
		return Update{}, false, fmt.Errorf("map message: nil message")
	}

	chat := resolveChatFromPeer(message.PeerID, envelope)
	actor := resolveActorFromPeer(message.FromID, envelope)
	if actor.ID == gotdUnknownActorID {
		actor = resolveActorFromPeer(message.PeerID, envelope)
	}

	payload := mapMessagePayload(message)

	occurredAt := intToTimeUTC(message.Date)
	if occurredAt.IsZero() {
		occurredAt = envelope.occurredAt
	}
	m.rememberConversationPeer(chat, resolveInputPeerFromPeer(message.PeerID, envelope))
	m.rememberMessageReactionCounts(chat.ID, message)
	m.rememberMessageSnapshot(chat.ID, message)

	return Update{
		ID:         composeUpdateID(UpdateTypeMessage, chat.ID, payload.ID, occurredAt),
		Type:       UpdateTypeMessage,
		OccurredAt: occurredAt,
		Chat:       chat,
		Actor:      actor,
		Message:    payload,
		Metadata:   newGotdMetadata(envelope),
	}, true, nil
}

func (m DefaultGotdUpdateMapper) mapServiceMessage(
	message *tg.MessageService,
	envelope gotdUpdateEnvelope,
) (Update, bool, error) {
	if message == nil {
		return Update{}, false, fmt.Errorf("map service message: nil message")
	}
	if message.Action == nil {
		return Update{}, false, nil
	}

	chat := resolveChatFromPeer(message.PeerID, envelope)
	actor := resolveActorFromPeer(message.FromID, envelope)
	occurredAt := intToTimeUTC(message.Date)
	if occurredAt.IsZero() {
		occurredAt = envelope.occurredAt
	}
	m.rememberConversationPeer(chat, resolveInputPeerFromPeer(message.PeerID, envelope))

	switch action := message.Action.(type) {
	case *tg.MessageActionChatAddUser:
		if len(action.Users) == 0 {
			return Update{}, false, nil
		}
		member := resolveActorByUserID(action.Users[0], envelope)

		return Update{
			ID:         composeUpdateID(UpdateTypeMemberJoin, chat.ID, member.ID, occurredAt),
			Type:       UpdateTypeMemberJoin,
			OccurredAt: occurredAt,
			Chat:       chat,
			Actor:      actor,
			Member: &MemberPayload{
				Member:   member,
				Inviter:  actorPointer(actor),
				Reason:   "service_action_chat_add_user",
				JoinedAt: occurredAt,
			},
			Metadata: newGotdMetadata(envelope),
		}, true, nil
	case *tg.MessageActionChatDeleteUser:
		member := resolveActorByUserID(action.UserID, envelope)

		return Update{
			ID:         composeUpdateID(UpdateTypeMemberLeave, chat.ID, member.ID, occurredAt),
			Type:       UpdateTypeMemberLeave,
			OccurredAt: occurredAt,
			Chat:       chat,
			Actor:      actor,
			Member: &MemberPayload{
				Member: member,
				Reason: "service_action_chat_delete_user",
			},
			Metadata: newGotdMetadata(envelope),
		}, true, nil
	case *tg.MessageActionChatJoinedByLink:
		member := actor
		inviter := resolveActorByUserID(action.InviterID, envelope)

		return Update{
			ID:         composeUpdateID(UpdateTypeMemberJoin, chat.ID, member.ID, occurredAt),
			Type:       UpdateTypeMemberJoin,
			OccurredAt: occurredAt,
			Chat:       chat,
			Actor:      actor,
			Member: &MemberPayload{
				Member:   member,
				Inviter:  actorPointer(inviter),
				Reason:   "service_action_chat_joined_by_link",
				JoinedAt: occurredAt,
			},
			Metadata: newGotdMetadata(envelope),
		}, true, nil
	case *tg.MessageActionChatJoinedByRequest:
		member := actor

		return Update{
			ID:         composeUpdateID(UpdateTypeMemberJoin, chat.ID, member.ID, occurredAt),
			Type:       UpdateTypeMemberJoin,
			OccurredAt: occurredAt,
			Chat:       chat,
			Actor:      actor,
			Member: &MemberPayload{
				Member:   member,
				Reason:   "service_action_chat_joined_by_request",
				JoinedAt: occurredAt,
			},
			Metadata: newGotdMetadata(envelope),
		}, true, nil
	case *tg.MessageActionChatMigrateTo:
		return Update{
			ID:         composeUpdateID(UpdateTypeMigration, chat.ID, strconv.FormatInt(action.ChannelID, 10), occurredAt),
			Type:       UpdateTypeMigration,
			OccurredAt: occurredAt,
			Chat:       chat,
			Actor:      actor,
			Migration: &MigrationPayload{
				FromChatID: chat.ID,
				ToChatID:   strconv.FormatInt(action.ChannelID, 10),
				Reason:     "service_action_chat_migrate_to",
			},
			Metadata: newGotdMetadata(envelope),
		}, true, nil
	case *tg.MessageActionChannelMigrateFrom:
		return Update{
			ID:         composeUpdateID(UpdateTypeMigration, strconv.FormatInt(action.ChatID, 10), chat.ID, occurredAt),
			Type:       UpdateTypeMigration,
			OccurredAt: occurredAt,
			Chat:       chat,
			Actor:      actor,
			Migration: &MigrationPayload{
				FromChatID: strconv.FormatInt(action.ChatID, 10),
				ToChatID:   chat.ID,
				Reason:     "service_action_channel_migrate_from",
			},
			Metadata: newGotdMetadata(envelope),
		}, true, nil
	default:
		return Update{}, false, nil
	}
}

func (m DefaultGotdUpdateMapper) mapEditMessage(
	ctx context.Context,
	message tg.MessageClass,
	envelope gotdUpdateEnvelope,
) (Update, bool, error) {
	typed, ok := message.(*tg.Message)
	if !ok {
		return Update{}, false, nil
	}

	if reactionUpdate, mappedAsReaction, err := m.mapReactionFromEditedMessage(ctx, typed, envelope); err != nil {
		return Update{}, false, fmt.Errorf("map edit message as reaction: %w", err)
	} else if mappedAsReaction {
		return reactionUpdate, true, nil
	}
	if shouldSuppressAmbiguousReactionEdit(typed) {
		m.logDebug(ctx,
			"telegram mapper suppressed ambiguous reaction-like edit",
			"update_class", envelope.updateClass,
			"message_id", typed.ID,
			"edit_hide", typed.EditHide,
		)

		return Update{}, false, nil
	}

	chat := resolveChatFromPeer(typed.PeerID, envelope)
	actor := resolveActorFromPeer(typed.FromID, envelope)
	if actor.ID == gotdUnknownActorID {
		actor = resolveActorFromPeer(typed.PeerID, envelope)
	}
	occurredAt := resolveMutationOccurredAt(typed, envelope.occurredAt)
	m.rememberConversationPeer(chat, resolveInputPeerFromPeer(typed.PeerID, envelope))
	before := m.loadMessageSnapshot(chat.ID, typed.ID)
	payload := mapMessagePayload(typed)
	after := snapshotPayloadFromMessage(typed)
	m.rememberMessageReactionCounts(chat.ID, typed)
	m.rememberMessageSnapshot(chat.ID, typed)

	return Update{
		ID:         composeUpdateID(UpdateTypeEdit, chat.ID, strconv.Itoa(typed.ID), occurredAt),
		Type:       UpdateTypeEdit,
		OccurredAt: occurredAt,
		Chat:       chat,
		Actor:      actor,
		Message:    payload,
		Edit: &EditPayload{
			MessageID: strconv.Itoa(typed.ID),
			ChangedAt: timePointer(occurredAt),
			Before:    before,
			After:     after,
			Reason:    "telegram_edit_update",
		},
		Metadata: newGotdMetadata(envelope),
	}, true, nil
}

func (m DefaultGotdUpdateMapper) mapReactionFromEditedMessage(
	ctx context.Context,
	message *tg.Message,
	envelope gotdUpdateEnvelope,
) (Update, bool, error) {
	if message == nil {
		return Update{}, false, fmt.Errorf("map reaction from edited message: nil message")
	}

	chat := resolveChatFromPeer(message.PeerID, envelope)
	isLikelyReactionUpdate := isLikelyReactionOnlyEdit(message)
	reactionPayloadPresent, reactionResultsCount, reactionRecentCount := reactionPayloadStats(message)
	countCacheKnown := m.reactionCountCacheSnapshot(chat.ID, message.ID) != nil

	var (
		delta                      gotdReactionDelta
		ok                         bool
		deltaFrom                  = "none"
		resolverAttempted          bool
		nonLikelySnapshotKnown     bool
		nonLikelySnapshotUnchanged bool
	)
	if isLikelyReactionUpdate {
		delta, ok = reactionDeltaFromEditedMessage(message)
		if ok {
			deltaFrom = "edited_message"
		}
		if !ok {
			delta, ok = m.reactionDeltaFromCountCache(chat.ID, message)
			if ok {
				deltaFrom = "count_cache"
			}
		}
	} else {
		delta, ok = m.reactionDeltaFromCountCache(chat.ID, message)
		if ok {
			deltaFrom = "count_cache"
		}
	}
	if !ok {
		shouldAttemptResolver := isLikelyReactionUpdate
		if !shouldAttemptResolver {
			nonLikelySnapshotKnown, nonLikelySnapshotUnchanged = m.isMessageSnapshotUnchanged(chat.ID, message)
			shouldAttemptResolver = nonLikelySnapshotKnown && nonLikelySnapshotUnchanged
		}
		if shouldAttemptResolver {
			resolverAttempted = true
			delta, ok = m.reactionDeltaFromResolver(ctx, chat, message, envelope)
			if ok {
				deltaFrom = "resolver"
			}
		}
	}
	m.logDebug(ctx,
		"telegram mapper edited-message reaction classification",
		"update_class", envelope.updateClass,
		"chat_id", chat.ID,
		"message_id", message.ID,
		"is_likely_reaction_only_edit", isLikelyReactionUpdate,
		"mapped_as_reaction", ok,
		"delta_source", deltaFrom,
		"delta_action", delta.action,
		"delta_emoji", delta.emoji,
		"resolver_attempted", resolverAttempted,
		"count_cache_known", countCacheKnown,
		"reaction_payload_present", reactionPayloadPresent,
		"reaction_results_count", reactionResultsCount,
		"reaction_recent_count", reactionRecentCount,
		"non_likely_snapshot_known", nonLikelySnapshotKnown,
		"non_likely_snapshot_unchanged", nonLikelySnapshotUnchanged,
	)
	if !ok {
		if shouldRememberUnmappedReactionEditCounts(message, isLikelyReactionUpdate) {
			m.rememberMessageReactionCounts(chat.ID, message)
		}
		return Update{}, false, nil
	}
	if delta.peer == nil {
		delta.peer = message.PeerID
	}

	actor := resolveActorFromPeer(delta.actor, envelope)
	occurredAt := resolveMutationOccurredAt(message, envelope.occurredAt)
	m.rememberConversationPeer(chat, resolveInputPeerFromPeer(delta.peer, envelope))
	m.rememberMessageReactionCounts(chat.ID, message)
	m.logDebug(ctx,
		"telegram mapper classified edited message as reaction",
		"update_class", envelope.updateClass,
		"chat_id", chat.ID,
		"message_id", delta.messageID,
		"delta_action", delta.action,
		"delta_emoji", delta.emoji,
	)

	return Update{
		ID:         composeUpdateID(delta.action, chat.ID, strconv.Itoa(delta.messageID), delta.emoji, occurredAt),
		Type:       delta.action,
		OccurredAt: occurredAt,
		Chat:       chat,
		Actor:      actor,
		Reaction: &ReactionPayload{
			MessageID: strconv.Itoa(delta.messageID),
			Emoji:     delta.emoji,
		},
		Metadata: newGotdMetadata(envelope),
	}, true, nil
}

func (m DefaultGotdUpdateMapper) mapDeleteMessages(
	update *tg.UpdateDeleteMessages,
	envelope gotdUpdateEnvelope,
) (Update, bool, error) {
	if update == nil || len(update.Messages) == 0 {
		return Update{}, false, nil
	}

	messageID := update.Messages[0]
	occurredAt := envelope.occurredAt
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}

	return Update{
		ID:         composeUpdateID(UpdateTypeDelete, gotdUnknownConversationID, strconv.Itoa(messageID), occurredAt),
		Type:       UpdateTypeDelete,
		OccurredAt: occurredAt,
		Chat: ChatRef{
			ID:   gotdUnknownConversationID,
			Type: otogi.ConversationTypePrivate,
		},
		Actor: ActorRef{ID: gotdUnknownActorID},
		Delete: &DeletePayload{
			MessageID: strconv.Itoa(messageID),
			Reason:    "telegram_delete_update",
		},
		Metadata: newGotdMetadata(envelope),
	}, true, nil
}

func (m DefaultGotdUpdateMapper) mapDeleteChannelMessages(
	update *tg.UpdateDeleteChannelMessages,
	envelope gotdUpdateEnvelope,
) (Update, bool, error) {
	if update == nil || len(update.Messages) == 0 {
		return Update{}, false, nil
	}

	chat := resolveChatByChannelID(update.ChannelID, envelope)
	occurredAt := envelope.occurredAt
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	m.rememberConversationPeer(chat, resolveInputPeerByChannelID(update.ChannelID, envelope))

	return Update{
		ID:         composeUpdateID(UpdateTypeDelete, chat.ID, strconv.Itoa(update.Messages[0]), occurredAt),
		Type:       UpdateTypeDelete,
		OccurredAt: occurredAt,
		Chat:       chat,
		Actor:      ActorRef{ID: gotdUnknownActorID},
		Delete: &DeletePayload{
			MessageID: strconv.Itoa(update.Messages[0]),
			Reason:    "telegram_delete_channel_update",
		},
		Metadata: newGotdMetadata(envelope),
	}, true, nil
}

func (m DefaultGotdUpdateMapper) mapReactionDelta(
	ctx context.Context,
	envelope gotdUpdateEnvelope,
) (Update, bool, error) {
	delta := envelope.reaction
	if delta == nil {
		return Update{}, false, fmt.Errorf("map reaction delta: nil delta")
	}
	if delta.emoji == "" {
		return Update{}, false, nil
	}

	chat := resolveChatFromPeer(delta.peer, envelope)
	actor := resolveActorFromPeer(delta.actor, envelope)
	occurredAt := envelope.occurredAt
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	m.rememberConversationPeer(chat, resolveInputPeerFromPeer(delta.peer, envelope))
	m.applyReactionDeltaToCountCache(chat.ID, *delta)
	m.logDebug(ctx,
		"telegram mapper mapped explicit reaction delta",
		"update_class", envelope.updateClass,
		"chat_id", chat.ID,
		"message_id", delta.messageID,
		"delta_action", delta.action,
		"delta_emoji", delta.emoji,
	)

	return Update{
		ID:         composeUpdateID(delta.action, chat.ID, strconv.Itoa(delta.messageID), delta.emoji, occurredAt),
		Type:       delta.action,
		OccurredAt: occurredAt,
		Chat:       chat,
		Actor:      actor,
		Reaction: &ReactionPayload{
			MessageID: strconv.Itoa(delta.messageID),
			Emoji:     delta.emoji,
		},
		Metadata: newGotdMetadata(envelope),
	}, true, nil
}

func (m DefaultGotdUpdateMapper) mapChatParticipantAdd(
	update *tg.UpdateChatParticipantAdd,
	envelope gotdUpdateEnvelope,
) (Update, bool, error) {
	if update == nil {
		return Update{}, false, fmt.Errorf("map chat participant add: nil update")
	}

	chat := resolveChatByChatID(update.ChatID, envelope)
	actor := resolveActorByUserID(update.InviterID, envelope)
	member := resolveActorByUserID(update.UserID, envelope)
	occurredAt := intToTimeUTC(update.Date)
	if occurredAt.IsZero() {
		occurredAt = envelope.occurredAt
	}
	m.rememberConversationPeer(chat, resolveInputPeerByChatID(update.ChatID))

	return Update{
		ID:         composeUpdateID(UpdateTypeMemberJoin, chat.ID, member.ID, occurredAt),
		Type:       UpdateTypeMemberJoin,
		OccurredAt: occurredAt,
		Chat:       chat,
		Actor:      actor,
		Member: &MemberPayload{
			Member:   member,
			Inviter:  actorPointer(actor),
			Reason:   "update_chat_participant_add",
			JoinedAt: occurredAt,
		},
		Metadata: newGotdMetadata(envelope),
	}, true, nil
}

func (m DefaultGotdUpdateMapper) mapChatParticipantDelete(
	update *tg.UpdateChatParticipantDelete,
	envelope gotdUpdateEnvelope,
) (Update, bool, error) {
	if update == nil {
		return Update{}, false, fmt.Errorf("map chat participant delete: nil update")
	}

	chat := resolveChatByChatID(update.ChatID, envelope)
	member := resolveActorByUserID(update.UserID, envelope)
	occurredAt := envelope.occurredAt
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	m.rememberConversationPeer(chat, resolveInputPeerByChatID(update.ChatID))

	return Update{
		ID:         composeUpdateID(UpdateTypeMemberLeave, chat.ID, member.ID, occurredAt),
		Type:       UpdateTypeMemberLeave,
		OccurredAt: occurredAt,
		Chat:       chat,
		Actor:      member,
		Member: &MemberPayload{
			Member: member,
			Reason: "update_chat_participant_delete",
		},
		Metadata: newGotdMetadata(envelope),
	}, true, nil
}

func (m DefaultGotdUpdateMapper) mapChatParticipantAdmin(
	update *tg.UpdateChatParticipantAdmin,
	envelope gotdUpdateEnvelope,
) (Update, bool, error) {
	if update == nil {
		return Update{}, false, fmt.Errorf("map chat participant admin: nil update")
	}

	chat := resolveChatByChatID(update.ChatID, envelope)
	member := resolveActorByUserID(update.UserID, envelope)
	occurredAt := envelope.occurredAt
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	m.rememberConversationPeer(chat, resolveInputPeerByChatID(update.ChatID))

	oldRole := "member"
	newRole := "member"
	if update.IsAdmin {
		newRole = "admin"
	} else {
		oldRole = "admin"
	}

	return Update{
		ID:         composeUpdateID(UpdateTypeRole, chat.ID, member.ID, occurredAt),
		Type:       UpdateTypeRole,
		OccurredAt: occurredAt,
		Chat:       chat,
		Actor:      member,
		Role: &RolePayload{
			MemberID:  member.ID,
			OldRole:   oldRole,
			NewRole:   newRole,
			ChangedBy: member,
		},
		Metadata: newGotdMetadata(envelope),
	}, true, nil
}

func (m DefaultGotdUpdateMapper) mapChatParticipant(
	update *tg.UpdateChatParticipant,
	envelope gotdUpdateEnvelope,
) (Update, bool, error) {
	if update == nil {
		return Update{}, false, fmt.Errorf("map chat participant: nil update")
	}

	chat := resolveChatByChatID(update.ChatID, envelope)
	actor := resolveActorByUserID(update.ActorID, envelope)
	member := resolveActorByUserID(update.UserID, envelope)
	occurredAt := intToTimeUTC(update.Date)
	if occurredAt.IsZero() {
		occurredAt = envelope.occurredAt
	}
	m.rememberConversationPeer(chat, resolveInputPeerByChatID(update.ChatID))

	prevParticipant, prevExists := update.GetPrevParticipant()
	newParticipant, newExists := update.GetNewParticipant()

	if !prevExists && newExists {
		return Update{
			ID:         composeUpdateID(UpdateTypeMemberJoin, chat.ID, member.ID, occurredAt),
			Type:       UpdateTypeMemberJoin,
			OccurredAt: occurredAt,
			Chat:       chat,
			Actor:      actor,
			Member: &MemberPayload{
				Member:   member,
				Inviter:  actorPointer(actor),
				Reason:   "update_chat_participant_join",
				JoinedAt: occurredAt,
			},
			Metadata: newGotdMetadata(envelope),
		}, true, nil
	}

	if prevExists && !newExists {
		return Update{
			ID:         composeUpdateID(UpdateTypeMemberLeave, chat.ID, member.ID, occurredAt),
			Type:       UpdateTypeMemberLeave,
			OccurredAt: occurredAt,
			Chat:       chat,
			Actor:      actor,
			Member: &MemberPayload{
				Member: member,
				Reason: "update_chat_participant_leave",
			},
			Metadata: newGotdMetadata(envelope),
		}, true, nil
	}

	if prevExists && newExists {
		oldRole := chatParticipantRole(prevParticipant)
		newRole := chatParticipantRole(newParticipant)
		if oldRole != newRole {
			return Update{
				ID:         composeUpdateID(UpdateTypeRole, chat.ID, member.ID, occurredAt),
				Type:       UpdateTypeRole,
				OccurredAt: occurredAt,
				Chat:       chat,
				Actor:      actor,
				Role: &RolePayload{
					MemberID:  member.ID,
					OldRole:   oldRole,
					NewRole:   newRole,
					ChangedBy: actor,
				},
				Metadata: newGotdMetadata(envelope),
			}, true, nil
		}
	}

	return Update{}, false, nil
}

func (m DefaultGotdUpdateMapper) mapChannelParticipant(
	update *tg.UpdateChannelParticipant,
	envelope gotdUpdateEnvelope,
) (Update, bool, error) {
	if update == nil {
		return Update{}, false, fmt.Errorf("map channel participant: nil update")
	}

	chat := resolveChatByChannelID(update.ChannelID, envelope)
	actor := resolveActorByUserID(update.ActorID, envelope)
	member := resolveActorByUserID(update.UserID, envelope)
	occurredAt := intToTimeUTC(update.Date)
	if occurredAt.IsZero() {
		occurredAt = envelope.occurredAt
	}
	m.rememberConversationPeer(chat, resolveInputPeerByChannelID(update.ChannelID, envelope))

	prevParticipant, prevExists := update.GetPrevParticipant()
	newParticipant, newExists := update.GetNewParticipant()

	oldRole := channelParticipantRole(prevParticipant)
	newRole := channelParticipantRole(newParticipant)
	oldActive := isChannelRoleActive(oldRole)
	newActive := isChannelRoleActive(newRole)

	switch {
	case !prevExists && newActive:
		return Update{
			ID:         composeUpdateID(UpdateTypeMemberJoin, chat.ID, member.ID, occurredAt),
			Type:       UpdateTypeMemberJoin,
			OccurredAt: occurredAt,
			Chat:       chat,
			Actor:      actor,
			Member: &MemberPayload{
				Member:   member,
				Inviter:  actorPointer(actor),
				Reason:   "update_channel_participant_join",
				JoinedAt: occurredAt,
			},
			Metadata: newGotdMetadata(envelope),
		}, true, nil
	case prevExists && oldActive && (!newExists || !newActive):
		return Update{
			ID:         composeUpdateID(UpdateTypeMemberLeave, chat.ID, member.ID, occurredAt),
			Type:       UpdateTypeMemberLeave,
			OccurredAt: occurredAt,
			Chat:       chat,
			Actor:      actor,
			Member: &MemberPayload{
				Member: member,
				Reason: "update_channel_participant_leave",
			},
			Metadata: newGotdMetadata(envelope),
		}, true, nil
	case prevExists && newExists && oldRole != newRole:
		return Update{
			ID:         composeUpdateID(UpdateTypeRole, chat.ID, member.ID, occurredAt),
			Type:       UpdateTypeRole,
			OccurredAt: occurredAt,
			Chat:       chat,
			Actor:      actor,
			Role: &RolePayload{
				MemberID:  member.ID,
				OldRole:   oldRole,
				NewRole:   newRole,
				ChangedBy: actor,
			},
			Metadata: newGotdMetadata(envelope),
		}, true, nil
	default:
		return Update{}, false, nil
	}
}

type gotdUpdateEnvelope struct {
	update      tg.UpdateClass
	occurredAt  time.Time
	usersByID   map[int64]*tg.User
	chatsByID   map[int64]gotdChatInfo
	updateClass string
	reaction    *gotdReactionDelta
}

type gotdReactionDelta struct {
	action    UpdateType
	messageID int
	emoji     string
	actor     tg.PeerClass
	peer      tg.PeerClass
}

type gotdChatInfo struct {
	title     string
	kind      otogi.ConversationType
	inputPeer tg.InputPeerClass
}

// MessageReactionResolver resolves full reaction counts for a specific message.
type MessageReactionResolver interface {
	// ResolveMessageReactionCounts returns current reaction counts by emoji for messageID.
	ResolveMessageReactionCounts(ctx context.Context, peer tg.InputPeerClass, messageID int) (map[string]int, error)
}

type reactionCountCache struct {
	mu        sync.Mutex
	byMessage map[string]map[string]int
}

type messageSnapshotCache struct {
	mu        sync.Mutex
	byMessage map[string]messageSnapshotRecord
}

type messageSnapshotRecord struct {
	fingerprint string
	snapshot    SnapshotPayload
}

func newReactionCountCache() *reactionCountCache {
	return &reactionCountCache{
		byMessage: make(map[string]map[string]int),
	}
}

func newMessageSnapshotCache() *messageSnapshotCache {
	return &messageSnapshotCache{
		byMessage: make(map[string]messageSnapshotRecord),
	}
}

func (m DefaultGotdUpdateMapper) rememberMessageReactionCounts(chatID string, message *tg.Message) {
	if m.reactionCache == nil || chatID == "" || message == nil {
		return
	}

	counts, present := reactionCountsFromMessage(message)
	if !present {
		counts = map[string]int{}
	}

	key := messageReactionCacheKey(chatID, message.ID)
	m.reactionCache.mu.Lock()
	m.reactionCache.byMessage[key] = cloneReactionCounts(counts)
	m.reactionCache.mu.Unlock()
}

func (m DefaultGotdUpdateMapper) rememberMessageSnapshot(chatID string, message *tg.Message) {
	if m.messageSnapshotCache == nil || chatID == "" || message == nil {
		return
	}
	snapshot := snapshotPayloadFromMessage(message)
	if snapshot == nil {
		return
	}

	key := messageReactionCacheKey(chatID, message.ID)
	m.messageSnapshotCache.mu.Lock()
	m.messageSnapshotCache.byMessage[key] = messageSnapshotRecord{
		fingerprint: messageSnapshotFingerprint(*snapshot),
		snapshot:    cloneSnapshotPayload(*snapshot),
	}
	m.messageSnapshotCache.mu.Unlock()
}

func (m DefaultGotdUpdateMapper) isMessageSnapshotUnchanged(chatID string, message *tg.Message) (known bool, unchanged bool) {
	if m.messageSnapshotCache == nil || chatID == "" || message == nil {
		return false, false
	}
	currentSnapshot := snapshotPayloadFromMessage(message)
	if currentSnapshot == nil {
		return false, false
	}

	key := messageReactionCacheKey(chatID, message.ID)
	current := messageSnapshotFingerprint(*currentSnapshot)

	m.messageSnapshotCache.mu.Lock()
	previous, exists := m.messageSnapshotCache.byMessage[key]
	m.messageSnapshotCache.mu.Unlock()
	if !exists {
		return false, false
	}

	return true, previous.fingerprint == current
}

func (m DefaultGotdUpdateMapper) loadMessageSnapshot(chatID string, messageID int) *SnapshotPayload {
	if m.messageSnapshotCache == nil || chatID == "" || messageID == 0 {
		return nil
	}

	key := messageReactionCacheKey(chatID, messageID)
	m.messageSnapshotCache.mu.Lock()
	record, exists := m.messageSnapshotCache.byMessage[key]
	m.messageSnapshotCache.mu.Unlock()
	if !exists {
		return nil
	}

	snapshot := cloneSnapshotPayload(record.snapshot)
	return &snapshot
}

func (m DefaultGotdUpdateMapper) reactionDeltaFromResolver(
	ctx context.Context,
	chat ChatRef,
	message *tg.Message,
	envelope gotdUpdateEnvelope,
) (gotdReactionDelta, bool) {
	if m.reactionResolver == nil || chat.ID == "" || message == nil {
		return gotdReactionDelta{}, false
	}

	peer := resolveInputPeerFromPeer(message.PeerID, envelope)
	if peer == nil && m.peerCache != nil {
		cachedPeer, err := m.peerCache.Resolve(otogi.Conversation{
			ID:   chat.ID,
			Type: chat.Type,
		})
		if err == nil {
			peer = cachedPeer
		} else {
			m.logDebug(ctx,
				"telegram mapper reaction resolver peer cache miss",
				"chat_id", chat.ID,
				"chat_type", chat.Type,
				"message_id", message.ID,
				"error", err,
			)
		}
	}
	if peer == nil {
		m.logDebug(ctx,
			"telegram mapper reaction resolver skipped",
			"chat_id", chat.ID,
			"chat_type", chat.Type,
			"message_id", message.ID,
			"reason", "missing_input_peer",
		)

		return gotdReactionDelta{}, false
	}

	resolveCtx := ctx
	cancel := func() {}
	if m.reactionResolveTimeout > 0 {
		resolveCtx, cancel = context.WithTimeout(ctx, m.reactionResolveTimeout)
	}
	defer cancel()

	previous := m.reactionCountCacheSnapshot(chat.ID, message.ID)
	if previous == nil {
		previous = map[string]int{}
	}

	var lastCounts map[string]int
	for attempt := 1; attempt <= reactionResolveMaxAttempts; attempt++ {
		counts, err := m.reactionResolver.ResolveMessageReactionCounts(resolveCtx, peer, message.ID)
		if err != nil {
			m.logDebug(ctx,
				"telegram mapper reaction resolver failed",
				"chat_id", chat.ID,
				"chat_type", chat.Type,
				"message_id", message.ID,
				"attempt", attempt,
				"error", err,
			)

			return gotdReactionDelta{}, false
		}
		if counts == nil {
			counts = map[string]int{}
		}
		lastCounts = counts

		emoji, action, ok := diffReactionCounts(previous, counts)
		if ok {
			m.setReactionCountCache(chat.ID, message.ID, counts)
			m.logDebug(ctx,
				"telegram mapper reaction resolver matched delta",
				"chat_id", chat.ID,
				"chat_type", chat.Type,
				"message_id", message.ID,
				"attempt", attempt,
				"delta_action", action,
				"delta_emoji", emoji,
			)

			return gotdReactionDelta{
				action:    action,
				messageID: message.ID,
				emoji:     emoji,
				peer:      message.PeerID,
			}, true
		}

		if attempt >= reactionResolveMaxAttempts {
			break
		}
		timer := time.NewTimer(reactionResolveRetryDelay)
		select {
		case <-resolveCtx.Done():
			timer.Stop()
			attempt = reactionResolveMaxAttempts
		case <-timer.C:
		}
	}
	if lastCounts != nil {
		m.setReactionCountCache(chat.ID, message.ID, lastCounts)
	}

	return gotdReactionDelta{}, false
}

func (m DefaultGotdUpdateMapper) setReactionCountCache(chatID string, messageID int, counts map[string]int) {
	if m.reactionCache == nil || chatID == "" || messageID == 0 {
		return
	}

	key := messageReactionCacheKey(chatID, messageID)
	m.reactionCache.mu.Lock()
	m.reactionCache.byMessage[key] = cloneReactionCounts(counts)
	m.reactionCache.mu.Unlock()
}

func (m DefaultGotdUpdateMapper) reactionDeltaFromCountCache(chatID string, message *tg.Message) (gotdReactionDelta, bool) {
	if m.reactionCache == nil || chatID == "" || message == nil {
		return gotdReactionDelta{}, false
	}

	counts, present := reactionCountsFromMessage(message)
	if !present {
		return gotdReactionDelta{}, false
	}

	key := messageReactionCacheKey(chatID, message.ID)

	m.reactionCache.mu.Lock()
	previous, known := m.reactionCache.byMessage[key]
	m.reactionCache.mu.Unlock()

	if !known {
		m.setReactionCountCache(chatID, message.ID, counts)
		return gotdReactionDelta{}, false
	}

	emoji, action, ok := diffReactionCounts(previous, counts)
	if !ok {
		return gotdReactionDelta{}, false
	}
	m.setReactionCountCache(chatID, message.ID, counts)

	return gotdReactionDelta{
		action:    action,
		messageID: message.ID,
		emoji:     emoji,
		peer:      message.PeerID,
	}, true
}

func (m DefaultGotdUpdateMapper) reactionCountCacheSnapshot(chatID string, messageID int) map[string]int {
	if m.reactionCache == nil || chatID == "" || messageID == 0 {
		return nil
	}

	key := messageReactionCacheKey(chatID, messageID)
	m.reactionCache.mu.Lock()
	counts, known := m.reactionCache.byMessage[key]
	m.reactionCache.mu.Unlock()
	if !known {
		return nil
	}

	return cloneReactionCounts(counts)
}

func (m DefaultGotdUpdateMapper) applyReactionDeltaToCountCache(chatID string, delta gotdReactionDelta) {
	if m.reactionCache == nil || chatID == "" || delta.messageID == 0 || delta.emoji == "" {
		return
	}

	key := messageReactionCacheKey(chatID, delta.messageID)
	m.reactionCache.mu.Lock()
	counts := cloneReactionCounts(m.reactionCache.byMessage[key])
	if counts == nil {
		counts = make(map[string]int)
	}
	switch delta.action {
	case UpdateTypeReactionAdd:
		counts[delta.emoji]++
	case UpdateTypeReactionRemove:
		current := counts[delta.emoji]
		if current <= 1 {
			delete(counts, delta.emoji)
		} else {
			counts[delta.emoji] = current - 1
		}
	default:
	}
	m.reactionCache.byMessage[key] = counts
	m.reactionCache.mu.Unlock()
}

func messageReactionCacheKey(chatID string, messageID int) string {
	return chatID + ":" + strconv.Itoa(messageID)
}

func reactionCountsFromMessage(message *tg.Message) (map[string]int, bool) {
	if message == nil {
		return nil, false
	}
	reactions, present := message.GetReactions()
	if !present {
		return nil, false
	}

	counts := make(map[string]int, len(reactions.Results))
	for _, result := range reactions.Results {
		emoji := reactionToEmoji(result.Reaction)
		if emoji == "" || result.Count <= 0 {
			continue
		}
		counts[emoji] = result.Count
	}

	return counts, true
}

func cloneReactionCounts(counts map[string]int) map[string]int {
	if len(counts) == 0 {
		return map[string]int{}
	}

	cloned := make(map[string]int, len(counts))
	for emoji, count := range counts {
		cloned[emoji] = count
	}

	return cloned
}

func diffReactionCounts(previous, current map[string]int) (string, UpdateType, bool) {
	emojiSet := make(map[string]struct{}, len(previous)+len(current))
	for emoji := range previous {
		emojiSet[emoji] = struct{}{}
	}
	for emoji := range current {
		emojiSet[emoji] = struct{}{}
	}

	candidateEmoji := ""
	candidateAction := UpdateType("")
	for emoji := range emojiSet {
		beforeCount := previous[emoji]
		afterCount := current[emoji]
		delta := afterCount - beforeCount
		if delta == 0 {
			continue
		}
		if delta != 1 && delta != -1 {
			return "", "", false
		}
		action := UpdateTypeReactionAdd
		if delta < 0 {
			action = UpdateTypeReactionRemove
		}
		if candidateEmoji != "" {
			return "", "", false
		}
		candidateEmoji = emoji
		candidateAction = action
	}

	if candidateEmoji == "" {
		return "", "", false
	}

	return candidateEmoji, candidateAction, true
}

func mapMessagePayload(message *tg.Message) *MessagePayload {
	if message == nil {
		return nil
	}

	payload := &MessagePayload{
		ID:        strconv.Itoa(message.ID),
		Text:      message.Message,
		Entities:  mapTextEntities(message.Entities),
		Media:     mapMessageMedia(message.Media),
		Reactions: mapMessageReactions(message),
	}
	if replyTo, ok := message.GetReplyTo(); ok {
		if header, ok := replyTo.(*tg.MessageReplyHeader); ok {
			if replyToMessageID, ok := header.GetReplyToMsgID(); ok {
				payload.ReplyToID = strconv.Itoa(replyToMessageID)
			}
			if threadID, ok := header.GetReplyToTopID(); ok {
				payload.ThreadID = strconv.Itoa(threadID)
			}
		}
	}

	return payload
}

func mapMessageReactions(message *tg.Message) []otogi.MessageReaction {
	if message == nil {
		return nil
	}
	reactions, present := message.GetReactions()
	if !present {
		return nil
	}

	counts := make(map[string]int, len(reactions.Results))
	for _, result := range reactions.Results {
		emoji := reactionToEmoji(result.Reaction)
		if emoji == "" || result.Count <= 0 {
			continue
		}
		counts[emoji] = result.Count
	}
	if len(counts) == 0 {
		return nil
	}

	emojis := make([]string, 0, len(counts))
	for emoji := range counts {
		emojis = append(emojis, emoji)
	}
	sort.Strings(emojis)

	projected := make([]otogi.MessageReaction, 0, len(emojis))
	for _, emoji := range emojis {
		projected = append(projected, otogi.MessageReaction{
			Emoji: emoji,
			Count: counts[emoji],
		})
	}

	return projected
}

func snapshotPayloadFromMessage(message *tg.Message) *SnapshotPayload {
	if message == nil {
		return nil
	}

	snapshot := SnapshotPayload{
		Text:  message.Message,
		Media: mapMessageMedia(message.Media),
	}

	return &snapshot
}

func cloneSnapshotPayload(snapshot SnapshotPayload) SnapshotPayload {
	cloned := snapshot
	if len(snapshot.Media) > 0 {
		cloned.Media = cloneMediaPayloads(snapshot.Media)
	} else {
		cloned.Media = nil
	}

	return cloned
}

func cloneMediaPayloads(media []MediaPayload) []MediaPayload {
	if len(media) == 0 {
		return nil
	}

	cloned := make([]MediaPayload, len(media))
	for idx, item := range media {
		attachment := item
		if item.Preview != nil {
			preview := *item.Preview
			if len(item.Preview.Bytes) > 0 {
				preview.Bytes = append([]byte(nil), item.Preview.Bytes...)
			}
			attachment.Preview = &preview
		}
		cloned[idx] = attachment
	}

	return cloned
}

func messageSnapshotFingerprint(snapshot SnapshotPayload) string {
	var builder strings.Builder
	builder.WriteString(snapshot.Text)
	if len(snapshot.Media) == 0 {
		return builder.String()
	}

	builder.WriteByte('\x1f')
	for idx, item := range snapshot.Media {
		if idx > 0 {
			builder.WriteByte('\x1e')
		}
		builder.WriteString(string(item.Type))
		builder.WriteByte(':')
		builder.WriteString(item.ID)
		builder.WriteByte(':')
		builder.WriteString(item.FileName)
		builder.WriteByte(':')
		builder.WriteString(item.MIMEType)
	}

	return builder.String()
}

func reactionPayloadStats(message *tg.Message) (present bool, resultsCount int, recentCount int) {
	if message == nil {
		return false, 0, 0
	}
	reactions, present := message.GetReactions()
	if !present {
		return false, 0, 0
	}
	resultsCount = len(reactions.Results)
	if recent, ok := reactions.GetRecentReactions(); ok {
		recentCount = len(recent)
	}

	return true, resultsCount, recentCount
}

func indexGotdUsers(users []tg.UserClass) map[int64]*tg.User {
	if len(users) == 0 {
		return nil
	}

	out := make(map[int64]*tg.User, len(users))
	for _, user := range users {
		if user == nil {
			continue
		}
		notEmpty, ok := user.AsNotEmpty()
		if !ok || notEmpty == nil {
			continue
		}
		out[notEmpty.ID] = notEmpty
	}

	return out
}

func indexGotdChats(chats []tg.ChatClass) map[int64]gotdChatInfo {
	if len(chats) == 0 {
		return nil
	}

	out := make(map[int64]gotdChatInfo, len(chats))
	for _, chat := range chats {
		if chat == nil {
			continue
		}

		switch typed := chat.(type) {
		case *tg.Chat:
			out[typed.ID] = gotdChatInfo{
				title:     typed.Title,
				kind:      otogi.ConversationTypeGroup,
				inputPeer: typed.AsInputPeer(),
			}
		case *tg.ChatForbidden:
			out[typed.ID] = gotdChatInfo{
				title: typed.Title,
				kind:  otogi.ConversationTypeGroup,
				inputPeer: &tg.InputPeerChat{
					ChatID: typed.ID,
				},
			}
		case *tg.Channel:
			kind := otogi.ConversationTypeChannel
			if typed.Megagroup {
				kind = otogi.ConversationTypeGroup
			}
			out[typed.ID] = gotdChatInfo{
				title:     typed.Title,
				kind:      kind,
				inputPeer: typed.AsInputPeer(),
			}
		case *tg.ChannelForbidden:
			kind := otogi.ConversationTypeChannel
			if typed.Megagroup {
				kind = otogi.ConversationTypeGroup
			}
			out[typed.ID] = gotdChatInfo{
				title: typed.Title,
				kind:  kind,
				inputPeer: &tg.InputPeerChannel{
					ChannelID:  typed.ID,
					AccessHash: typed.AccessHash,
				},
			}
		}
	}

	return out
}

func resolveChatFromPeer(peer tg.PeerClass, envelope gotdUpdateEnvelope) ChatRef {
	switch typed := peer.(type) {
	case *tg.PeerUser:
		actor := resolveActorByUserID(typed.UserID, envelope)
		return ChatRef{
			ID:    actor.ID,
			Type:  otogi.ConversationTypePrivate,
			Title: actor.DisplayName,
		}
	case *tg.PeerChat:
		return resolveChatByChatID(typed.ChatID, envelope)
	case *tg.PeerChannel:
		return resolveChatByChannelID(typed.ChannelID, envelope)
	default:
		return ChatRef{
			ID:   gotdUnknownConversationID,
			Type: otogi.ConversationTypePrivate,
		}
	}
}

func resolveChatByChatID(chatID int64, envelope gotdUpdateEnvelope) ChatRef {
	id := strconv.FormatInt(chatID, 10)
	info, ok := envelope.chatsByID[chatID]
	if !ok {
		return ChatRef{
			ID:   id,
			Type: otogi.ConversationTypeGroup,
		}
	}

	return ChatRef{
		ID:    id,
		Title: info.title,
		Type:  info.kind,
	}
}

func resolveChatByChannelID(channelID int64, envelope gotdUpdateEnvelope) ChatRef {
	id := strconv.FormatInt(channelID, 10)
	info, ok := envelope.chatsByID[channelID]
	if !ok {
		return ChatRef{
			ID:   id,
			Type: otogi.ConversationTypeChannel,
		}
	}

	return ChatRef{
		ID:    id,
		Title: info.title,
		Type:  info.kind,
	}
}

func resolveActorFromPeer(peer tg.PeerClass, envelope gotdUpdateEnvelope) ActorRef {
	switch typed := peer.(type) {
	case *tg.PeerUser:
		return resolveActorByUserID(typed.UserID, envelope)
	case *tg.PeerChat:
		return ActorRef{
			ID:          strconv.FormatInt(typed.ChatID, 10),
			DisplayName: lookupChatTitle(typed.ChatID, envelope),
			IsBot:       false,
		}
	case *tg.PeerChannel:
		return ActorRef{
			ID:          strconv.FormatInt(typed.ChannelID, 10),
			DisplayName: lookupChatTitle(typed.ChannelID, envelope),
			IsBot:       false,
		}
	default:
		return ActorRef{ID: gotdUnknownActorID}
	}
}

func resolveActorByUserID(userID int64, envelope gotdUpdateEnvelope) ActorRef {
	id := strconv.FormatInt(userID, 10)
	if userID == 0 {
		return ActorRef{ID: gotdUnknownActorID}
	}

	user, ok := envelope.usersByID[userID]
	if !ok || user == nil {
		return ActorRef{ID: id}
	}

	username, _ := user.GetUsername()
	firstName, _ := user.GetFirstName()
	lastName, _ := user.GetLastName()

	displayName := strings.TrimSpace(strings.TrimSpace(firstName + " " + lastName))
	if displayName == "" {
		displayName = username
	}
	if displayName == "" {
		displayName = id
	}

	return ActorRef{
		ID:          id,
		Username:    username,
		DisplayName: displayName,
		IsBot:       user.Bot,
	}
}

func resolveInputPeerFromPeer(peer tg.PeerClass, envelope gotdUpdateEnvelope) tg.InputPeerClass {
	switch typed := peer.(type) {
	case *tg.PeerUser:
		return resolveInputPeerByUserID(typed.UserID, envelope)
	case *tg.PeerChat:
		return resolveInputPeerByChatID(typed.ChatID)
	case *tg.PeerChannel:
		return resolveInputPeerByChannelID(typed.ChannelID, envelope)
	default:
		return nil
	}
}

func resolveInputPeerByUserID(userID int64, envelope gotdUpdateEnvelope) tg.InputPeerClass {
	if userID == 0 {
		return nil
	}

	user, ok := envelope.usersByID[userID]
	if !ok || user == nil {
		return nil
	}

	return user.AsInputPeer()
}

func resolveInputPeerByChatID(chatID int64) tg.InputPeerClass {
	if chatID == 0 {
		return nil
	}

	return &tg.InputPeerChat{ChatID: chatID}
}

func resolveInputPeerByChannelID(channelID int64, envelope gotdUpdateEnvelope) tg.InputPeerClass {
	if channelID == 0 {
		return nil
	}

	info, ok := envelope.chatsByID[channelID]
	if !ok || info.inputPeer == nil {
		return nil
	}

	return cloneInputPeer(info.inputPeer)
}

func lookupChatTitle(chatID int64, envelope gotdUpdateEnvelope) string {
	info, ok := envelope.chatsByID[chatID]
	if !ok {
		return ""
	}
	return info.title
}

func mapTextEntities(entities []tg.MessageEntityClass) []otogi.TextEntity {
	if len(entities) == 0 {
		return nil
	}

	out := make([]otogi.TextEntity, 0, len(entities))
	for _, entity := range entities {
		if entity == nil {
			continue
		}

		mapped := otogi.TextEntity{
			Type:   mapTextEntityTypeFromTelegram(entity),
			Offset: entity.GetOffset(),
			Length: entity.GetLength(),
		}

		switch typed := entity.(type) {
		case *tg.MessageEntityPre:
			mapped.Language = typed.Language
		case *tg.MessageEntityTextURL:
			mapped.URL = typed.URL
		case *tg.MessageEntityMentionName:
			mapped.MentionUserID = strconv.FormatInt(typed.UserID, 10)
		case *tg.MessageEntityCustomEmoji:
			mapped.CustomEmojiID = strconv.FormatInt(typed.DocumentID, 10)
		case *tg.MessageEntityBlockquote:
			mapped.Collapsed = typed.Collapsed
		default:
		}

		out = append(out, mapped)
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

func mapTextEntityTypeFromTelegram(entity tg.MessageEntityClass) otogi.TextEntityType {
	if entity == nil {
		return otogi.TextEntityTypeUnknown
	}

	switch entity.(type) {
	case *tg.MessageEntityUnknown:
		return otogi.TextEntityTypeUnknown
	case *tg.MessageEntityMention:
		return otogi.TextEntityTypeMention
	case *tg.MessageEntityHashtag:
		return otogi.TextEntityTypeHashtag
	case *tg.MessageEntityBotCommand:
		return otogi.TextEntityTypeBotCommand
	case *tg.MessageEntityURL:
		return otogi.TextEntityTypeURL
	case *tg.MessageEntityEmail:
		return otogi.TextEntityTypeEmail
	case *tg.MessageEntityBold:
		return otogi.TextEntityTypeBold
	case *tg.MessageEntityItalic:
		return otogi.TextEntityTypeItalic
	case *tg.MessageEntityCode:
		return otogi.TextEntityTypeCode
	case *tg.MessageEntityPre:
		return otogi.TextEntityTypePre
	case *tg.MessageEntityTextURL:
		return otogi.TextEntityTypeTextURL
	case *tg.MessageEntityMentionName:
		return otogi.TextEntityTypeMentionName
	case *tg.MessageEntityPhone:
		return otogi.TextEntityTypePhone
	case *tg.MessageEntityCashtag:
		return otogi.TextEntityTypeCashtag
	case *tg.MessageEntityBankCard:
		return otogi.TextEntityTypeBankCard
	case *tg.MessageEntityUnderline:
		return otogi.TextEntityTypeUnderline
	case *tg.MessageEntityStrike:
		return otogi.TextEntityTypeStrike
	case *tg.MessageEntityBlockquote:
		return otogi.TextEntityTypeBlockquote
	case *tg.MessageEntitySpoiler:
		return otogi.TextEntityTypeSpoiler
	case *tg.MessageEntityCustomEmoji:
		return otogi.TextEntityTypeCustomEmoji
	default:
		return otogi.TextEntityTypeUnknown
	}
}

func mapMessageMedia(media tg.MessageMediaClass) []MediaPayload {
	switch typed := media.(type) {
	case *tg.MessageMediaPhoto:
		photo, ok := typed.GetPhoto()
		if !ok || photo == nil {
			return nil
		}
		photoID := mapPhotoID(photo)
		if photoID == "" {
			return nil
		}

		return []MediaPayload{
			{
				ID:   photoID,
				Type: otogi.MediaTypePhoto,
			},
		}
	case *tg.MessageMediaDocument:
		document, ok := typed.GetDocument()
		if !ok || document == nil {
			return nil
		}
		return mapDocumentMedia(document)
	default:
		return nil
	}
}

func mapPhotoID(photo tg.PhotoClass) string {
	switch typed := photo.(type) {
	case *tg.Photo:
		return strconv.FormatInt(typed.ID, 10)
	case *tg.PhotoEmpty:
		return strconv.FormatInt(typed.ID, 10)
	default:
		return ""
	}
}

func mapDocumentMedia(document tg.DocumentClass) []MediaPayload {
	typed, ok := document.(*tg.Document)
	if !ok {
		return nil
	}

	mediaType := mediaTypeFromDocument(typed.MimeType, typed.Attributes)
	fileName := documentFileName(typed.Attributes)

	return []MediaPayload{
		{
			ID:        strconv.FormatInt(typed.ID, 10),
			Type:      mediaType,
			MIMEType:  typed.MimeType,
			FileName:  fileName,
			SizeBytes: typed.Size,
		},
	}
}

func mediaTypeFromDocument(mimeType string, attributes []tg.DocumentAttributeClass) otogi.MediaType {
	for _, attribute := range attributes {
		switch attribute.(type) {
		case *tg.DocumentAttributeAudio:
			return otogi.MediaTypeAudio
		case *tg.DocumentAttributeVideo:
			return otogi.MediaTypeVideo
		}
	}

	switch {
	case strings.HasPrefix(mimeType, "image/"):
		return otogi.MediaTypePhoto
	case strings.HasPrefix(mimeType, "video/"):
		return otogi.MediaTypeVideo
	case strings.HasPrefix(mimeType, "audio/"):
		return otogi.MediaTypeAudio
	default:
		return otogi.MediaTypeDocument
	}
}

func documentFileName(attributes []tg.DocumentAttributeClass) string {
	for _, attribute := range attributes {
		typed, ok := attribute.(*tg.DocumentAttributeFilename)
		if !ok {
			continue
		}
		return typed.FileName
	}

	return ""
}

func reactionToEmoji(reaction tg.ReactionClass) string {
	switch typed := reaction.(type) {
	case *tg.ReactionEmoji:
		return typed.Emoticon
	case *tg.ReactionCustomEmoji:
		return "custom:" + strconv.FormatInt(typed.DocumentID, 10)
	case *tg.ReactionPaid:
		return "paid"
	default:
		return ""
	}
}

func reactionDeltaFromEditedMessage(message *tg.Message) (gotdReactionDelta, bool) {
	if message == nil {
		return gotdReactionDelta{}, false
	}
	reactions, ok := message.GetReactions()
	if !ok {
		return gotdReactionDelta{}, false
	}

	recentReactions, ok := reactions.GetRecentReactions()
	if ok && len(recentReactions) > 0 {
		candidate, candidateOK := selectRecentReactionCandidate(recentReactions)
		if candidateOK {
			emoji := reactionToEmoji(candidate.Reaction)
			if emoji != "" {
				return gotdReactionDelta{
					action:    UpdateTypeReactionAdd,
					messageID: message.ID,
					emoji:     emoji,
					actor:     candidate.PeerID,
					peer:      message.PeerID,
				}, true
			}
		}
	}

	emoji, ok := reactionFromChosenResults(reactions.Results)
	if !ok {
		return gotdReactionDelta{}, false
	}
	return gotdReactionDelta{
		action:    UpdateTypeReactionAdd,
		messageID: message.ID,
		emoji:     emoji,
		peer:      message.PeerID,
	}, true
}

func selectRecentReactionCandidate(recentReactions []tg.MessagePeerReaction) (tg.MessagePeerReaction, bool) {
	for _, reaction := range recentReactions {
		if reaction.My {
			return reaction, true
		}
	}
	for _, reaction := range recentReactions {
		if reaction.Unread {
			return reaction, true
		}
	}
	if len(recentReactions) == 0 {
		return tg.MessagePeerReaction{}, false
	}

	latest := recentReactions[0]
	for _, reaction := range recentReactions[1:] {
		if reaction.Date > latest.Date {
			latest = reaction
		}
	}

	return latest, true
}

func reactionFromChosenResults(results []tg.ReactionCount) (string, bool) {
	found := false
	highestOrder := 0
	chosenEmoji := ""

	for _, result := range results {
		order, ok := result.GetChosenOrder()
		if !ok {
			continue
		}
		emoji := reactionToEmoji(result.Reaction)
		if emoji == "" {
			continue
		}
		if !found || order > highestOrder {
			found = true
			highestOrder = order
			chosenEmoji = emoji
		}
	}
	if !found {
		if len(results) != 1 {
			return "", false
		}
		emoji := reactionToEmoji(results[0].Reaction)
		if emoji == "" || results[0].Count <= 0 {
			return "", false
		}

		return emoji, true
	}

	return chosenEmoji, true
}

func shouldSuppressAmbiguousReactionEdit(message *tg.Message) bool {
	if message == nil || !message.EditHide {
		return false
	}
	_, present := message.GetReactions()

	return present
}

func shouldRememberUnmappedReactionEditCounts(message *tg.Message, isLikelyReactionUpdate bool) bool {
	if message == nil {
		return false
	}
	if !isLikelyReactionUpdate {
		return true
	}
	reactions, present := message.GetReactions()
	if !present {
		return true
	}
	if len(reactions.Results) > 0 {
		return true
	}
	recentReactions, ok := reactions.GetRecentReactions()
	if ok && len(recentReactions) > 0 {
		return true
	}

	return false
}

func isLikelyReactionOnlyEdit(message *tg.Message) bool {
	if message == nil {
		return false
	}
	if message.EditHide {
		return true
	}
	if _, ok := message.GetEditDate(); !ok {
		return true
	}

	return false
}

func resolveMutationOccurredAt(message *tg.Message, envelopeOccurredAt time.Time) time.Time {
	if message != nil {
		if editDate, ok := message.GetEditDate(); ok {
			if occurredAt := intToTimeUTC(editDate); !occurredAt.IsZero() {
				return occurredAt
			}
		}
	}
	if !envelopeOccurredAt.IsZero() {
		return envelopeOccurredAt
	}
	if message != nil {
		if occurredAt := intToTimeUTC(message.Date); !occurredAt.IsZero() {
			return occurredAt
		}
	}

	return time.Now().UTC()
}

func timePointer(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}

	utc := value.UTC()
	return &utc
}

func actorPointer(actor ActorRef) *ActorRef {
	if actor.ID == "" || actor.ID == gotdUnknownActorID {
		return nil
	}
	copyActor := actor
	return &copyActor
}

func intToTimeUTC(value int) time.Time {
	if value <= 0 {
		return time.Time{}
	}
	return time.Unix(int64(value), 0).UTC()
}

func composeUpdateID(updateType UpdateType, chatID string, parts ...any) string {
	values := []string{"tg", updateTypeIDToken(updateType)}
	if chatID != "" {
		values = append(values, chatID)
	}
	for _, part := range parts {
		switch typed := part.(type) {
		case string:
			if typed != "" {
				values = append(values, typed)
			}
		case time.Time:
			if !typed.IsZero() {
				values = append(values, strconv.FormatInt(typed.UnixNano(), 10))
			}
		default:
			values = append(values, fmt.Sprint(part))
		}
	}

	return strings.Join(values, ":")
}

func updateTypeIDToken(updateType UpdateType) string {
	switch updateType {
	case UpdateTypeMessage:
		return string(otogi.EventKindMessageCreated)
	case UpdateTypeEdit:
		return string(otogi.EventKindMessageEdited)
	case UpdateTypeDelete:
		return string(otogi.EventKindMessageRetracted)
	case UpdateTypeReactionAdd:
		return string(otogi.EventKindReactionAdded)
	case UpdateTypeReactionRemove:
		return string(otogi.EventKindReactionRemoved)
	case UpdateTypeMemberJoin:
		return string(otogi.EventKindMemberJoined)
	case UpdateTypeMemberLeave:
		return string(otogi.EventKindMemberLeft)
	case UpdateTypeRole:
		return string(otogi.EventKindRoleUpdated)
	case UpdateTypeMigration:
		return string(otogi.EventKindChatMigrated)
	default:
		return string(updateType)
	}
}

func newGotdMetadata(envelope gotdUpdateEnvelope) map[string]string {
	if envelope.updateClass == "" {
		return nil
	}
	return map[string]string{
		"gotd_update": envelope.updateClass,
	}
}

func chatParticipantRole(participant tg.ChatParticipantClass) string {
	switch participant.(type) {
	case *tg.ChatParticipantCreator:
		return "owner"
	case *tg.ChatParticipantAdmin:
		return "admin"
	case *tg.ChatParticipant:
		return "member"
	default:
		return ""
	}
}

func channelParticipantRole(participant tg.ChannelParticipantClass) string {
	switch participant.(type) {
	case *tg.ChannelParticipantCreator:
		return "owner"
	case *tg.ChannelParticipantAdmin:
		return "admin"
	case *tg.ChannelParticipant:
		return "member"
	case *tg.ChannelParticipantSelf:
		return "member"
	case *tg.ChannelParticipantBanned:
		return "banned"
	case *tg.ChannelParticipantLeft:
		return "left"
	default:
		return ""
	}
}

func isChannelRoleActive(role string) bool {
	switch role {
	case "", "left", "banned":
		return false
	default:
		return true
	}
}
