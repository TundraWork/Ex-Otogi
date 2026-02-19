package otogi

import (
	"fmt"
	"time"
	"unicode/utf8"
)

// EventKind identifies a neutral domain event type.
type EventKind string

const (
	// EventKindArticleCreated is emitted when a new article is posted.
	EventKindArticleCreated EventKind = "article.created"
	// EventKindArticleEdited is emitted when an existing article is edited.
	EventKindArticleEdited EventKind = "article.edited"
	// EventKindArticleRetracted is emitted when an article is deleted/retracted.
	EventKindArticleRetracted EventKind = "article.retracted"
	// EventKindArticleReactionAdded is emitted when a reaction is added to an article.
	EventKindArticleReactionAdded EventKind = "article.reaction.added"
	// EventKindArticleReactionRemoved is emitted when a reaction is removed from an article.
	EventKindArticleReactionRemoved EventKind = "article.reaction.removed"
	// EventKindCommandReceived is emitted when one inbound article is parsed as one ordinary command.
	EventKindCommandReceived EventKind = "command.received"
	// EventKindSystemCommandReceived is emitted when one inbound article is parsed as one system command.
	EventKindSystemCommandReceived EventKind = "system_command.received"
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
// Event fields are intentionally composable: Article, Mutation, Reaction, and
// StateChange are optional payload branches selected by Kind to avoid platform-specific
// leakage.
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
	// Article carries content for article-created events.
	Article *Article
	// Mutation carries before/after context for edit and retraction events.
	Mutation *ArticleMutation
	// Reaction carries emoji reaction metadata for article reaction events.
	Reaction *Reaction
	// Command carries parsed command metadata for command-received events.
	Command *CommandInvocation
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

// Article holds neutral article content including rich media.
type Article struct {
	// ID is the article identifier on the source platform.
	ID string
	// ThreadID is the optional thread/topic identifier containing the article.
	ThreadID string
	// ReplyToArticleID is the parent article identifier when this is a reply.
	ReplyToArticleID string
	// Text is the normalized article text body.
	Text string
	// Entities describes formatted ranges inside Text.
	Entities []TextEntity
	// Media contains normalized attachments associated with the article.
	Media []MediaAttachment
	// Reactions contains the projected reaction summary derived from reaction events.
	Reactions []ArticleReaction
}

// ArticleReaction summarizes projected reaction state for one emoji.
type ArticleReaction struct {
	// Emoji identifies the reaction emoji token.
	Emoji string
	// Count is the number of active reactions for Emoji.
	Count int
}

// TextEntityType identifies a normalized rich-text annotation type.
type TextEntityType string

const (
	// TextEntityTypeUnknown identifies an unknown or passthrough entity.
	TextEntityTypeUnknown TextEntityType = "unknown"
	// TextEntityTypeMention identifies a textual @mention entity.
	TextEntityTypeMention TextEntityType = "mention"
	// TextEntityTypeHashtag identifies a hashtag entity.
	TextEntityTypeHashtag TextEntityType = "hashtag"
	// TextEntityTypeBotCommand identifies a bot command entity.
	TextEntityTypeBotCommand TextEntityType = "bot_command"
	// TextEntityTypeURL identifies an auto-detected URL entity.
	TextEntityTypeURL TextEntityType = "url"
	// TextEntityTypeEmail identifies an email entity.
	TextEntityTypeEmail TextEntityType = "email"
	// TextEntityTypeBold identifies bold text formatting.
	TextEntityTypeBold TextEntityType = "bold"
	// TextEntityTypeItalic identifies italic text formatting.
	TextEntityTypeItalic TextEntityType = "italic"
	// TextEntityTypeCode identifies inline monospace formatting.
	TextEntityTypeCode TextEntityType = "code"
	// TextEntityTypePre identifies preformatted code block formatting.
	TextEntityTypePre TextEntityType = "pre"
	// TextEntityTypeTextURL identifies clickable text with a dedicated URL.
	TextEntityTypeTextURL TextEntityType = "text_url"
	// TextEntityTypeMentionName identifies a mention bound to one concrete user ID.
	TextEntityTypeMentionName TextEntityType = "mention_name"
	// TextEntityTypePhone identifies a phone-number entity.
	TextEntityTypePhone TextEntityType = "phone"
	// TextEntityTypeCashtag identifies a cashtag entity.
	TextEntityTypeCashtag TextEntityType = "cashtag"
	// TextEntityTypeBankCard identifies a bank-card-number entity.
	TextEntityTypeBankCard TextEntityType = "bank_card"
	// TextEntityTypeUnderline identifies underlined formatting.
	TextEntityTypeUnderline TextEntityType = "underline"
	// TextEntityTypeStrike identifies strikethrough formatting.
	TextEntityTypeStrike TextEntityType = "strike"
	// TextEntityTypeBlockquote identifies block-quote formatting.
	TextEntityTypeBlockquote TextEntityType = "blockquote"
	// TextEntityTypeSpoiler identifies spoiler formatting.
	TextEntityTypeSpoiler TextEntityType = "spoiler"
	// TextEntityTypeCustomEmoji identifies custom emoji entity rendering.
	TextEntityTypeCustomEmoji TextEntityType = "custom_emoji"
)

// TextEntity marks a rich text fragment.
type TextEntity struct {
	// Type identifies the entity class (for example link, mention, or bold).
	Type TextEntityType
	// Offset is the zero-based character offset in the article text.
	Offset int
	// Length is the character span of the entity.
	Length int
	// URL is the destination URL for text_url entities.
	URL string
	// Language identifies the programming language tag for pre entities.
	Language string
	// MentionUserID identifies the user bound to mention_name entities.
	MentionUserID string
	// CustomEmojiID identifies the source custom-emoji identifier.
	CustomEmojiID string
	// Collapsed indicates whether blockquote entities should be collapsed by default.
	Collapsed bool
}

// ValidateTextEntities validates rich-text entities against one article text body.
//
// Offsets and lengths are interpreted as Unicode code-point indexes.
func ValidateTextEntities(text string, entities []TextEntity) error {
	if len(entities) == 0 {
		return nil
	}

	textRuneCount := utf8.RuneCountInString(text)
	for index, entity := range entities {
		if entity.Type == "" {
			return fmt.Errorf("entity[%d]: missing type", index)
		}
		if entity.Offset < 0 {
			return fmt.Errorf("entity[%d]: invalid negative offset %d", index, entity.Offset)
		}
		if entity.Length <= 0 {
			return fmt.Errorf("entity[%d]: invalid length %d", index, entity.Length)
		}
		end := entity.Offset + entity.Length
		if end > textRuneCount {
			return fmt.Errorf(
				"entity[%d]: range [%d,%d) exceeds text length %d",
				index,
				entity.Offset,
				end,
				textRuneCount,
			)
		}

		switch entity.Type {
		case TextEntityTypeTextURL:
			if entity.URL == "" {
				return fmt.Errorf("entity[%d]: text_url requires url", index)
			}
		case TextEntityTypeMentionName:
			if entity.MentionUserID == "" {
				return fmt.Errorf("entity[%d]: mention_name requires mention_user_id", index)
			}
		case TextEntityTypeCustomEmoji:
			if entity.CustomEmojiID == "" {
				return fmt.Errorf("entity[%d]: custom_emoji requires custom_emoji_id", index)
			}
		default:
		}
	}

	return nil
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

// MutationType identifies article mutation kind.
type MutationType string

const (
	// MutationTypeEdit indicates article edit.
	MutationTypeEdit MutationType = "edit"
	// MutationTypeRetraction indicates article deletion/retraction.
	MutationTypeRetraction MutationType = "retraction"
)

// ArticleMutation holds before/after article mutation context.
type ArticleMutation struct {
	// Type identifies the mutation operation.
	Type MutationType
	// TargetArticleID identifies the article affected by the mutation.
	TargetArticleID string
	// ChangedAt is when the mutation happened on the source platform when known.
	ChangedAt *time.Time
	// Before captures article state before mutation.
	Before *ArticleSnapshot
	// After captures article state after mutation.
	After *ArticleSnapshot
	// Reason carries optional platform-provided context for the mutation.
	Reason string
}

// ArticleSnapshot stores immutable article state snapshots for mutations.
type ArticleSnapshot struct {
	// Text is the immutable text snapshot.
	Text string
	// Entities stores immutable rich-text entities aligned with Text.
	Entities []TextEntity
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
	// ArticleID identifies the article receiving the reaction mutation.
	ArticleID string
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

// StateChange wraps non-article platform state transitions.
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
	case EventKindArticleCreated:
		if e.Article == nil {
			return fmt.Errorf("%w: article.created requires article payload", ErrInvalidEvent)
		}
		if e.Article.ID == "" {
			return fmt.Errorf("%w: article.created requires article id", ErrInvalidEvent)
		}
		if err := ValidateTextEntities(e.Article.Text, e.Article.Entities); err != nil {
			return fmt.Errorf("%w: article.created invalid entities: %w", ErrInvalidEvent, err)
		}
	case EventKindArticleEdited, EventKindArticleRetracted:
		if e.Mutation == nil {
			return fmt.Errorf("%w: article mutation event requires mutation payload", ErrInvalidEvent)
		}
		if e.Mutation.TargetArticleID == "" {
			return fmt.Errorf("%w: article mutation event requires target article id", ErrInvalidEvent)
		}
		if err := validateMutationSnapshotEntities(e.Mutation); err != nil {
			return fmt.Errorf("%w: mutation invalid entities: %w", ErrInvalidEvent, err)
		}
	case EventKindArticleReactionAdded, EventKindArticleReactionRemoved:
		if e.Reaction == nil {
			return fmt.Errorf("%w: article reaction event requires reaction payload", ErrInvalidEvent)
		}
		if e.Reaction.ArticleID == "" {
			return fmt.Errorf("%w: article reaction event requires reaction article id", ErrInvalidEvent)
		}
	case EventKindCommandReceived, EventKindSystemCommandReceived:
		if e.Command == nil {
			return fmt.Errorf("%w: command event requires command payload", ErrInvalidEvent)
		}
		if err := e.Command.Validate(); err != nil {
			return fmt.Errorf("%w: command event invalid command payload: %w", ErrInvalidEvent, err)
		}
		if e.Article == nil {
			return fmt.Errorf("%w: command event requires article payload", ErrInvalidEvent)
		}
		if e.Article.ID == "" {
			return fmt.Errorf("%w: command event requires article id", ErrInvalidEvent)
		}
		if err := ValidateTextEntities(e.Article.Text, e.Article.Entities); err != nil {
			return fmt.Errorf("%w: command event invalid article entities: %w", ErrInvalidEvent, err)
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

func validateMutationSnapshotEntities(mutation *ArticleMutation) error {
	if mutation == nil {
		return nil
	}
	if mutation.Before != nil {
		if err := ValidateTextEntities(mutation.Before.Text, mutation.Before.Entities); err != nil {
			return fmt.Errorf("before snapshot: %w", err)
		}
	}
	if mutation.After != nil {
		if err := ValidateTextEntities(mutation.After.Text, mutation.After.Entities); err != nil {
			return fmt.Errorf("after snapshot: %w", err)
		}
	}

	return nil
}
