package otogi

import (
	"fmt"
	"net/url"
	"time"
)

// EventAction says what happened. Core actions are the constants below;
// drivers and modules may define custom actions as absolute URIs.
type EventAction string

const (
	// ActionMessageCreated is a new message.
	ActionMessageCreated EventAction = "message.created"
	// ActionMessageEdited is an edit of an existing message.
	ActionMessageEdited EventAction = "message.edited"
	// ActionMessageDeleted is a deletion of one or more messages.
	ActionMessageDeleted EventAction = "message.deleted"
	// ActionMessageService is a platform service message, such as a member
	// joining or a title change.
	ActionMessageService EventAction = "message.service"
)

// Valid reports whether a is a core action or an absolute URI.
func (a EventAction) Valid() bool {
	switch a {
	case ActionMessageCreated, ActionMessageEdited, ActionMessageDeleted, ActionMessageService:
		return true
	case "":
		return false
	}
	u, err := url.Parse(string(a))
	return err == nil && u.IsAbs()
}

// Direction says whether the bot's account received or sent the event.
type Direction string

const (
	// DirectionIncoming is an event from someone else.
	DirectionIncoming Direction = "incoming"
	// DirectionOutgoing is an event sent by the bot's own account, which user
	// accounts also observe.
	DirectionOutgoing Direction = "outgoing"
	// DirectionSystem is an event without a sender, such as a service notice.
	DirectionSystem Direction = "system"
)

// Completeness says how much of an event's content is available.
type Completeness string

const (
	// CompletenessComplete means all content is present.
	CompletenessComplete Completeness = "complete"
	// CompletenessPartial means some content is missing.
	CompletenessPartial Completeness = "partial"
	// CompletenessRedacted means the platform removed the content.
	CompletenessRedacted Completeness = "redacted"
	// CompletenessUnavailable means the content could not be fetched or decoded.
	CompletenessUnavailable Completeness = "unavailable"
)

// ConversationKind classifies a conversation.
type ConversationKind string

const (
	// ConversationDirect is a one-to-one chat.
	ConversationDirect ConversationKind = "direct"
	// ConversationGroup is a multi-member chat.
	ConversationGroup ConversationKind = "group"
	// ConversationChannel is a broadcast channel.
	ConversationChannel ConversationKind = "channel"
)

// Command is a parsed bot command, such as "/remind 10m tea".
type Command struct {
	// Name is the command without prefix or bot mention, lowercased.
	Name string `json:"name"`
	// Args is the text after the command, trimmed.
	Args string `json:"args,omitempty"`
	// Argv is Args split on whitespace.
	Argv []string `json:"argv,omitempty"`
}

// Conversation describes where an event happened.
type Conversation struct {
	// ID identifies the conversation on its platform.
	ID ConversationID `json:"id"`
	// Kind classifies the conversation when the driver knows it.
	Kind ConversationKind `json:"kind,omitempty"`
	// Title is the display title when the driver knows it.
	Title string `json:"title,omitempty"`
}

// Actor describes who caused an event.
type Actor struct {
	// ID identifies the actor on its platform.
	ID ActorID `json:"id"`
	// Name is the display name when the driver knows it.
	Name string `json:"name,omitempty"`
}

// ProviderLocator is driver-owned data that lets the driver find the source
// object again, for example to refetch media. Other components treat it as
// opaque.
type ProviderLocator struct {
	// Namespace is an absolute URI naming the format of Data.
	Namespace string `json:"namespace"`
	// Version is the format version of Data.
	Version uint32 `json:"version,omitempty"`
	// Data is the driver-defined locator.
	Data []byte `json:"data,omitempty"`
}

// Event is one platform event normalized into the protocol-neutral model.
type Event struct {
	// ID is the runtime identifier; see EventID.
	ID EventID `json:"id,omitempty"`
	// Platform is the source platform.
	Platform PlatformID `json:"platform"`
	// Account is the bot account that observed the event.
	Account AccountID `json:"account"`
	// Conversation is where the event happened.
	Conversation Conversation `json:"conversation"`
	// SourceID is the platform's own event or message ID, when it has one.
	SourceID SourceEventID `json:"source_id,omitempty"`
	// Actor is who caused the event; empty for system events.
	Actor Actor `json:"actor,omitzero"`
	// Action is what happened.
	Action EventAction `json:"action"`
	// Direction says whether the event was received or sent by the account.
	Direction Direction `json:"direction,omitempty"`
	// OccurredAt is when the event happened on the platform.
	OccurredAt time.Time `json:"occurred_at,omitzero"`
	// ReceivedAt is when the driver received the event.
	ReceivedAt time.Time `json:"received_at,omitzero"`
	// EditedAt is when the message was last edited, for edit events.
	EditedAt time.Time `json:"edited_at,omitzero"`
	// Completeness says how much of Message is available.
	Completeness Completeness `json:"completeness,omitempty"`
	// Locator lets the driver find the source object again.
	Locator ProviderLocator `json:"locator,omitzero"`
	// Message is the event's rich content and relations.
	Message Message `json:"message,omitzero"`
	// Command is set when the message invokes a bot command, by the command
	// parser middleware or by a driver with native commands.
	Command *Command `json:"command,omitempty"`
	// Mentioned reports that the message mentions the receiving account, as
	// with an @ to a bot in a group. A mention in the text has a mention
	// annotation whose Target is the account's own actor ID; a platform may
	// also strip it and only set Mentioned.
	Mentioned bool `json:"mentioned,omitempty"`
	// Extensions carry platform data that has no portable meaning yet.
	Extensions []Extension `json:"extensions,omitempty"`
}

// Destination returns the conversation the event happened in.
func (e Event) Destination() Destination {
	return Destination{Platform: e.Platform, Account: e.Account, Conversation: e.Conversation.ID}
}

// ActorKey is the platform-qualified actor ID, "<platform>:<actor id>"
// (for example "telegram:user:123"), as operator and allow lists in the
// config spell it. It is empty when the event has no actor.
func (e Event) ActorKey() string {
	if e.Actor.ID == "" {
		return ""
	}
	return string(e.Platform) + ":" + string(e.Actor.ID)
}

// Ref returns a reference to the event's message, for replies, edits,
// deletes, and reactions.
func (e Event) Ref() EventRef {
	return EventRef{Platform: e.Platform, Account: e.Account, Conversation: e.Conversation.ID, SourceEvent: e.SourceID, Locator: e.Locator}
}

// Validate reports an error wrapping ErrInvalid when e lacks its scope or
// action, or its message is structurally invalid.
func (e Event) Validate() error {
	switch {
	case e.Platform == "" || e.Account == "" || e.Conversation.ID == "":
		return fmt.Errorf("event %q: missing platform, account, or conversation: %w", e.ID, ErrInvalid)
	case !e.Action.Valid():
		return fmt.Errorf("event %q: action %q: %w", e.ID, e.Action, ErrInvalid)
	}
	if err := e.Message.Validate(); err != nil {
		return fmt.Errorf("event %q: %w", e.ID, err)
	}
	return nil
}
