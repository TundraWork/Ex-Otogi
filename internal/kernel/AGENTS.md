# internal/kernel Rules

## Purpose
`internal/kernel` contains core orchestration and business logic.

## Dependency Rules
- Allowed: `pkg/otogi`, stdlib, narrowly scoped utility dependencies.
- Forbidden: direct imports from `internal/driver`.
- Kernel must depend on interfaces, not concrete platform SDKs.

## Concurrency Rules
- All async flows must be cancellable via `context.Context`.
- Use `errgroup.WithContext` for coordinated worker lifecycles.
- Event bus fan-out must avoid unbounded blocking; define buffering and drop/backpressure policy explicitly.
- No orphan goroutines; termination must be testable.

## Reliability Rules
- Recover panic at worker boundaries and propagate structured error signals.
- Keep retry/backoff explicit and bounded.

## Documentation Rules
- Exported methods, exported interface methods, and exported struct fields must have Godoc.
- Exported package-level functions/types/vars/consts must also have Godoc.
- Documentation must explain intent, invariants, and behavior semantics.

## Testing
- Use table-driven tests for logic-heavy functions.
- Use mocks/fakes for interfaces from `pkg/otogi`.
- Include race-focused tests for event bus, plugin scheduling, and shutdown paths.

## Quality Checks
- After each task, run:
  - `make fmt`
  - `make lint`
  - `make arch-check`
  - `make test`
