package telegram

import (
	"context"
	"fmt"
	"time"

	"ex-otogi/pkg/otogi/platform"
)

// Decoder converts Telegram update DTOs into Otogi events.
type Decoder interface {
	// Decode maps one adapter update into a validated Otogi event envelope.
	Decode(ctx context.Context, update Update) (*platform.Event, error)
}

// DefaultDecoder provides default Telegram-to-otogi mappings.
type DefaultDecoder struct{}

// NewDefaultDecoder creates a default decoder.
func NewDefaultDecoder() DefaultDecoder {
	return DefaultDecoder{}
}

// Decode converts a Telegram update into an Otogi event.
func (d DefaultDecoder) Decode(_ context.Context, update Update) (*platform.Event, error) {
	event := newBaseEvent(update)
	eventKind, known := eventKindFromUpdateType(update.Type)
	if !known {
		return nil, fmt.Errorf("decode update %s: unsupported type", update.Type)
	}
	event.Kind = eventKind

	switch update.Type {
	case UpdateTypeMessage:
		article, err := decodeArticle(update.Article)
		if err != nil {
			return nil, fmt.Errorf("decode article: %w", err)
		}
		event.Article = article
	case UpdateTypeEdit:
		mutation, err := decodeEdit(update.Edit, update.OccurredAt)
		if err != nil {
			return nil, fmt.Errorf("decode edit: %w", err)
		}
		event.Mutation = mutation
	case UpdateTypeDelete:
		mutation, err := decodeDelete(update.Delete, update.OccurredAt)
		if err != nil {
			return nil, fmt.Errorf("decode delete: %w", err)
		}
		event.Mutation = mutation
	case UpdateTypeReactionAdd, UpdateTypeReactionRemove:
		reaction, err := decodeReaction(update.Type, update.Reaction)
		if err != nil {
			return nil, fmt.Errorf("decode reaction: %w", err)
		}
		event.Reaction = reaction
	case UpdateTypeMemberJoin, UpdateTypeMemberLeave:
		stateChange, err := decodeMember(update.Type, update.Member)
		if err != nil {
			return nil, fmt.Errorf("decode member update: %w", err)
		}
		event.StateChange = stateChange
	case UpdateTypeRole:
		stateChange, err := decodeRole(update.Role)
		if err != nil {
			return nil, fmt.Errorf("decode role update: %w", err)
		}
		event.StateChange = stateChange
	case UpdateTypeMigration:
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
func newBaseEvent(update Update) *platform.Event {
	occurredAt := update.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}

	return &platform.Event{
		ID:         update.ID,
		OccurredAt: occurredAt,
		Source: platform.EventSource{
			Platform: platform.PlatformTelegram,
		},
		Conversation: platform.Conversation{
			ID:    update.Chat.ID,
			Type:  update.Chat.Type,
			Title: update.Chat.Title,
		},
		Actor: platform.Actor{
			ID:          update.Actor.ID,
			Username:    update.Actor.Username,
			DisplayName: update.Actor.DisplayName,
			IsBot:       update.Actor.IsBot,
		},
		Metadata: update.Metadata,
	}
}

// decodeArticle maps Telegram article payload into standardized article content.
func decodeArticle(payload *ArticlePayload) (*platform.Article, error) {
	if payload == nil {
		return nil, fmt.Errorf("missing article payload")
	}

	return &platform.Article{
		ID:               payload.ID,
		ThreadID:         payload.ThreadID,
		ReplyToArticleID: payload.ReplyToArticleID,
		Text:             payload.Text,
		Entities:         payload.Entities,
		Media:            mapMedia(payload.Media),
		Reactions:        append([]platform.ArticleReaction(nil), payload.Reactions...),
	}, nil
}

// decodeEdit maps Telegram edit payload into mutation semantics.
func decodeEdit(payload *ArticleEditPayload, occurredAt time.Time) (*platform.ArticleMutation, error) {
	if payload == nil {
		return nil, fmt.Errorf("missing edit payload")
	}
	changedAt := cloneTimePointer(payload.ChangedAt)
	if changedAt == nil {
		changedAt = eventTimePointer(occurredAt)
	}

	return &platform.ArticleMutation{
		Type:            platform.MutationTypeEdit,
		TargetArticleID: payload.ArticleID,
		ChangedAt:       changedAt,
		Before:          mapSnapshot(payload.Before),
		After:           mapSnapshot(payload.After),
		Reason:          payload.Reason,
	}, nil
}

// decodeDelete maps Telegram delete payload into retraction mutation semantics.
func decodeDelete(payload *ArticleDeletePayload, occurredAt time.Time) (*platform.ArticleMutation, error) {
	if payload == nil {
		return nil, fmt.Errorf("missing delete payload")
	}
	changedAt := eventTimePointer(occurredAt)

	return &platform.ArticleMutation{
		Type:            platform.MutationTypeRetraction,
		TargetArticleID: payload.ArticleID,
		ChangedAt:       changedAt,
		Reason:          payload.Reason,
	}, nil
}

// decodeReaction maps reaction add/remove payload into standardized reaction metadata.
func decodeReaction(updateType UpdateType, payload *ArticleReactionPayload) (*platform.Reaction, error) {
	if payload == nil {
		return nil, fmt.Errorf("missing reaction payload")
	}

	action := platform.ReactionActionAdd
	if updateType == UpdateTypeReactionRemove {
		action = platform.ReactionActionRemove
	}

	return &platform.Reaction{
		ArticleID: payload.ArticleID,
		Emoji:     payload.Emoji,
		Action:    action,
	}, nil
}

// decodeMember maps join/leave transitions into standardized member state changes.
func decodeMember(updateType UpdateType, payload *MemberPayload) (*platform.StateChange, error) {
	if payload == nil {
		return nil, fmt.Errorf("missing member payload")
	}

	action := platform.EventKindMemberJoined
	if updateType == UpdateTypeMemberLeave {
		action = platform.EventKindMemberLeft
	}

	var inviter *platform.Actor
	if payload.Inviter != nil {
		inviter = &platform.Actor{
			ID:          payload.Inviter.ID,
			Username:    payload.Inviter.Username,
			DisplayName: payload.Inviter.DisplayName,
			IsBot:       payload.Inviter.IsBot,
		}
	}

	return &platform.StateChange{
		Type: platform.StateChangeTypeMember,
		Member: &platform.MemberChange{
			Action:   action,
			Member:   mapActor(payload.Member),
			Inviter:  inviter,
			Reason:   payload.Reason,
			JoinedAt: payload.JoinedAt,
		},
	}, nil
}

// decodeRole maps role transitions into standardized role state changes.
func decodeRole(payload *RolePayload) (*platform.StateChange, error) {
	if payload == nil {
		return nil, fmt.Errorf("missing role payload")
	}

	return &platform.StateChange{
		Type: platform.StateChangeTypeRole,
		Role: &platform.RoleChange{
			MemberID: payload.MemberID,
			OldRole:  payload.OldRole,
			NewRole:  payload.NewRole,
			ChangedBy: platform.Actor{
				ID:          payload.ChangedBy.ID,
				Username:    payload.ChangedBy.Username,
				DisplayName: payload.ChangedBy.DisplayName,
				IsBot:       payload.ChangedBy.IsBot,
			},
		},
	}, nil
}

// decodeMigration maps Telegram chat migrations into standardized migration state changes.
func decodeMigration(payload *MigrationPayload) (*platform.StateChange, error) {
	if payload == nil {
		return nil, fmt.Errorf("missing migration payload")
	}

	return &platform.StateChange{
		Type: platform.StateChangeTypeMigration,
		Migration: &platform.ChatMigration{
			FromConversationID: payload.FromChatID,
			ToConversationID:   payload.ToChatID,
			Reason:             payload.Reason,
		},
	}, nil
}

// mapMedia converts media descriptors into standardized attachment metadata.
func mapMedia(media []MediaPayload) []platform.MediaAttachment {
	if len(media) == 0 {
		return nil
	}

	mapped := make([]platform.MediaAttachment, 0, len(media))
	for _, item := range media {
		mapped = append(mapped, platform.MediaAttachment{
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
func mapPreview(preview *MediaPreviewPayload) *platform.MediaPreview {
	if preview == nil {
		return nil
	}

	return &platform.MediaPreview{
		MIMEType: preview.MIMEType,
		Bytes:    preview.Bytes,
		Width:    preview.Width,
		Height:   preview.Height,
		Duration: preview.Duration,
	}
}

// mapSnapshot converts immutable article snapshots for mutation payloads.
func mapSnapshot(snapshot *ArticleSnapshotPayload) *platform.ArticleSnapshot {
	if snapshot == nil {
		return nil
	}

	return &platform.ArticleSnapshot{
		Text:     snapshot.Text,
		Entities: append([]platform.TextEntity(nil), snapshot.Entities...),
		Media:    mapMedia(snapshot.Media),
	}
}

// mapActor converts adapter actor references to standardized actor values.
func mapActor(actor ActorRef) platform.Actor {
	return platform.Actor{
		ID:          actor.ID,
		Username:    actor.Username,
		DisplayName: actor.DisplayName,
		IsBot:       actor.IsBot,
	}
}

func eventTimePointer(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}

	utc := value.UTC()
	return &utc
}

func cloneTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}

	utc := value.UTC()
	return &utc
}
