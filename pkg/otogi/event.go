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
	ID           string
	Kind         EventKind
	OccurredAt   time.Time
	Platform     Platform
	TenantID     string
	Conversation Conversation
	Actor        Actor
	Message      *Message
	Mutation     *Mutation
	Reaction     *Reaction
	StateChange  *StateChange
	Metadata     map[string]string
}

// Conversation identifies the neutral destination where an event occurred.
type Conversation struct {
	ID    string
	Type  ConversationType
	Title string
}

// Actor identifies the user/account that initiated an event.
type Actor struct {
	ID          string
	Username    string
	DisplayName string
	IsBot       bool
}

// Message holds neutral message content including rich media.
type Message struct {
	ID        string
	ThreadID  string
	ReplyToID string
	Text      string
	Entities  []TextEntity
	Media     []MediaAttachment
}

// TextEntity marks a rich text fragment.
type TextEntity struct {
	Type   string
	Offset int
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
	ID        string
	Type      MediaType
	MIMEType  string
	FileName  string
	SizeBytes int64
	Caption   string
	URI       string
	Preview   *MediaPreview
}

// MediaPreview carries optional lightweight preview data.
type MediaPreview struct {
	MIMEType string
	Bytes    []byte
	Width    int
	Height   int
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
	Type            MutationType
	TargetMessageID string
	Before          *MessageSnapshot
	After           *MessageSnapshot
	Reason          string
}

// MessageSnapshot stores immutable message state snapshots for mutations.
type MessageSnapshot struct {
	Text  string
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
	MessageID string
	Emoji     string
	Action    ReactionAction
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
	Type      StateChangeType
	Member    *MemberChange
	Role      *RoleChange
	Migration *ChatMigration
}

// MemberChange captures join/leave transitions.
type MemberChange struct {
	Action   EventKind
	Member   Actor
	Inviter  *Actor
	Reason   string
	JoinedAt time.Time
}

// RoleChange captures privilege transitions.
type RoleChange struct {
	MemberID  string
	OldRole   string
	NewRole   string
	ChangedBy Actor
}

// ChatMigration captures conversation identifier migration.
type ChatMigration struct {
	FromConversationID string
	ToConversationID   string
	Reason             string
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
