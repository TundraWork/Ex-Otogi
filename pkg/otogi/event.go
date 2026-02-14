package otogi

import (
	"fmt"
	"time"
)

// EventKind identifies a neutral domain event type.
type EventKind string

const (
	// EventKindMessageCreated is emitted when a new message is posted.
	EventKindMessageCreated EventKind = "message.created"
	// EventKindMessageEdited is emitted when an existing message is edited.
	EventKindMessageEdited EventKind = "message.edited"
	// EventKindMessageRetracted is emitted when a message is deleted/retracted.
	EventKindMessageRetracted EventKind = "message.retracted"
	// EventKindReactionAdded is emitted when a reaction is added to a message.
	EventKindReactionAdded EventKind = "reaction.added"
	// EventKindReactionRemoved is emitted when a reaction is removed from a message.
	EventKindReactionRemoved EventKind = "reaction.removed"
	// EventKindMemberJoined is emitted when a member joins a conversation.
	EventKindMemberJoined EventKind = "member.joined"
	// EventKindMemberLeft is emitted when a member leaves a conversation.
	EventKindMemberLeft EventKind = "member.left"
	// EventKindRoleUpdated is emitted when a member role changes.
	EventKindRoleUpdated EventKind = "role.updated"
	// EventKindChatMigrated is emitted when a conversation migrates IDs.
	EventKindChatMigrated EventKind = "chat.migrated"
)

// Platform identifies an external chat platform source.
type Platform string

const (
	// PlatformTelegram is Telegram.
	PlatformTelegram Platform = "telegram"
)

// ConversationType identifies conversation scope.
type ConversationType string

const (
	// ConversationTypePrivate is a direct/private conversation.
	ConversationTypePrivate ConversationType = "private"
	// ConversationTypeGroup is a group conversation.
	ConversationTypeGroup ConversationType = "group"
	// ConversationTypeChannel is a channel-style conversation.
	ConversationTypeChannel ConversationType = "channel"
)

// Event is the neutral protocol envelope that all drivers publish and modules consume.
//
// Event fields are intentionally composable: Message, Mutation, Reaction, and StateChange
// are optional payload branches selected by Kind to avoid platform-specific leakage.
type Event struct {
	// ID is a stable identifier for this event instance.
	ID string
	// Kind selects which payload branch is expected.
	Kind EventKind
	// OccurredAt is the source-platform timestamp for the event.
	OccurredAt time.Time
	// Platform identifies the upstream platform that produced the event.
	Platform Platform
	// TenantID scopes the event to a tenant/workspace when multi-tenant routing is used.
	TenantID string
	// Conversation identifies where the event happened.
	Conversation Conversation
	// Actor identifies who initiated the event when available.
	Actor Actor
	// Message carries message content for message-created events.
	Message *Message
	// Mutation carries before/after context for edit and retraction events.
	Mutation *Mutation
	// Reaction carries emoji reaction metadata for reaction events.
	Reaction *Reaction
	// StateChange carries membership, role, and migration transitions.
	StateChange *StateChange
	// Metadata stores optional driver-provided key/value context.
	Metadata map[string]string
}

// Conversation identifies the neutral destination where an event occurred.
type Conversation struct {
	// ID is the stable conversation identifier on the source platform.
	ID string
	// Type describes the conversation scope.
	Type ConversationType
	// Title is a best-effort display label for the conversation.
	Title string
}

// Actor identifies the user/account that initiated an event.
type Actor struct {
	// ID is the stable actor identifier on the source platform.
	ID string
	// Username is the platform handle when available.
	Username string
	// DisplayName is the human-readable actor name.
	DisplayName string
	// IsBot reports whether the actor is an automated account.
	IsBot bool
}

// Message holds neutral message content including rich media.
type Message struct {
	// ID is the message identifier on the source platform.
	ID string
	// ThreadID is the optional thread/topic identifier containing the message.
	ThreadID string
	// ReplyToID is the parent message identifier when this is a reply.
	ReplyToID string
	// Text is the normalized message text body.
	Text string
	// Entities describes formatted ranges inside Text.
	Entities []TextEntity
	// Media contains normalized attachments associated with the message.
	Media []MediaAttachment
}

// TextEntity marks a rich text fragment.
type TextEntity struct {
	// Type identifies the entity class (for example link, mention, or bold).
	Type string
	// Offset is the zero-based character offset in the message text.
	Offset int
	// Length is the character span of the entity.
	Length int
}

// MediaType identifies attachment media categories.
type MediaType string

const (
	// MediaTypePhoto identifies an image attachment.
	MediaTypePhoto MediaType = "photo"
	// MediaTypeVideo identifies a video attachment.
	MediaTypeVideo MediaType = "video"
	// MediaTypeDocument identifies a generic file attachment.
	MediaTypeDocument MediaType = "document"
	// MediaTypeAudio identifies an audio attachment.
	MediaTypeAudio MediaType = "audio"
)

// MediaAttachment represents rich media payload metadata.
type MediaAttachment struct {
	// ID is the stable attachment identifier when provided by the platform.
	ID string
	// Type is the normalized media category.
	Type MediaType
	// MIMEType is the attachment content type when known.
	MIMEType string
	// FileName is the original attachment filename when available.
	FileName string
	// SizeBytes is the attachment size in bytes when available.
	SizeBytes int64
	// Caption is the optional media caption text.
	Caption string
	// URI is the optional retrievable location for the attachment.
	URI string
	// Preview carries optional lightweight preview data.
	Preview *MediaPreview
}

