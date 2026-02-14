package telegram

import (
	"context"
	"fmt"
	"time"

	"ex-otogi/pkg/otogi"
)

// Decoder converts Telegram update DTOs into neutral otogi events.
type Decoder interface {
	// Decode maps one adapter update into a validated neutral event envelope.
	Decode(ctx context.Context, update Update) (*otogi.Event, error)
}

// DefaultDecoder provides default Telegram-to-otogi mappings.
type DefaultDecoder struct{}

// NewDefaultDecoder creates a default decoder.
func NewDefaultDecoder() DefaultDecoder {
	return DefaultDecoder{}
}

// Decode converts a Telegram update into a neutral event.
func (d DefaultDecoder) Decode(_ context.Context, update Update) (*otogi.Event, error) {
	event := newBaseEvent(update)

	switch update.Type {
	case UpdateTypeMessage:
		event.Kind = otogi.EventKindMessageCreated
		message, err := decodeMessage(update.Message)
		if err != nil {
			return nil, fmt.Errorf("decode message: %w", err)
		}
		event.Message = message
	case UpdateTypeEdit:
		event.Kind = otogi.EventKindMessageEdited
		mutation, err := decodeEdit(update.Edit)
		if err != nil {
			return nil, fmt.Errorf("decode edit: %w", err)
		}
		event.Mutation = mutation
	case UpdateTypeDelete:
		event.Kind = otogi.EventKindMessageRetracted
		mutation, err := decodeDelete(update.Delete)
		if err != nil {
			return nil, fmt.Errorf("decode delete: %w", err)
		}
		event.Mutation = mutation
	case UpdateTypeReactionAdd, UpdateTypeReactionRemove:
		event.Kind = mapReactionKind(update.Type)
		reaction, err := decodeReaction(update.Type, update.Reaction)
		if err != nil {
			return nil, fmt.Errorf("decode reaction: %w", err)
		}
		event.Reaction = reaction
	case UpdateTypeMemberJoin, UpdateTypeMemberLeave:
		event.Kind = mapMembershipKind(update.Type)
		stateChange, err := decodeMember(update.Type, update.Member)
		if err != nil {
			return nil, fmt.Errorf("decode member update: %w", err)
		}
		event.StateChange = stateChange
	case UpdateTypeRole:
		event.Kind = otogi.EventKindRoleUpdated
		stateChange, err := decodeRole(update.Role)
		if err != nil {
			return nil, fmt.Errorf("decode role update: %w", err)
		}
		event.StateChange = stateChange
	case UpdateTypeMigration:
		event.Kind = otogi.EventKindChatMigrated
		stateChange, err := decodeMigration(update.Migration)
		if err != nil {
			return nil, fmt.Errorf("decode migration: %w", err)
		}
		event.StateChange = stateChange
	default:
		return nil, fmt.Errorf("decode update %s: unsupported type", update.Type)
	}

	if err := event.Validate(); err != nil {
		return nil, fmt.Errorf("decode update %s: %w", update.Type, err)
	}

	return event, nil
}

// newBaseEvent builds the shared envelope fields used by all update mappings.
func newBaseEvent(update Update) *otogi.Event {
	occurredAt := update.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}

	return &otogi.Event{
		ID:         update.ID,
		OccurredAt: occurredAt,
		Platform:   otogi.PlatformTelegram,
		Conversation: otogi.Conversation{
			ID:    update.Chat.ID,
			Type:  update.Chat.Type,
			Title: update.Chat.Title,
		},
		Actor: otogi.Actor{
			ID:          update.Actor.ID,
			Username:    update.Actor.Username,
			DisplayName: update.Actor.DisplayName,
			IsBot:       update.Actor.IsBot,
		},
		Metadata: update.Metadata,
	}
}

// decodeMessage maps Telegram message payload into neutral message content.
func decodeMessage(payload *MessagePayload) (*otogi.Message, error) {
	if payload == nil {
		return nil, fmt.Errorf("missing message payload")
	}

	return &otogi.Message{
		ID:        payload.ID,
		ThreadID:  payload.ThreadID,
		ReplyToID: payload.ReplyToID,
		Text:      payload.Text,
		Entities:  payload.Entities,
		Media:     mapMedia(payload.Media),
	}, nil
}

// decodeEdit maps Telegram edit payload into mutation semantics.
func decodeEdit(payload *EditPayload) (*otogi.Mutation, error) {
	if payload == nil {
		return nil, fmt.Errorf("missing edit payload")
	}

	return &otogi.Mutation{
		Type:            otogi.MutationTypeEdit,
		TargetMessageID: payload.MessageID,
		Before:          mapSnapshot(payload.Before),
		After:           mapSnapshot(payload.After),
		Reason:          payload.Reason,
	}, nil
}

// decodeDelete maps Telegram delete payload into retraction mutation semantics.
func decodeDelete(payload *DeletePayload) (*otogi.Mutation, error) {
	if payload == nil {
		return nil, fmt.Errorf("missing delete payload")
	}

	return &otogi.Mutation{
		Type:            otogi.MutationTypeRetraction,
		TargetMessageID: payload.MessageID,
		Reason:          payload.Reason,
	}, nil
}

