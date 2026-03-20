package discord

import (
	"time"

	"ex-otogi/pkg/otogi/platform"
)

// UpdateType identifies the Discord update semantic category.
type UpdateType string

const (
	// UpdateTypeMessage identifies new message updates.
	UpdateTypeMessage UpdateType = "message"
	// UpdateTypeEdit identifies edited message updates.
	UpdateTypeEdit UpdateType = "edit"
	// UpdateTypeDelete identifies deleted message updates.
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
)

// Update is the Discord adapter's internal DTO before Otogi decoding.
type Update struct {
	// ID is a stable identifier for the mapped Discord update.
	ID string
	// Type identifies the mapped update semantic category.
	Type UpdateType
	// OccurredAt is the Discord-side timestamp for the update.
	OccurredAt time.Time
	// Channel identifies where the update occurred.
	Channel ChannelRef
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
	// Metadata stores additional mapper-specific attributes.
	Metadata map[string]string
}

// ChannelRef identifies Discord channel context.
type ChannelRef struct {
	// ID is the Discord channel identifier.
	ID string
	// GuildID is the Discord guild identifier when the channel belongs to a guild.
	GuildID string
	// Title is the Discord channel name when available.
	Title string
	// Type is the normalized conversation type.
	Type platform.ConversationType
}

// ActorRef identifies Discord actor context.
type ActorRef struct {
	// ID is the Discord user identifier.
	ID string
	// Username is the Discord username.
	Username string
	// DisplayName is the human-readable display name.
	DisplayName string
	// IsBot reports whether the actor is a bot account.
	IsBot bool
}

// ArticlePayload represents a Discord article projection.
type ArticlePayload struct {
	// ID is the Discord message identifier.
	ID string
	// ThreadID is the optional thread channel identifier.
	ThreadID string
	// ReplyToArticleID is the replied-to message identifier when present.
	ReplyToArticleID string
	// Text is the normalized article text.
	Text string
	// Entities carries rich-text entity ranges.
	Entities []platform.TextEntity
	// Media carries normalized media attachments.
	Media []MediaPayload
}

// MediaPayload represents Discord media metadata.
type MediaPayload struct {
	// ID is the attachment identifier.
	ID string
	// Type is the normalized media category.
	Type platform.MediaType
	// MIMEType is the attachment MIME type.
	MIMEType string
	// FileName is the original attachment filename.
	FileName string
	// SizeBytes is the attachment size in bytes when known.
	SizeBytes int64
	// Caption is the optional media caption text.
	Caption string
	// URI is the CDN URL for the attachment.
	URI string
}

// ArticleEditPayload captures before/after article content for edits.
type ArticleEditPayload struct {
	// ArticleID identifies the edited Discord message.
	ArticleID string
	// ChangedAt captures when the edit happened when known.
	ChangedAt *time.Time
	// Before captures the article snapshot before the edit.
	Before *ArticleSnapshotPayload
	// After captures the article snapshot after the edit.
	After *ArticleSnapshotPayload
	// Reason carries optional context for the edit.
	Reason string
}

// ArticleSnapshotPayload captures immutable article snapshots.
type ArticleSnapshotPayload struct {
	// Text is the immutable article text snapshot.
	Text string
	// Entities stores immutable rich-text entities aligned with Text.
	Entities []platform.TextEntity
	// Media is the immutable media snapshot.
	Media []MediaPayload
}

// ArticleDeletePayload captures article deletion metadata.
type ArticleDeletePayload struct {
	// ArticleID identifies the deleted message.
	ArticleID string
	// Reason carries optional context for deletion.
	Reason string
}

// ArticleReactionPayload captures emoji reaction metadata.
type ArticleReactionPayload struct {
	// ArticleID identifies the message receiving the reaction update.
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
	// Reason carries optional context for membership transition.
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

func eventKindFromUpdateType(updateType UpdateType) (platform.EventKind, bool) {
	switch updateType {
	case UpdateTypeMessage:
		return platform.EventKindArticleCreated, true
	case UpdateTypeEdit:
		return platform.EventKindArticleEdited, true
	case UpdateTypeDelete:
		return platform.EventKindArticleRetracted, true
	case UpdateTypeReactionAdd:
		return platform.EventKindArticleReactionAdded, true
	case UpdateTypeReactionRemove:
		return platform.EventKindArticleReactionRemoved, true
	case UpdateTypeMemberJoin:
		return platform.EventKindMemberJoined, true
	case UpdateTypeMemberLeave:
		return platform.EventKindMemberLeft, true
	case UpdateTypeRole:
		return platform.EventKindRoleUpdated, true
	default:
		return "", false
	}
}
