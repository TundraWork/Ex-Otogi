# Ex-Otogi Engineering Rules

This file is the working index for Ex-Otogi on the `next` branch. Domain detail lives in `docs/` (start at `docs/README.md`). Direct user instructions override both.

## Priorities

In order:

1. **Operability (运维便捷性).** An operator can install, configure, run, inspect, back up, restore, and upgrade the bot without reading source. Every event, module outcome, outbound action, and failure is visible and queryable.
2. **Maintainability (可维护性).** Our own code stays lean, direct, and easy to change, leaving room for complex features later. Less code beats clever code.
3. **Design over implementation.** Spend the thinking on architecture and contracts. Then implement the simplest efficient approach that meets the goal.
4. **Flexibility.** The protocol-neutral rich event model and a rich compiled-in module SDK let new platforms and modules be added without touching the kernel.

## What not to do

- No hand-rolled infrastructure: when a modern, mature, well-maintained library solves the problem (serialization, HTTP, SQL, migrations, config, logging, lint rules), use it instead of writing our own. Heavy dependencies are fine if they are high quality.
- No over-engineering: no speculative abstractions, configuration knobs, or layers without a current use. Lean is not closed: keep the extension seams the roadmap needs. The rich event model and driver contract stay protocol-neutral, with no platform assumptions in `pkg/otogi` or the kernel. The module contract grows new capabilities additively.
- No over-defensive programming: no validation, permission checks, retries, fallbacks, or tests that guard against failures that cannot realistically happen. Every safeguard names the real failure it prevents.
- No data hiding: telemetry, event logs, and diagnostics are never redacted or silently trimmed. Message content is recorded in full. Credentials are kept out simply by not logging them.
- No ceremony: no compliance-style process, release attestations, or documentation that restates code. Do not test documentation wording.
- No compatibility shims to the pre-`next` (`master`) implementation.

## Workflow

For non-trivial changes:

1. **Research** the relevant docs and code paths, including callers of any contract you change.
2. **Design** first. Write or update the design when the change affects architecture or contracts, and get it approved.
3. **Implement** one milestone at a time, simplest approach first.
4. **Verify** the changed behavior at its real boundary, then run `make quality`.
5. **Clean up**: remove obsolete code and docs in the same change.
6. **Report** the exact commands run and their results. Never claim unobserved success.

Do not ask the user for facts available from the repository or toolchain. Stop for product decisions.

## System invariants

- Single-node Go modular monolith with SQLite on local disk. Modules and drivers are compiled in and registered explicitly at the composition root.
- Dependencies flow `pkg/otogi` (contracts) ← `internal/kernel` ← `internal/adapter/*`. The kernel never imports adapters or modules. Platform SDK types (gotd, etc.) stay inside their adapter. Enforced by depguard in `make lint`.
- Every goroutine, queue, timer, and external resource has one owner and stops on shutdown.
- Queues are bounded. When full, the caller gets an explicit error or blocks under its context; nothing is dropped silently.
- `context.Context` flows through every blocking call.
- Module and driver panics are recovered at their boundary and reported; one bad module must not crash the process.
- Errors are wrapped with `%w` and handled once: returned, translated at a boundary, or turned into a visible failure.
- Never copy a live SQLite file; use SQLite's backup mechanisms.

## Go rules

- The `go` directive in `go.mod` selects the toolchain (plain `go` switches to it automatically). golangci-lint is pinned in `tools/go.mod`.
- `ctx context.Context` is the first parameter of blocking or long-lived operations; never stored in structs.
- Prefer synchronous code. Add concurrency only when it is needed and its lifecycle is obvious.
- Construct dependencies explicitly; no mutable package globals or service locators.
- Public contracts in `pkg/otogi` use domain types and have behavioral Godoc.
- Generated code is never edited by hand.

## Quality gates

`make quality` must pass before work is complete. Do not weaken a gate to make a change pass; fix the cause or change the gate deliberately. Details: `docs/quality.md`.

## Docs map

- `docs/product.md`: goals, scope, non-goals
- `docs/architecture.md`: layers, dependencies, composition, persistence ownership
- `docs/runtime.md`: events, delivery, queues, lifecycle, shutdown
- `docs/extensions.md`: module and driver SDK
- `docs/reliability.md`: failures, retries, recovery
- `docs/security.md`: credentials, management access, input limits
- `docs/observability.md`: structured event log and diagnostics
- `docs/management-plane.md`: operator API
- `docs/operations.md`: config, SQLite, backup, upgrade, shutdown
- `docs/quality.md`: tests and gates
- `docs/standards.md`: external references
- `docs/runbooks/`: concrete operator procedures

Long-term memory and control-panel UX need their own approved designs.
