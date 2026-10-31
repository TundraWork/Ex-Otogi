package otogi

import "context"

// Destination is a conversation on a platform account.
type Destination struct {
	// Platform is the destination platform.
	Platform PlatformID `json:"platform"`
	// Account is the account that acts there.
	Account AccountID `json:"account"`
	// Conversation is the destination conversation.
	Conversation ConversationID `json:"conversation"`
}

// EffectKind names a side effect a driver performs.
type EffectKind string

const (
	// EffectSend sends Message to To.
	EffectSend EffectKind = "send"
	// EffectEdit replaces the content of Ref with Message.
	EffectEdit EffectKind = "edit"
	// EffectDelete deletes Ref.
	EffectDelete EffectKind = "delete"
	// EffectReact sets the account's reaction on Ref to Reaction; an empty
	// Reaction removes it.
	EffectReact EffectKind = "react"
	// EffectStream shows a streamed message to To; see Streamer. Message is
	// its final content.
	EffectStream EffectKind = "stream"
)

// Effect is one side effect requested by a module. New kinds are added over
// time; a driver returns ErrUnsupported for kinds it does not implement.
type Effect struct {
	// Kind selects the effect.
	Kind EffectKind `json:"kind"`
	// Key identifies this effect across retries and replays; drivers use it
	// so the effect happens at most once.
	Key IdempotencyKey `json:"key"`
	// Module is the module requesting the effect.
	Module ModuleID `json:"module"`
	// Origin is the unit of work that produced the effect: an event ID, or a
	// job or timer run ID.
	Origin string `json:"origin"`
	// To is where a send goes.
	To Destination `json:"to,omitzero"`
	// Ref is the message an edit, delete, or react applies to.
	Ref EventRef `json:"ref,omitzero"`
	// Message is the content of a send or edit.
	Message Message `json:"message,omitzero"`
	// Reaction is the reaction of a react effect.
	Reaction string `json:"reaction,omitempty"`
}

// Effector performs effects. Drivers implement it; with several drivers, the
// composition root routes by platform and account.
type Effector interface {
	// Apply performs e and returns a reference to the message it created or
	// changed. Applying an already-applied Key repeats nothing.
	Apply(ctx context.Context, e Effect) (EventRef, error)
}

// Streamer is implemented by effectors that show streamed messages: messages
// whose content grows while they are written, such as a language model's
// reply. The kernel uses it for Invocation.Stream. Without one, the kernel
// sends the final content with an ordinary send.
type Streamer interface {
	// OpenStream starts a streamed message to e.To. e.Key identifies it, so a
	// replayed stream reuses the platform messages it already sent. Nothing
	// needs to be shown yet; a platform may indicate that a reply is coming.
	// ErrUnsupported means the destination cannot stream.
	OpenStream(ctx context.Context, e Effect) (DriverStream, error)
}

// DriverStream is one streamed message, as a driver shows it. The kernel
// calls it from one goroutine at a time and always closes it.
type DriverStream interface {
	// Update sets the whole content so far. The driver shows it now, or
	// reports shown false when the platform's rate limits make it wait; a
	// later Update or Close supersedes it. Update never waits out a rate
	// limit.
	Update(ctx context.Context, msg Message) (shown bool, err error)
	// Close shows msg as the final content, waiting out rate limits if it
	// must, releases the stream, and returns the platform messages holding
	// the content, in order. An empty msg shows nothing.
	Close(ctx context.Context, msg Message) ([]EventRef, error)
}

// MediaFetcher downloads media that drivers describe with an opaque source.
type MediaFetcher interface {
	// FetchMedia returns the bytes of src, as seen by the account at from.
	FetchMedia(ctx context.Context, from Destination, src MediaSource) ([]byte, error)
}
