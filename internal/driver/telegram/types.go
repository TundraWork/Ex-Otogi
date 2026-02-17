package telegram

import (
	"time"

	"ex-otogi/pkg/otogi"
)

// UpdateType identifies the Telegram update semantic category.
type UpdateType string

const (
	// UpdateTypeMessage identifies new message updates.
	UpdateTypeMessage UpdateType = "message"
	// UpdateTypeEdit identifies edited message updates.
	UpdateTypeEdit UpdateType = "edit"
	// UpdateTypeDelete identifies deleted/retracted message updates.
	UpdateTypeDelete UpdateType = "delete"
	// UpdateTypeReactionAdd identifies reaction add updates.
	UpdateTypeReactionAdd UpdateType = "reaction_add"
	// UpdateTypeReactionRemove identifies reaction remove updates.
	UpdateTypeReactionRemove UpdateType = "reaction_remove"
	// UpdateTypeMemberJoin identifies member join updates.
	UpdateTypeMemberJoin UpdateType = "member_join"
	// UpdateTypeMemberLeave identifies member leave updates.
	UpdateTypeMemberLeave UpdateType = "member_leave"
	// UpdateTypeRole identifies role update updates.
	UpdateTypeRole UpdateType = "role"
	// UpdateTypeMigration identifies chat migration updates.
	UpdateTypeMigration UpdateType = "migration"
)

// Update is the Telegram adapter's internal DTO before neutral decoding.
type Update struct {
	// ID is a stable identifier for the mapped Telegram update.
	ID string
	// Type identifies the mapped update semantic category.
	Type UpdateType
	// OccurredAt is the Telegram-side timestamp for the update.
	OccurredAt time.Time
	// Chat identifies where the update occurred.
	Chat ChatRef
	// Actor identifies who initiated the update when known.
	Actor ActorRef
	// Article carries payload for article updates.
	Article *ArticlePayload
	// Edit carries payload for article edit updates.
	Edit *ArticleEditPayload
	// Delete carries payload for article deletion updates.
	Delete *ArticleDeletePayload
	// Reaction carries payload for article reaction updates.
	Reaction *ArticleReactionPayload
	// Member carries payload for member join/leave updates.
	Member *MemberPayload
	// Role carries payload for role mutation updates.
	Role *RolePayload
	// Migration carries payload for chat migration updates.
	Migration *MigrationPayload
	// Metadata stores additional mapper-specific attributes.
	Metadata map[string]string
}

// ChatRef identifies Telegram chat context.
type ChatRef struct {
	// ID is the Telegram chat identifier.
	ID string
	// Title is the Telegram chat title when available.
	Title string
	// Type is the normalized conversation type.
	Type otogi.ConversationType
}

// ActorRef identifies Telegram actor context.
type ActorRef struct {
	// ID is the Telegram user/account identifier.
	ID string
	// Username is the Telegram handle when available.
	Username string
	// DisplayName is the human-readable Telegram display name.
	DisplayName string
	// IsBot reports whether the actor is a bot account.
	IsBot bool
}

// ArticlePayload represents a Telegram article projection.
type ArticlePayload struct {
	// ID is the Telegram article identifier.
	ID string
	// ThreadID is the optional topic/thread identifier.
	ThreadID string
	// ReplyToArticleID is the replied-to article identifier when present.
	ReplyToArticleID string
	// Text is the normalized article text.
	Text string
	// Entities carries rich-text entity ranges.
	Entities []otogi.TextEntity
	// Media carries normalized media attachments.
	Media []MediaPayload
	// Reactions carries projected reaction counts when included by Telegram updates.
	Reactions []otogi.ArticleReaction
}

