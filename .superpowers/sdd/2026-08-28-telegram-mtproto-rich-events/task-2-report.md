# Task 2 Report

Status: complete

## Implemented

- Routes valid rich events by exact `EventAction`; valid actions without subscribers complete with no deliveries.
- Preserves the complete rich `EventInput` projection presented to modules.
- Adds narrow `kernel.TrustedAdmitter` deterministic-ID admission while ordinary `Submit` retains kernel-generated IDs.
- Rejects duplicate active IDs across trusted and ordinary admission paths and permits replay after completion.
- Adds bounded, cancelable, one-shot volatile completion subscriptions keyed by `EventID`.
- Emits completion only after every interested module finishes; cancellation and shutdown close subscriptions without blocking routing.
- Preserves at-least-once replay and stable idempotency derivation from the trusted runtime ID.

## Verification

- `./scripts/quality/go.sh test -race ./internal/kernel`
- `make quality`

Both commands passed on 2026-08-28. `make quality` covered formatting, lint/doccheck, architecture checks, all unit tests, repository race tests, leak tests, and smoke execution.

## Review

A focused reviewer found one important cross-path active-ID collision. A regression test reproduced it, the common admission path now rejects every active ID collision, and the kernel race and full quality gates passed afterward.

## Concerns

Completion acknowledgement is intentionally volatile and bounded. It provides no durable tombstone or exactly-once guarantee; callers must subscribe before admission and replay the same stable ID after acknowledgement loss.
