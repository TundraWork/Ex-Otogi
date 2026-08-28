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

## Review Fix Round 1

- Attached learned peers to the first emitted capture even when an index-zero pts-only placeholder precedes the routed update.
- Validated every channel cursor enumerated during startup against durable consistency/degraded state before gotd receives it; ahead cursors now durably degrade and fail loading.
- Required one caller-owned `Health` shared by manager callbacks/storage and downstream capture.
- Derived common/channel consistency progress for quarantined pts updates, including read-history updates.
- Completed source journals for empty, too-long, and difference-empty manager outcomes without opening the startup gate to live overtaking.
- Bounded the combined user/chat entity count before peer encoding allocation.
- Returned errors for common/channel cursors ahead of normalized markers instead of handing gotd unsafe state.
- Preserved channel-specific consistency boxes for channel deletion capture.
- Made archcheck source classification repository-root-relative even when invoked on a nested subtree, while retaining the narrow same-Telegram-adapter allowance.
