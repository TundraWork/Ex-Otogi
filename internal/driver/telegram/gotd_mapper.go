package telegram

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"ex-otogi/pkg/otogi"

	"github.com/gotd/td/tg"
)

const (
	gotdUnknownConversationID = "unknown"
	gotdUnknownActorID        = "unknown"
)

// DefaultGotdUpdateMapper maps gotd updates into adapter DTO updates.
type DefaultGotdUpdateMapper struct {
	peerCache *PeerCache
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

// NewDefaultGotdUpdateMapper creates the default gotd mapper.
func NewDefaultGotdUpdateMapper(options ...GotdUpdateMapperOption) DefaultGotdUpdateMapper {
	mapper := DefaultGotdUpdateMapper{}
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
		return m.mapReactionDelta(envelope)
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
		return m.mapEditMessage(update.Message, envelope)
	case *tg.UpdateEditChannelMessage:
		return m.mapEditMessage(update.Message, envelope)
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

	payload := &MessagePayload{
		ID:       strconv.Itoa(message.ID),
		Text:     message.Message,
		Entities: mapTextEntities(message.Entities),
		Media:    mapMessageMedia(message.Media),
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

	occurredAt := intToTimeUTC(message.Date)
	if occurredAt.IsZero() {
		occurredAt = envelope.occurredAt
	}
	m.rememberConversationPeer(chat, resolveInputPeerFromPeer(message.PeerID, envelope))

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
	message tg.MessageClass,
	envelope gotdUpdateEnvelope,
) (Update, bool, error) {
	typed, ok := message.(*tg.Message)
	if !ok {
		return Update{}, false, nil
	}

	chat := resolveChatFromPeer(typed.PeerID, envelope)
	actor := resolveActorFromPeer(typed.FromID, envelope)
	if actor.ID == gotdUnknownActorID {
		actor = resolveActorFromPeer(typed.PeerID, envelope)
	}
	occurredAt := intToTimeUTC(typed.Date)
	if occurredAt.IsZero() {
		occurredAt = envelope.occurredAt
	}
	m.rememberConversationPeer(chat, resolveInputPeerFromPeer(typed.PeerID, envelope))

	return Update{
		ID:         composeUpdateID(UpdateTypeEdit, chat.ID, strconv.Itoa(typed.ID), occurredAt),
		Type:       UpdateTypeEdit,
		OccurredAt: occurredAt,
		Chat:       chat,
		Actor:      actor,
		Edit: &EditPayload{
			MessageID: strconv.Itoa(typed.ID),
			After: &SnapshotPayload{
				Text:  typed.Message,
				Media: mapMessageMedia(typed.Media),
			},
			Reason: "telegram_edit_update",
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

func (m DefaultGotdUpdateMapper) mapReactionDelta(envelope gotdUpdateEnvelope) (Update, bool, error) {
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

		typeName := entity.TypeName()
		typeName = strings.TrimPrefix(typeName, "messageEntity")
		typeName = strings.ToLower(typeName)

		out = append(out, otogi.TextEntity{
			Type:   typeName,
			Offset: entity.GetOffset(),
			Length: entity.GetLength(),
		})
	}

	if len(out) == 0 {
		return nil
	}

	return out
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
	values := []string{"tg", string(updateType)}
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
