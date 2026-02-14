package otogi

import (
	"context"
	"fmt"
)

// ServiceOutboundDispatcher is the canonical service registry key for outbound messaging.
const ServiceOutboundDispatcher = "otogi.outbound_dispatcher"

// OutboundDispatcher sends neutral outbound operations to a platform adapter.
//
// Implementations should enforce platform-specific constraints while preserving
// these protocol-level request semantics.
type OutboundDispatcher interface {
	// SendMessage publishes a new outbound message to a destination conversation.
	SendMessage(ctx context.Context, request SendMessageRequest) (*OutboundMessage, error)
	// EditMessage mutates an existing outbound message by ID.
	EditMessage(ctx context.Context, request EditMessageRequest) error
	// DeleteMessage removes an existing outbound message by ID.
	DeleteMessage(ctx context.Context, request DeleteMessageRequest) error
	// SetReaction adds or removes a reaction on an existing message.
	SetReaction(ctx context.Context, request SetReactionRequest) error
}

// OutboundTarget identifies where an outbound operation should be delivered.
type OutboundTarget struct {
	// Platform identifies the downstream chat platform.
	Platform Platform
	// Conversation identifies the destination conversation.
	Conversation Conversation
}

// Validate checks target identity fields used for outbound routing.
func (t OutboundTarget) Validate() error {
	if t.Platform == "" {
		return fmt.Errorf("%w: missing platform", ErrInvalidOutboundRequest)
	}
	if t.Conversation.ID == "" {
		return fmt.Errorf("%w: missing conversation id", ErrInvalidOutboundRequest)
	}
	if t.Conversation.Type == "" {
		return fmt.Errorf("%w: missing conversation type", ErrInvalidOutboundRequest)
	}

	return nil
}

// OutboundTargetFromEvent derives a destination target from an inbound event.
func OutboundTargetFromEvent(event *Event) (OutboundTarget, error) {
	if event == nil {
		return OutboundTarget{}, fmt.Errorf("%w: nil event", ErrInvalidOutboundRequest)
	}
	target := OutboundTarget{
		Platform:     event.Platform,
		Conversation: event.Conversation,
	}
	if err := target.Validate(); err != nil {
		return OutboundTarget{}, fmt.Errorf("derive target from event %s: %w", event.Kind, err)
	}

	return target, nil
}

// OutboundMessage identifies a message successfully emitted by the dispatcher.
type OutboundMessage struct {
	// ID is the destination-platform message identifier.
	ID string
	// Target is the destination where this message was delivered.
	Target OutboundTarget
}

// SendMessageRequest describes a new outbound text message.
type SendMessageRequest struct {
	// Target identifies where the message should be sent.
	Target OutboundTarget
	// Text is the message body.
	Text string
	// ReplyToMessageID optionally links this message as a reply.
	ReplyToMessageID string
	// DisableLinkPreview disables link previews when supported by the platform.
	DisableLinkPreview bool
	// Silent suppresses destination-side notifications when supported.
	Silent bool
}

// Validate checks the request envelope before dispatch.
func (r SendMessageRequest) Validate() error {
	if err := r.Target.Validate(); err != nil {
		return fmt.Errorf("validate send message target: %w", err)
	}
	if r.Text == "" {
		return fmt.Errorf("%w: missing message text", ErrInvalidOutboundRequest)
	}

	return nil
}

// EditMessageRequest describes a text edit for an existing message.
type EditMessageRequest struct {
	// Target identifies where the message exists.
	Target OutboundTarget
	// MessageID identifies which message should be edited.
	MessageID string
	// Text is the replacement message body.
	Text string
	// DisableLinkPreview disables link previews when supported by the platform.
	DisableLinkPreview bool
}

// Validate checks the request envelope before dispatch.
func (r EditMessageRequest) Validate() error {
	if err := r.Target.Validate(); err != nil {
		return fmt.Errorf("validate edit message target: %w", err)
	}
	if r.MessageID == "" {
		return fmt.Errorf("%w: missing message id", ErrInvalidOutboundRequest)
	}
	if r.Text == "" {
		return fmt.Errorf("%w: missing message text", ErrInvalidOutboundRequest)
	}

	return nil
}

// DeleteMessageRequest describes message deletion behavior.
type DeleteMessageRequest struct {
	// Target identifies where the message exists.
	Target OutboundTarget
	// MessageID identifies which message should be deleted.
	MessageID string
	// Revoke requests deletion for all participants when supported.
	Revoke bool
}

// Validate checks the request envelope before dispatch.
func (r DeleteMessageRequest) Validate() error {
	if err := r.Target.Validate(); err != nil {
		return fmt.Errorf("validate delete message target: %w", err)
	}
	if r.MessageID == "" {
		return fmt.Errorf("%w: missing message id", ErrInvalidOutboundRequest)
	}

	return nil
}

// SetReactionRequest describes reaction mutation behavior.
type SetReactionRequest struct {
	// Target identifies where the message exists.
	Target OutboundTarget
	// MessageID identifies which message should be reacted to.
	MessageID string
	// Emoji is the reaction token to apply.
	Emoji string
	// Action determines whether the reaction is added or removed.
	Action ReactionAction
}

// Validate checks the request envelope before dispatch.
func (r SetReactionRequest) Validate() error {
	if err := r.Target.Validate(); err != nil {
		return fmt.Errorf("validate set reaction target: %w", err)
	}
	if r.MessageID == "" {
		return fmt.Errorf("%w: missing message id", ErrInvalidOutboundRequest)
	}
	if r.Action != ReactionActionAdd && r.Action != ReactionActionRemove {
		return fmt.Errorf("%w: unsupported reaction action %q", ErrInvalidOutboundRequest, r.Action)
	}
	if r.Action == ReactionActionAdd && r.Emoji == "" {
		return fmt.Errorf("%w: missing reaction emoji", ErrInvalidOutboundRequest)
	}

	return nil
}
