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

// MediaFetcher downloads media that drivers describe with an opaque source.
type MediaFetcher interface {
	// FetchMedia returns the bytes of src, as seen by the account at from.
	FetchMedia(ctx context.Context, from Destination, src MediaSource) ([]byte, error)
}
