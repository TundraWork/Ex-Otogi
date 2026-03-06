package otogi

import (
	"context"
	"fmt"
	"time"
)

// ServiceModerationDispatcher is the canonical service registry key for moderation operations.
const ServiceModerationDispatcher = "otogi.moderation_dispatcher"

// ModerationDispatcher sends neutral moderation operations to one sink adapter.
//
// Implementations translate neutral restriction semantics into platform-specific
// RPC calls. Not all platforms support every restriction flag; implementations
// should apply best-effort mapping and return ErrOutboundUnsupported for
// capabilities the platform cannot satisfy.
type ModerationDispatcher interface {
	// RestrictMember applies permission restrictions to a member in a conversation.
	RestrictMember(ctx context.Context, request RestrictMemberRequest) error
}

// RestrictMemberRequest describes a member permission restriction operation.
type RestrictMemberRequest struct {
	// Target identifies the conversation where the restriction applies.
	Target OutboundTarget
	// MemberID is the platform-specific member identifier to restrict.
	MemberID string
	// Permissions describes which capabilities are granted after the restriction.
	// Flags set to true indicate the member CAN perform that action.
	Permissions MemberPermissions
	// UntilDate is the restriction expiry. Zero value means the restriction
	// is applied indefinitely (or until the platform's maximum, whichever is shorter).
	UntilDate time.Time
}

// Validate checks the request envelope before dispatch.
func (r RestrictMemberRequest) Validate() error {
	if err := r.Target.Validate(); err != nil {
		return fmt.Errorf("validate restrict member target: %w", err)
	}
	if r.MemberID == "" {
		return fmt.Errorf("%w: missing member id", ErrInvalidOutboundRequest)
	}

	return nil
}

// MemberPermissions describes platform-neutral member capability flags.
//
// Each flag set to true means the member is ALLOWED to perform that action.
// Platforms map these neutral flags to their own permission model.
type MemberPermissions struct {
	// SendMessages allows sending text messages.
	SendMessages bool
	// SendMedia allows sending photos, videos, documents, and audio.
	SendMedia bool
	// SendPolls allows sending polls.
	SendPolls bool
	// SendOther allows sending stickers, GIFs, games, and inline bot results.
	SendOther bool
	// EmbedLinks allows embedding link previews.
	EmbedLinks bool
	// ChangeInfo allows changing the conversation title, photo, and description.
	ChangeInfo bool
	// InviteUsers allows inviting new members.
	InviteUsers bool
	// PinMessages allows pinning messages.
	PinMessages bool
	// ManageTopics allows creating, deleting, or renaming forum topics.
	ManageTopics bool
}

// AllPermissionsGranted returns a MemberPermissions with every flag set to true.
func AllPermissionsGranted() MemberPermissions {
	return MemberPermissions{
		SendMessages: true,
		SendMedia:    true,
		SendPolls:    true,
		SendOther:    true,
		EmbedLinks:   true,
		ChangeInfo:   true,
		InviteUsers:  true,
		PinMessages:  true,
		ManageTopics: true,
	}
}

// NoPermissionsGranted returns a MemberPermissions with every flag set to false.
func NoPermissionsGranted() MemberPermissions {
	return MemberPermissions{}
}