// MediaPayload represents Telegram media metadata.
type MediaPayload struct {
	// ID is the attachment identifier.
	ID string
	// Type is the normalized media category.
	Type otogi.MediaType
	// MIMEType is the attachment MIME type.
	MIMEType string
	// FileName is the original attachment filename.
	FileName string
	// SizeBytes is the attachment size in bytes when known.
	SizeBytes int64
	// Caption is the optional media caption text.
	Caption string
	// URI is an optional retrievable location for the media.
	URI string
	// Preview carries optional lightweight preview data.
	Preview *MediaPreviewPayload
}

// MediaPreviewPayload contains optional preview bytes and dimensions.
type MediaPreviewPayload struct {
	// MIMEType is the preview content type.
	MIMEType string
	// Bytes holds preview bytes when available.
	Bytes []byte
	// Width is the preview width in pixels.
	Width int
	// Height is the preview height in pixels.
	Height int
	// Duration is preview duration for time-based media.
	Duration time.Duration
}

// ArticleEditPayload captures before/after article content for edits.
type ArticleEditPayload struct {
	// ArticleID identifies the edited Telegram article.
	ArticleID string
	// ChangedAt captures when the edit happened on Telegram when known.
	ChangedAt *time.Time
	// Before captures the article snapshot before the edit.
	Before *ArticleSnapshotPayload
	// After captures the article snapshot after the edit.
	After *ArticleSnapshotPayload
	// Reason carries optional platform context for the edit.
	Reason string
}

// ArticleSnapshotPayload captures immutable article snapshots.
type ArticleSnapshotPayload struct {
	// Text is the immutable article text snapshot.
	Text string
	// Entities stores immutable rich-text entities aligned with Text.
	Entities []otogi.TextEntity
	// Media is the immutable media snapshot.
	Media []MediaPayload
}

// ArticleDeletePayload captures article deletion metadata.
type ArticleDeletePayload struct {
	// ArticleID identifies the deleted/retracted article.
	ArticleID string
	// Reason carries optional platform context for deletion.
	Reason string
}

// ArticleReactionPayload captures emoji reaction metadata.
type ArticleReactionPayload struct {
	// ArticleID identifies the article receiving the reaction update.
	ArticleID string
	// Emoji is the normalized emoji token.
	Emoji string
}

// MemberPayload captures join/leave transitions.
type MemberPayload struct {
	// Member identifies the member affected by the update.
	Member ActorRef
	// Inviter identifies who invited the member when available.
	Inviter *ActorRef
	// Reason carries optional platform context for membership transition.
	Reason string
	// JoinedAt is the member join timestamp when provided.
	JoinedAt time.Time
}

// RolePayload captures role mutation metadata.
type RolePayload struct {
	// MemberID identifies the member whose role changed.
	MemberID string
	// OldRole is the previous role value.
	OldRole string
	// NewRole is the resulting role value.
	NewRole string
	// ChangedBy identifies who performed the role change.
	ChangedBy ActorRef
}

// MigrationPayload captures Telegram chat migration metadata.
type MigrationPayload struct {
	// FromChatID is the prior Telegram chat identifier.
	FromChatID string
	// ToChatID is the replacement Telegram chat identifier.
	ToChatID string
	// Reason carries optional platform context for migration.
	Reason string
}

func eventKindFromUpdateType(updateType UpdateType) (otogi.EventKind, bool) {
	switch updateType {
	case UpdateTypeMessage:
		return otogi.EventKindArticleCreated, true
	case UpdateTypeEdit:
		return otogi.EventKindArticleEdited, true
	case UpdateTypeDelete:
		return otogi.EventKindArticleRetracted, true
	case UpdateTypeReactionAdd:
		return otogi.EventKindArticleReactionAdded, true
	case UpdateTypeReactionRemove:
		return otogi.EventKindArticleReactionRemoved, true
	case UpdateTypeMemberJoin:
		return otogi.EventKindMemberJoined, true
	case UpdateTypeMemberLeave:
		return otogi.EventKindMemberLeft, true
	case UpdateTypeRole:
		return otogi.EventKindRoleUpdated, true
	case UpdateTypeMigration:
		return otogi.EventKindChatMigrated, true
	default:
		return "", false
	}
}
