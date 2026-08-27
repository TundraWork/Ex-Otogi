package otogi

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// EventID identifies one event admitted by the runtime.
type EventID struct{ value string }

// SourceEventID identifies an event in its source platform.
type SourceEventID struct{ value string }

// TraceID identifies distributed trace context supplied at admission.
type TraceID struct{ value string }

// CausalEventID identifies the runtime event that caused another event.
type CausalEventID struct{ value string }

// PlatformID identifies an event or message transport platform.
type PlatformID struct{ value string }

// AccountID identifies a platform account.
type AccountID struct{ value string }

// TenantID identifies an isolation tenant.
type TenantID struct{ value string }

// ConversationID identifies a conversation within platform and account scope.
type ConversationID struct{ value string }

// ActorID identifies the actor that originated an event.
type ActorID struct{ value string }

// ModuleID identifies a statically supplied module.
type ModuleID struct{ value string }

// IdempotencyKey identifies one logical outbound effect across retries.
type IdempotencyKey struct{ value string }

func validateIdentifier(value string) error {
	if value == "" || len(value) > 128 || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return fmt.Errorf("identifier: %w", ErrInvalid)
	}
	return nil
}

// NewEventID validates value and returns a comparable event identifier.
func NewEventID(value string) (EventID, error) {
	if err := validateIdentifier(value); err != nil {
		return EventID{}, err
	}
	return EventID{value}, nil
}

// NewSourceEventID validates value and returns a comparable source event identifier.
func NewSourceEventID(value string) (SourceEventID, error) {
	if err := validateIdentifier(value); err != nil {
		return SourceEventID{}, err
	}
	return SourceEventID{value}, nil
}

// NewTraceID validates value and returns a comparable trace identifier.
func NewTraceID(value string) (TraceID, error) {
	if err := validateIdentifier(value); err != nil {
		return TraceID{}, err
	}
	return TraceID{value}, nil
}

// NewCausalEventID validates value and returns a comparable causal event identifier.
func NewCausalEventID(value string) (CausalEventID, error) {
	if err := validateIdentifier(value); err != nil {
		return CausalEventID{}, err
	}
	return CausalEventID{value}, nil
}

// NewPlatformID validates value and returns a comparable platform identifier.
func NewPlatformID(value string) (PlatformID, error) {
	if err := validateIdentifier(value); err != nil {
		return PlatformID{}, err
	}
	return PlatformID{value}, nil
}

// NewAccountID validates value and returns a comparable account identifier.
func NewAccountID(value string) (AccountID, error) {
	if err := validateIdentifier(value); err != nil {
		return AccountID{}, err
	}
	return AccountID{value}, nil
}

// NewTenantID validates value and returns a comparable tenant identifier.
func NewTenantID(value string) (TenantID, error) {
	if err := validateIdentifier(value); err != nil {
		return TenantID{}, err
	}
	return TenantID{value}, nil
}

// NewConversationID validates value and returns a comparable conversation identifier.
func NewConversationID(value string) (ConversationID, error) {
	if err := validateIdentifier(value); err != nil {
		return ConversationID{}, err
	}
	return ConversationID{value}, nil
}

// NewActorID validates value and returns a comparable actor identifier.
func NewActorID(value string) (ActorID, error) {
	if err := validateIdentifier(value); err != nil {
		return ActorID{}, err
	}
	return ActorID{value}, nil
}

// NewModuleID validates value and returns a comparable module identifier.
func NewModuleID(value string) (ModuleID, error) {
	if err := validateIdentifier(value); err != nil {
		return ModuleID{}, err
	}
	return ModuleID{value}, nil
}

// NewIdempotencyKey validates value and returns a comparable idempotency key.
func NewIdempotencyKey(value string) (IdempotencyKey, error) {
	if err := validateIdentifier(value); err != nil {
		return IdempotencyKey{}, err
	}
	return IdempotencyKey{value}, nil
}

// String returns the exact validated identifier.
func (v EventID) String() string { return v.value }

// String returns the exact validated identifier.
func (v SourceEventID) String() string { return v.value }

// String returns the exact validated identifier.
func (v TraceID) String() string { return v.value }

// String returns the exact validated identifier.
func (v CausalEventID) String() string { return v.value }

// String returns the exact validated identifier.
func (v PlatformID) String() string { return v.value }

// String returns the exact validated identifier.
func (v AccountID) String() string { return v.value }

// String returns the exact validated identifier.
func (v TenantID) String() string { return v.value }

// String returns the exact validated identifier.
func (v ConversationID) String() string { return v.value }

// String returns the exact validated identifier.
func (v ActorID) String() string { return v.value }

// String returns the exact validated identifier.
func (v ModuleID) String() string { return v.value }

// String returns the exact validated key.
func (v IdempotencyKey) String() string { return v.value }
