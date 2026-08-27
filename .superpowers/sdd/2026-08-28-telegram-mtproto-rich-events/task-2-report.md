# Task 2 Report

Status: complete

## Implemented

- Routes valid rich events by exact `EventAction`; valid actions without subscribers complete with no deliveries.
- Preserves the complete rich `EventInput` projection presented to modules.
- Adds narrow `kernel.TrustedAdmitter` deterministic-ID admission using only `pkg/otogi` values in its method signatures, while ordinary `Submit` retains kernel-generated IDs.
- Completion subscriptions atomically reserve their `EventID`; ordinary admission cannot consume a reservation, trusted admission consumes it, and active/reserved duplicates are rejected.
- Adds bounded, cancelable, one-shot volatile completion subscriptions keyed by `EventID`.
- Emits completion only after every interested module finishes successfully enough to acknowledge; canceled results, routing cancellation, and shutdown close subscriptions without a value so durable callers replay.
- Preserves at-least-once replay and stable idempotency derivation from the trusted runtime ID.

## Verification

- `./scripts/quality/go.sh test -race ./internal/kernel`
- `make quality`

Both commands passed on 2026-08-28. `make quality` covered formatting, lint/doccheck, architecture checks, all unit tests, repository race tests, leak tests, and smoke execution.

## Review

A focused review found cross-path ID collisions, reservation ownership, package-boundary, and cancellation acknowledgement defects. Regression tests reproduced each issue; the common admission path now rejects active collisions, subscription reservations are atomic, the channel carries `otogi.EventID`, and canceled routing cannot acknowledge. Kernel race and full quality gates passed afterward.

## Concerns

Completion acknowledgement is intentionally volatile and bounded. It provides no durable tombstone or exactly-once guarantee; callers must subscribe before admission and replay the same stable ID after acknowledgement loss.
