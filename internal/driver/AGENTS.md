# internal/driver Rules

## Purpose
`internal/driver` implements platform adapters (Telegram) and module/plugin wiring.

## Dependency Rules
- Allowed: `internal/kernel`, `pkg/otogi`, SDK clients, transport adapters.
- Drivers must not own business rules that belong in kernel.

## Integration Rules
- Translate external events into `pkg/otogi` contracts.
- Keep I/O concerns (serialization, transport retries, client sessions) within driver boundaries.
- Every external call must use context with timeout/deadline policy.

## Plugin Rules
- Plugins/modules must have explicit lifecycle hooks with stop semantics.
- Hot-plug paths must be idempotent and safe under concurrent reload attempts.

## Reliability
- Recover panics at SDK callback boundaries and pass failures upstream.
- Never swallow driver errors; wrap and return with operation context.

## Documentation Rules
- Exported methods, exported interface methods, and exported struct fields must have Godoc.
- Exported package-level functions/types/vars/consts must also have Godoc.
- Comments should communicate behavior and integration contracts.

## Testing
- Use interface mocks for kernel boundaries.
- Add integration tests with fake clients for Telegram/session behavior when feasible.

## Quality Checks
- After each task, run:
  - `make fmt`
  - `make lint`
  - `make arch-check`
  - `make test`
