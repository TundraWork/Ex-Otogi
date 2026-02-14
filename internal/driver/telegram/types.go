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
	ID         string
	Type       UpdateType
	OccurredAt time.Time
	Chat       ChatRef
	Actor      ActorRef
	Message    *MessagePayload
	Edit       *EditPayload
	Delete     *DeletePayload
	Reaction   *ReactionPayload
	Member     *MemberPayload
	Role       *RolePayload
	Migration  *MigrationPayload
	Metadata   map[string]string
}

// ChatRef identifies Telegram chat context.
type ChatRef struct {
	ID    string
	Title string
	Type  otogi.ConversationType
}

// ActorRef identifies Telegram actor context.
type ActorRef struct {
	ID          string
	Username    string
	DisplayName string
	IsBot       bool
}

// MessagePayload represents a Telegram message projection.
type MessagePayload struct {
	ID        string
	ThreadID  string
	ReplyToID string
	Text      string
	Entities  []otogi.TextEntity
	Media     []MediaPayload
}

// MediaPayload represents Telegram media metadata.
type MediaPayload struct {
	ID        string
	Type      otogi.MediaType
	MIMEType  string
	FileName  string
	SizeBytes int64
	Caption   string
	URI       string
	Preview   *MediaPreviewPayload
}

// MediaPreviewPayload contains optional preview bytes and dimensions.
type MediaPreviewPayload struct {
	MIMEType string
	Bytes    []byte
	Width    int
	Height   int
	Duration time.Duration
}

// EditPayload captures before/after message content for edits.
type EditPayload struct {
	MessageID string
	Before    *SnapshotPayload
	After     *SnapshotPayload
	Reason    string
}

// SnapshotPayload captures immutable message snapshots.
type SnapshotPayload struct {
	Text  string
	Media []MediaPayload
}

// DeletePayload captures message deletion metadata.
type DeletePayload struct {
	MessageID string
	Reason    string
}

// ReactionPayload captures emoji reaction metadata.
type ReactionPayload struct {
	MessageID string
	Emoji     string
}

// MemberPayload captures join/leave transitions.
type MemberPayload struct {
	Member   ActorRef
	Inviter  *ActorRef
	Reason   string
	JoinedAt time.Time
}

// RolePayload captures role mutation metadata.
type RolePayload struct {
	MemberID  string
	OldRole   string
	NewRole   string
	ChangedBy ActorRef
}

// MigrationPayload captures Telegram chat migration metadata.
type MigrationPayload struct {
	FromChatID string
	ToChatID   string
	Reason     string
}
