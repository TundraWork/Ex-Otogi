# Task 5 Report

## Status

Implemented and review-hardened durable Telegram update journaling, startup gating, gotd manager integration, normalized capture, deletion routing, and durable degraded-state monitoring.

## Delivered

- Shared `JournalGate` for socket `UpdateHandler` and RPC `hook.UpdateHook`, with journal-before-manager ordering and a strict 64-row startup bound.
- Asynchronous startup replay after gotd initializes manager state, preventing the ten-entry internal queue from deadlocking 64-row recovery while keeping the gate locked against live overtaking; replay errors cancel and return from `RunManager`.
- Explicit inner-update correlation across gotd's queue, including transformed short-message identity and split/combined deliveries; source journals remain pending until their final derived delivery commits.
- gotd `updates.Manager` construction using durable state/hash stores, `hook.AffectedHook(manager)`, and complete unrecoverable callbacks.
- Monitored state storage that durably degrades readiness on setter failures and when loaded common/channel cursors exceed normalized consistency markers.
- Transactional downstream capture of peers, locators, inbox/quarantine rows, consistency markers, routed channel deletes, locator-resolved common deletes, and structured unresolved deletion tombstones.
- Pre-transaction capture bounds (`MaxCaptureRows == 1024`) with oversized outputs degraded for Task 6 resynchronization rather than allocated into unbounded transactions.
- Difference-output journaling, negative manager sequence exclusion, raw journal retention on failures, and same-adapter architecture composition.

## Focused Verification

- `./scripts/quality/go.sh test ./internal/adapter/telegram/updates ./internal/adapter/telegram/store ./internal/adapter/telegram/mapper -count=1`
- `./scripts/quality/go.sh test -race ./internal/adapter/telegram/updates ./internal/adapter/telegram/store -count=1`

Both completed successfully. Project-wide format, lint, architecture, and quality suites are intentionally left to the integration owner.

## Task 6 Boundary

Task 5 persists readiness-blocking degraded/resync-required state; it does not execute full resynchronization. Task 6 remains responsible for common/channel `getState`/difference/history recovery, reconciliation, and clearing or permanently classifying degraded state.

## Consistency Note

The implementation does not claim atomicity between gotd cursor writes and inbox capture because gotd v0.161.0 exposes these as separate operations and suppresses some setter errors after logging. Durable journal retention, source correlation/completion, stable source deduplication, consistency markers, and monitored storage failures provide the recovery boundary.
