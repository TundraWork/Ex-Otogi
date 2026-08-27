package otogi

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Invocation exposes effects scoped to one module delivery.
type Invocation interface {
	// Send sends a rich message once for a stable effect slot within the delivery.
	Send(context.Context, string, Message) error
}

// Messenger delivers fully scoped immutable outbound messages.
type Messenger interface {
	// Send delivers one fully validated outbound message or returns its delivery error.
	Send(context.Context, OutboundMessage) error
}

// OutboundMessageParams supplies values used to construct an OutboundMessage.
type OutboundMessageParams struct {
	// Tenant identifies the destination tenant.
	Tenant TenantID
	// Platform identifies the destination platform.
	Platform PlatformID
	// Account identifies the destination account.
	Account AccountID
	// Conversation identifies the destination conversation.
	Conversation ConversationID
	// OriginatingEvent identifies the event that caused the message.
	OriginatingEvent EventID
	// OriginatingModule identifies the producing module.
	OriginatingModule ModuleID
	// Message contains the rich outbound content, relations, and extensions.
	Message Message
	// IdempotencyKey identifies the logical effect across retries.
	IdempotencyKey IdempotencyKey
}

// OutboundMessage is an immutable scoped message ready for an adapter.
type OutboundMessage struct {
	tenant            TenantID
	platform          PlatformID
	account           AccountID
	conversation      ConversationID
	originatingEvent  EventID
	originatingModule ModuleID
	message           Message
	idempotencyKey    IdempotencyKey
}

// NewOutboundMessage validates every required scope, origin, content, and idempotency value.
func NewOutboundMessage(p OutboundMessageParams) (OutboundMessage, error) {
	if p.Tenant.value == "" || p.Platform.value == "" || p.Account.value == "" || p.Conversation.value == "" || p.OriginatingEvent.value == "" || p.OriginatingModule.value == "" || len(p.Message.parts) == 0 || p.IdempotencyKey.value == "" {
		return OutboundMessage{}, fmt.Errorf("outbound message: %w", ErrInvalid)
	}
	return OutboundMessage{tenant: p.Tenant, platform: p.Platform, account: p.Account, conversation: p.Conversation, originatingEvent: p.OriginatingEvent, originatingModule: p.OriginatingModule, message: p.Message, idempotencyKey: p.IdempotencyKey}, nil
}

// Tenant returns the destination tenant.
func (m OutboundMessage) Tenant() TenantID { return m.tenant }

// Platform returns the destination platform.
func (m OutboundMessage) Platform() PlatformID { return m.platform }

// Account returns the destination platform account.
func (m OutboundMessage) Account() AccountID { return m.account }

// Conversation returns the destination conversation.
func (m OutboundMessage) Conversation() ConversationID { return m.conversation }

// OriginatingEvent returns the event that caused the message.
func (m OutboundMessage) OriginatingEvent() EventID { return m.originatingEvent }

// OriginatingModule returns the module that produced the message.
func (m OutboundMessage) OriginatingModule() ModuleID { return m.originatingModule }

// Message returns the immutable rich content.
func (m OutboundMessage) Message() Message { return m.message }

// IdempotencyKey returns the key for the logical outbound effect.
func (m OutboundMessage) IdempotencyKey() IdempotencyKey { return m.idempotencyKey }

func newMessagingIdempotencyKey(event EventID, module ModuleID, effectSlot string) (IdempotencyKey, error) {
	if event.value == "" || module.value == "" || effectSlot == "" || len(effectSlot) > 64 || !utf8.ValidString(effectSlot) || strings.TrimSpace(effectSlot) != effectSlot {
		return IdempotencyKey{}, fmt.Errorf("messaging key: %w", ErrInvalid)
	}
	h := sha256.New()
	var size [8]byte
	for _, part := range []string{event.value, module.value, "core.messaging", effectSlot} {
		binary.BigEndian.PutUint64(size[:], uint64(len(part)))
		_, _ = h.Write(size[:])
		_, _ = h.Write([]byte(part))
	}
	return NewIdempotencyKey(hex.EncodeToString(h.Sum(nil)))
}
