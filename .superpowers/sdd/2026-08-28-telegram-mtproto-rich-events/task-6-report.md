# Task 6 Report

## Status

Implemented persisted, bounded Telegram degraded-state recovery without lifecycle, dispatcher, authentication, or sending scope.

## Delivered

- Durable recovery state machine for `degraded`, `resynchronizing`, `ready`, and `permanent_degraded`, retaining operator-visible causes and explicit permanent acknowledgements across restart.
- Transactional schema upgrades from Task 5 schema versions, plus account-erasure coverage for recovery records.
- Single-owner resynchronizer with mandatory difference/history/page/reprocessing bounds, per-call deadlines, cancellation checks, monotonic pagination checks, and no retries.
- Common `updates.getState` / bounded `updates.getDifference` recovery, including bounded known-dialog history planning for too-long differences.
- Per-channel bounded `updates.getChannelDifference` and `messages.getHistory`, durable channel cursor updates, and explicit permanent classification for inaccessible channels.
- Readiness aggregation that remains degraded until every required box is ready or a permanent-degraded box is explicitly acknowledged.
- Bounded raw journal and quarantine reprocessing queues. Rows are completed only after successful injected durable normalization; existing inbox/quarantine source-key uniqueness preserves dedupe.
- Public gotd v0.161 APIs only; generated `tg` values remain inside the Telegram adapter.

## Focused Verification

- `./scripts/quality/go.sh test ./internal/adapter/telegram/... -count=1`
- `./scripts/quality/go.sh test -race ./internal/adapter/telegram ./internal/adapter/telegram/store ./internal/adapter/telegram/updates -count=1`

Both completed successfully, including the package-level goleak harness in `updates`.

## Residual Task 7+ Concerns

- Driver lifecycle must wire the resynchronizer as the sole startup/reconnect recovery owner and publish readiness from persisted recovery aggregation.
- Dispatcher admission, claims, completion acknowledgements, and delivery lifecycle remain Task 7+.
- Authentication and outbound send/correlation remain later tasks.
- Concrete production replay callbacks must be assembled with the driver-owned downstream mapper and journal decoder when lifecycle composition is implemented; Task 6 provides the bounded durable orchestration seams.