// decodeReaction maps reaction add/remove payload into neutral reaction metadata.
func decodeReaction(updateType UpdateType, payload *ReactionPayload) (*otogi.Reaction, error) {
	if payload == nil {
		return nil, fmt.Errorf("missing reaction payload")
	}

	action := otogi.ReactionActionAdd
	if updateType == UpdateTypeReactionRemove {
		action = otogi.ReactionActionRemove
	}

	return &otogi.Reaction{
		MessageID: payload.MessageID,
		Emoji:     payload.Emoji,
		Action:    action,
	}, nil
}

// decodeMember maps join/leave transitions into neutral member state changes.
func decodeMember(updateType UpdateType, payload *MemberPayload) (*otogi.StateChange, error) {
	if payload == nil {
		return nil, fmt.Errorf("missing member payload")
	}

	action := otogi.EventKindMemberJoined
	if updateType == UpdateTypeMemberLeave {
		action = otogi.EventKindMemberLeft
	}

	var inviter *otogi.Actor
	if payload.Inviter != nil {
		inviter = &otogi.Actor{
			ID:          payload.Inviter.ID,
			Username:    payload.Inviter.Username,
			DisplayName: payload.Inviter.DisplayName,
			IsBot:       payload.Inviter.IsBot,
		}
	}

	return &otogi.StateChange{
		Type: otogi.StateChangeTypeMember,
		Member: &otogi.MemberChange{
			Action:   action,
			Member:   mapActor(payload.Member),
			Inviter:  inviter,
			Reason:   payload.Reason,
			JoinedAt: payload.JoinedAt,
		},
	}, nil
}

// decodeRole maps role transitions into neutral role state changes.
func decodeRole(payload *RolePayload) (*otogi.StateChange, error) {
	if payload == nil {
		return nil, fmt.Errorf("missing role payload")
	}

	return &otogi.StateChange{
		Type: otogi.StateChangeTypeRole,
		Role: &otogi.RoleChange{
			MemberID: payload.MemberID,
			OldRole:  payload.OldRole,
			NewRole:  payload.NewRole,
			ChangedBy: otogi.Actor{
				ID:          payload.ChangedBy.ID,
				Username:    payload.ChangedBy.Username,
				DisplayName: payload.ChangedBy.DisplayName,
				IsBot:       payload.ChangedBy.IsBot,
			},
		},
	}, nil
}

// decodeMigration maps Telegram chat migrations into neutral migration state changes.
func decodeMigration(payload *MigrationPayload) (*otogi.StateChange, error) {
	if payload == nil {
		return nil, fmt.Errorf("missing migration payload")
	}

	return &otogi.StateChange{
		Type: otogi.StateChangeTypeMigration,
		Migration: &otogi.ChatMigration{
			FromConversationID: payload.FromChatID,
			ToConversationID:   payload.ToChatID,
			Reason:             payload.Reason,
		},
	}, nil
}

// mapReactionKind derives neutral kind from Telegram reaction update type.
func mapReactionKind(updateType UpdateType) otogi.EventKind {
	if updateType == UpdateTypeReactionRemove {
		return otogi.EventKindReactionRemoved
	}

	return otogi.EventKindReactionAdded
}

// mapMembershipKind derives neutral kind from Telegram membership update type.
func mapMembershipKind(updateType UpdateType) otogi.EventKind {
	if updateType == UpdateTypeMemberLeave {
		return otogi.EventKindMemberLeft
	}

	return otogi.EventKindMemberJoined
}

// mapMedia converts media descriptors into neutral attachment metadata.
func mapMedia(media []MediaPayload) []otogi.MediaAttachment {
	if len(media) == 0 {
		return nil
	}

	mapped := make([]otogi.MediaAttachment, 0, len(media))
	for _, item := range media {
		mapped = append(mapped, otogi.MediaAttachment{
			ID:        item.ID,
			Type:      item.Type,
			MIMEType:  item.MIMEType,
			FileName:  item.FileName,
			SizeBytes: item.SizeBytes,
			Caption:   item.Caption,
			URI:       item.URI,
			Preview:   mapPreview(item.Preview),
		})
	}

	return mapped
}

// mapPreview converts optional preview metadata.
func mapPreview(preview *MediaPreviewPayload) *otogi.MediaPreview {
	if preview == nil {
		return nil
	}

	return &otogi.MediaPreview{
		MIMEType: preview.MIMEType,
		Bytes:    preview.Bytes,
		Width:    preview.Width,
		Height:   preview.Height,
		Duration: preview.Duration,
	}
}

// mapSnapshot converts immutable message snapshots for mutation payloads.
func mapSnapshot(snapshot *SnapshotPayload) *otogi.MessageSnapshot {
	if snapshot == nil {
		return nil
	}

	return &otogi.MessageSnapshot{
		Text:  snapshot.Text,
		Media: mapMedia(snapshot.Media),
	}
}

// mapActor converts adapter actor references to neutral actor values.
func mapActor(actor ActorRef) otogi.Actor {
	return otogi.Actor{
		ID:          actor.ID,
		Username:    actor.Username,
		DisplayName: actor.DisplayName,
		IsBot:       actor.IsBot,
	}
}
