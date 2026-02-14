# pkg/otogi Rules

## Purpose
`pkg/otogi` is the contract layer. Keep it implementation-agnostic.

## Allowed Contents
- Interfaces
- Domain events and DTOs
- Public enums/constants
- Public error types and sentinel errors (when justified)

## Forbidden
- Imports from `internal/*`
- Driver-specific logic (Telegram, plugin runtime, transport details)
- Goroutine lifecycle management and side-effectful startup logic

## Design Rules
- Keep interfaces minimal and behavior-focused.
- Document exported types/functions with clear semantics.
- Favor small interfaces that are easy to mock.
- Changes in this package are API changes; preserve backward compatibility where possible.

## Testing
- Contract tests should validate interface expectations and serialization invariants.
- Generate mocks from interfaces for kernel/driver tests.
