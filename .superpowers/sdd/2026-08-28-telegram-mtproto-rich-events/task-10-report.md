# Task 10 Report

## Delivered

- Added an opt-in `cmd/telegram-smoke` command and `make telegram-smoke` target. Configuration is environment-only, is fully validated before the live function is called, and error/output tests prove the app hash is not echoed. The command requires an already-authorized private gotd session and performs authorization-status and self-account RPC checks without interactive credential callbacks.
- Added `NewComposition(ProductionCompositionConfig)`, which owns the real account-bound store, mapper/downstream, health, update manager/journal gate, durable replay/resynchronizer, inbox dispatcher, sender, and refetcher. Pre-client forwarding hooks satisfy gotd option ordering and bind to the concrete graph immediately after authorization and store opening.
- Added one adapter-owned versioned deterministic durable `EventInput` codec used by both update capture and inbox dispatch. It round-trips the full current rich-message graph and rejects unknown versions, trailing/malformed/oversized input, excessive collections/depth, and cancellation; its fuzz target checks arbitrary input without panics.
- Narrowed the production client session to the seven RPC methods used by manager/recovery/send/refetch. Generated gotd types remain inside the Telegram adapter.
- Kept the existing offline `cmd/otogi` smoke unchanged and excluded `telegram-smoke` from default `make quality`.
- Added default bounded Telegram UTF-16 mapper fuzz smoke, cross-protocol corpus, Telegram driver/update leak coverage, and existing all-package race coverage.
- Added architecture enforcement confining every gotd import, including test imports, to `internal/adapter/telegram`. Existing compiled guards pin gotd `v0.161.0`, SQLite `v1.57.0`, and `tg.Layer == 228`.
- Added operator and architecture documentation for private local session/SQLite separation, permissions, account binding, authorization, paired backup/restore, retention/erasure, degraded recovery, live testing, and gotd/layer upgrades. The quality documentation explicitly describes these as experimental milestone gates, not a production release gate.

## TDD evidence

The command tests first failed to compile because `run` and `liveConfig` did not exist. The codec and production-composition tests were likewise written against missing APIs before implementation. The final fake-backed composition smoke uses real SQLite, manager/gate, resynchronizer, dispatcher, and codec; it observes manager state initialization before recovery difference and dispatcher submission, publishes sender/refetcher only after binding, and verifies child cancellation before store close. A full-gate run then exposed a dispatcher-result shutdown deadlock in an existing failure test; removing the duplicate receive fixed the root cause and the focused regression passed ten consecutive runs.

## Verification

- `./scripts/quality/go.sh test ./cmd/telegram-smoke ./internal/adapter/telegram` — PASS.
- `./scripts/quality/go.sh test ./internal/adapter/telegram -run '^TestDriverImmediateDispatcherFailureFailsStart$' -count=10 -timeout=30s` — PASS.
- `./scripts/quality/go.sh test -race ./internal/adapter/telegram -run '^TestCompositionStartsRealGraphAndStopsInReverseOrder$' -count=1 -timeout=60s` — PASS.
- `make telegram-smoke` with no Telegram environment — expected refusal before network: `required environment variable OTOGI_TELEGRAM_APP_ID is missing`.
- `make quality` through `scripts/quality/go.sh`, under the inherited stale `GOROOT` — PASS. This included format check, golangci-lint (0 issues), doccheck, archcheck, all unit/integration tests, all-package race, deterministic one-iteration Telegram fuzz smoke, cross-protocol corpus, Telegram/kernel leak suites, and the unchanged offline scripted smoke.

## Live-test status

No real Telegram credentials or session were available, so the opt-in network smoke was not run. Its pre-network refusal and secret-safety paths were exercised locally. A passing live smoke is intentionally not claimed as production readiness.

## Fix round 1

Runtime, quality, and security review findings were corrected with focused regressions:

- pre-bind gotd middleware proxies now pass startup RPCs through and forward after graph binding;
- the authorized user ID flows from `Driver.runConnected` into manager composition without an uncancelable duplicate `Self` RPC;
- dispatcher readiness begins only after Run owns admission, while drain closes new claims and waits for active delivery and durable cleanup without canceling it;
- every non-context durable event-codec rejection wraps `otogi.ErrInvalid`, so poison inbox rows fail permanently and later rows progress; cancellation identity remains transient;
- default fuzz smoke runs both entity conversion and durable EventInput decoding;
- live smoke now verifies only preauthorized session connectivity and no longer claims SQLite, account-binding, recovery, or driver readiness coverage;
- Linux session/SQLite paths require real current-user-owned `0700` parents, single-link `0600` files and safe sidecars; unsupported platforms fail closed;
- restore instructions require a vetted no-follow restore tool, externally authenticated encrypted artifacts, strict flat-member allowlisting, integrity/binding verification, and atomic publication.

Focused verification passed with the repository wrapper: Telegram/store unit tests, selected runtime race tests, both bounded fuzz targets, command/quality contract tests, doccheck, and archcheck. No full `make quality` was run in this review round, per controller instruction.

## Fix round 2

Corrected the degraded-gap runbook: full `Driver`/`NewComposition` startup checks SQLite `RecoveryReady` and refuses readiness for unacknowledged degraded records, while `telegram-smoke` verifies preauthorized session connectivity only and cannot assess SQLite, account binding, recovery state, or full-driver readiness.

## Authoritative quality correction

The controller's authoritative gate exposed one `errorlint` finding in codec classification. A red regression proved the classified error preserved `otogi.ErrInvalid` but not its concrete cause. `classifyCodecError` now uses dual `%w` wrapping, preserving both identities while context cancellation/deadline errors remain unchanged. The focused codec/dispatcher tests and full `make quality` then passed, including lint, documentation and architecture checks, all unit and race tests, both bounded fuzz targets, corpus, leak suites, and the unchanged offline smoke.

## Final degradation propagation fix

Traced the post-open failure path from `JournalGate.Handle` through gotd manager delivery to `Downstream.fail`: durable `RecordGap` and pending journal state were preserved, but gotd treated the returned handler error as nonfatal and the concrete manager runner had no lifecycle signal. A red-green fix added one bounded, nonblocking, first-failure notification owned by shared updates `Health`. The notification is armed only after successful gate replay, so persisted startup degradation still blocks readiness without racing a runtime failure. The concrete manager runner now returns the structured degradation error and cancels its worker; Driver child supervision then transitions ready to failed and cancels manager, dispatcher, and client siblings. `JournalGate` latches the failed boundary before accepting another packet, and `Downstream` rejects packets already queued inside gotd behind that boundary, so later traffic cannot append, normalize, or overtake it. Normal cancellation remains classified as clean stop.

Regression coverage proves the first signal is one-shot under 100 repeated degradations, post-ready manager failure retains both the capture cause and durable-recording error identity, gate ingress rejects later packets without appending them, queued downstream work cannot capture past degraded health, and a real SQLite composition reaches ready then fails on an oversized mapper deletion batch. The integration assertion observes Driver failed/not-ready, sibling cancellation, one pending journal row, durable degraded consistency, and no later packet advancement.

Final verification:

- Focused updates and Telegram unit suites — PASS.
- Focused lifecycle/degradation regressions under `-race -count=10` — PASS.
- Focused lifecycle/degradation regressions under package `goleak` harnesses with `-count=50` — PASS.
- `make quality` — PASS, including lint, documentation and architecture checks, all unit and race tests, bounded fuzz targets, corpus, leak suites, and offline smoke.
