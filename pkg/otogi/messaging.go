package otogi

import "context"

// OutboundMessage is a message a module asked to send, fully addressed.
type OutboundMessage struct {
	// Platform is the destination platform.
	Platform PlatformID
	// Account is the sending account.
	Account AccountID
	// Conversation is the destination conversation.
	Conversation ConversationID
	// Event is the event whose handling produced the message.
	Event EventID
	// Module is the module that sent it.
	Module ModuleID
	// Key identifies this send; drivers use it to avoid duplicate sends on replay.
	Key IdempotencyKey
	// Message is the content.
	Message Message
}

// Messenger delivers outbound messages. Drivers implement it; with several
// drivers, the composition root routes by Platform and Account.
type Messenger interface {
	// Send delivers msg or returns why it could not.
	Send(ctx context.Context, msg OutboundMessage) error
}