// MediaPreview carries optional lightweight preview data.
type MediaPreview struct {
	// MIMEType is the preview content type.
	MIMEType string
	// Bytes holds preview bytes when inline previews are available.
	Bytes []byte
	// Width is the preview width in pixels.
	Width int
	// Height is the preview height in pixels.
	Height int
	// Duration is the preview duration for time-based media.
	Duration time.Duration
}

// MutationType identifies message mutation kind.
type MutationType string

const (
	// MutationTypeEdit indicates message edit.
	MutationTypeEdit MutationType = "edit"
	// MutationTypeRetraction indicates message deletion/retraction.
	MutationTypeRetraction MutationType = "retraction"
)

// Mutation holds before/after message mutation context.
type Mutation struct {
	// Type identifies the mutation operation.
	Type MutationType
	// TargetMessageID identifies the message affected by the mutation.
	TargetMessageID string
	// Before captures message state before mutation.
	Before *MessageSnapshot
	// After captures message state after mutation.
	After *MessageSnapshot
	// Reason carries optional platform-provided context for the mutation.
	Reason string
}

// MessageSnapshot stores immutable message state snapshots for mutations.
type MessageSnapshot struct {
	// Text is the immutable text snapshot.
	Text string
	// Media is the immutable media snapshot.
	Media []MediaAttachment
}

// ReactionAction identifies whether a reaction is being added or removed.
type ReactionAction string

const (
	// ReactionActionAdd indicates a reaction was added.
	ReactionActionAdd ReactionAction = "add"
	// ReactionActionRemove indicates a reaction was removed.
	ReactionActionRemove ReactionAction = "remove"
)

// Reaction holds neutral reaction/emoji metadata.
type Reaction struct {
	// MessageID identifies the message receiving the reaction mutation.
	MessageID string
	// Emoji is the normalized emoji token.
	Emoji string
	// Action identifies whether the emoji was added or removed.
	Action ReactionAction
}

// StateChangeType identifies conversation state transitions.
type StateChangeType string

const (
	// StateChangeTypeMember indicates join/leave mutations.
	StateChangeTypeMember StateChangeType = "member"
	// StateChangeTypeRole indicates role changes.
	StateChangeTypeRole StateChangeType = "role"
	// StateChangeTypeMigration indicates conversation ID migrations.
	StateChangeTypeMigration StateChangeType = "migration"
)

// StateChange wraps non-message platform state transitions.
type StateChange struct {
	// Type selects which state-change payload branch is set.
	Type StateChangeType
	// Member carries join/leave transitions.
	Member *MemberChange
	// Role carries member role transitions.
	Role *RoleChange
	// Migration carries conversation identifier migrations.
	Migration *ChatMigration
}

// MemberChange captures join/leave transitions.
type MemberChange struct {
	// Action is the member event kind (joined or left).
	Action EventKind
	// Member identifies the member affected by the transition.
	Member Actor
	// Inviter identifies who invited the member when available.
	Inviter *Actor
	// Reason carries optional platform context for the change.
	Reason string
	// JoinedAt is the join timestamp when provided by the source platform.
	JoinedAt time.Time
}

// RoleChange captures privilege transitions.
type RoleChange struct {
	// MemberID identifies the member whose role changed.
	MemberID string
	// OldRole is the previous role value.
	OldRole string
	// NewRole is the resulting role value.
	NewRole string
	// ChangedBy identifies the actor that performed the role change.
	ChangedBy Actor
}

// ChatMigration captures conversation identifier migration.
type ChatMigration struct {
	// FromConversationID is the previous conversation identifier.
	FromConversationID string
	// ToConversationID is the replacement conversation identifier.
	ToConversationID string
	// Reason carries optional platform context for the migration.
	Reason string
}

// Validate checks event envelope and payload coherence.
func (e *Event) Validate() error {
	if e == nil {
		return fmt.Errorf("%w: nil event", ErrInvalidEvent)
	}
	if e.ID == "" {
		return fmt.Errorf("%w: missing id", ErrInvalidEvent)
	}
	if e.Kind == "" {
		return fmt.Errorf("%w: missing kind", ErrInvalidEvent)
	}
	if e.OccurredAt.IsZero() {
		return fmt.Errorf("%w: missing occurred_at", ErrInvalidEvent)
	}
	if e.Conversation.ID == "" {
		return fmt.Errorf("%w: missing conversation id", ErrInvalidEvent)
	}

	return validatePayloadByKind(e)
}

// validatePayloadByKind enforces payload branch requirements for each event kind.
func validatePayloadByKind(e *Event) error {
	switch e.Kind {
	case EventKindMessageCreated:
		if e.Message == nil {
			return fmt.Errorf("%w: message.created requires message payload", ErrInvalidEvent)
		}
	case EventKindMessageEdited, EventKindMessageRetracted:
		if e.Mutation == nil {
			return fmt.Errorf("%w: mutation event requires mutation payload", ErrInvalidEvent)
		}
	case EventKindReactionAdded, EventKindReactionRemoved:
		if e.Reaction == nil {
			return fmt.Errorf("%w: reaction event requires reaction payload", ErrInvalidEvent)
		}
	case EventKindMemberJoined, EventKindMemberLeft, EventKindRoleUpdated, EventKindChatMigrated:
		if e.StateChange == nil {
			return fmt.Errorf("%w: state event requires state payload", ErrInvalidEvent)
		}
	default:
		return fmt.Errorf("%w: unsupported kind %q", ErrInvalidEvent, e.Kind)
	}

	return nil
}
