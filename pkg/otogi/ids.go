package otogi

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
)

// EventID identifies one event inside the runtime. Drivers with a durable
// inbox supply a stable ID so a replayed event keeps it; otherwise the kernel
// assigns one at admission.
type EventID string

// SourceEventID is the platform's own identifier for an event or message.
type SourceEventID string

// PlatformID names a platform, for example "telegram".
type PlatformID string

// AccountID identifies the bot's account on a platform.
type AccountID string

// ConversationID identifies a chat, group, channel, or room on a platform.
type ConversationID string

// ActorID identifies a user or bot on a platform.
type ActorID string

// ModuleID identifies a module. It is stable across restarts because it is
// part of idempotency keys.
type ModuleID string

// MiddlewareID identifies a middleware.
type MiddlewareID string

// PartID identifies a part within one message.
type PartID string

// IdempotencyKey identifies one logical side effect. Drivers use it to avoid
// repeating an effect when an event is replayed.
type IdempotencyKey string

// IdempotencyKeyFor derives the key for the effect named slot that module
// performs while working on origin (an event ID, or a job or timer run ID).
// The same inputs always give the same key.
func IdempotencyKeyFor(origin string, module ModuleID, slot string) IdempotencyKey {
	h := sha256.New()
	var size [8]byte
	for _, part := range []string{origin, string(module), slot} {
		binary.BigEndian.PutUint64(size[:], uint64(len(part)))
		_, _ = h.Write(size[:])
		_, _ = h.Write([]byte(part))
	}
	return IdempotencyKey(hex.EncodeToString(h.Sum(nil)))
}
