# Quality and Release

## Toolchain policy

The repository declares one Go language/toolchain version and uses a stable release that still receives upstream security fixes. Go supports the two most recent major releases; the project upgrades before its selected release leaves that window.

CI and local authoritative checks resolve identical tool versions from committed declarations and verify downloaded tools. Dependency resolution is reproducible; vendoring is optional, but an offline or proxied build path MUST be documented for releases.

Generated files record their generator and are reproducible from source. The gate fails when regeneration changes the tree.

## Experimental milestone gates

Until the implementation satisfies the full product boundary, commands introduced for the experimental vertical slice are milestone gates only, not the full release gate. They MUST NOT report deferred functionality as passing or be represented as satisfying the complete `quality` or `release-verify` contracts below. Every requirement in this document remains binding before a production-ready or conforming release claim.

The current milestone implements `make format`, `make fmt-check`, `make lint`, `make arch-check`, `make test`, `make test-race`, `make test-fuzz-smoke`, `make test-corpus`, `make test-leak`, `make telegram-smoke`, `make smoke`, and `make quality`. The experimental `arch-check` target implements the milestone architecture contract corresponding to the full-release `architecture` gate. The offline milestone `quality` target aggregates `fmt-check`, `lint`, `arch-check`, `test`, `test-race`, `test-fuzz-smoke`, `test-corpus`, `test-leak`, and `smoke`; it invokes neither mutating `format` nor live `telegram-smoke`. The full-release `verify-generated`, `security`, and `release-verify` commands remain deferred. Their operator contract and deferred scope are recorded in [`runbooks/core-runtime-smoke.md`](runbooks/core-runtime-smoke.md).

### Telegram quality gates

The Telegram milestone extends the default offline `make quality` gate with its mapper corpus, store and driver tests, race/leak coverage, generated-type import guard, pinned gotd/module-layer assertion, and bounded smoke runs of both `FuzzConvertEntitiesUTF16` and `FuzzDecodeEventInput`. Default quality MUST NOT read Telegram credentials, use a real session, resolve Telegram endpoints, or otherwise contact Telegram. `make telegram-smoke` is a separate, explicitly opted-in check of session connectivity only; it requires AppID, AppHash, and SessionPath and does not validate SQLite, account binding, recovery readiness, or full-driver readiness. It is excluded from `make quality` and is governed by the [Telegram MTProto runbook](runbooks/telegram-mtproto.md#opt-in-live-smoke).

Passing these gates proves only the named experimental milestone. It does not satisfy deferred security scanning, artifact verification, operational readiness, or the production release contract.

## Stable commands

The implementation repository provides discoverable, non-interactive commands with these contracts:

| Gate | Contract |
| --- | --- |
| `format` | Apply canonical Go and repository formatting |
| `verify-generated` | Regenerate and fail on drift |
| `lint` | Run compiler/vet/static analysis and documentation checks |
| `architecture` | Enforce import and ownership boundaries |
| `test` | Run deterministic unit and integration tests without races |
| `test-race` | Exercise concurrent packages with the Go race detector |
| `test-fuzz-smoke` | Run every registered fuzz target for a bounded CI duration |
| `test-leak` | Assert termination of long-lived components |
| `security` | Run secret, dependency, vulnerability, and artifact-policy checks |
| `quality` | Run all merge-blocking checks |
| `release-verify` | Build and verify release artifacts, SBOM, signatures, and provenance |

Names may be implemented through Make, a task runner, or scripts, but their behavior remains stable and documented. `quality` MUST fail when any required sub-gate fails.

## Test strategy

Tests defend observable contracts and plausible regressions.

- **Unit tests** cover pure domain policy and boundary translation. Table-driven form is used when cases share setup and assertion shape; it is not mandatory for unrelated behavior.
- **Contract tests** run every adapter or extension implementation against its capability semantics.
- **Integration tests** use real SQLite and protocol servers where mocks would hide transaction, encoding, timeout, or lifecycle behavior.
- **End-to-end smoke tests** start the built process, ingest a representative event, observe a side effect, query operator state, and shut down.
- **Race tests** cover packages that own concurrency or shared mutable state.
- **Leak tests** cover process components, subscriptions, workers, retries, and shutdown.
- **Fuzz tests** cover external decoders, identifiers, configuration, migrations, and parsers with meaningful input spaces.
- **Fault tests** implement the scenarios in `reliability.md`.
- **Benchmarks/load tests** establish the workload envelope and detect material resource regressions.

Tests use deterministic clocks, randomness, and bounded condition-based waits. Sleeping for an assumed scheduling interval is prohibited when a state signal can be observed. A test MUST be isolated, parallel-safe when marked parallel, and independent of execution order.

Mocks are generated only for stable interfaces where behavior inspection is necessary. Prefer fakes that implement meaningful state over strict call-sequence mocks. Tests MUST NOT assert private implementation structure unless that structure is itself a required boundary.

## Coverage

Coverage is a diagnostic map, not a target percentage. CI publishes package and changed-line coverage to reveal untested risk. Coverage alone never makes a gate pass, and generated code is excluded from interpretation. Security, recovery, overload, and cancellation contracts require direct tests regardless of aggregate coverage.

## Static analysis

The merge gate includes:

- `go test` compilation across supported build targets;
- `go vet` and a pinned maintained linter set;
- forbidden dependency/import checks;
- unchecked-error and lost-cancellation checks;
- formatting and generated drift;
- public Godoc checks for stable exported contracts;
- policy checks preventing production placeholders, debug output, accidental secrets, and unbounded concurrency primitives.

A linter suppression is narrow, local, and explains why the reported condition is safe. Global disabling to accommodate one case is prohibited.

## Development and CI cadence

During implementation, run the smallest gate that can falsify the current change. Before a milestone is complete, run its affected package and behavioral scenario. Before merge, CI runs `quality`, end-to-end smoke, and applicable workload/fault suites from a clean checkout.

Required CI checks cannot be bypassed by changing only workflow files. Changes to gates or tool versions receive the same review as production code and demonstrate the intended failure mode.

Flaky tests are defects. A failing test may be quarantined only with an owner, issue, expiry date, preserved signal, and explicit release-risk decision; repeated retries that hide the first failure are prohibited.

### Gotd upgrades

The repository currently pins `github.com/gotd/td v0.161.0` and asserts generated `tg.Layer == 228` in `internal/adapter/telegram/store/version_test.go`. An upgrade changes both expectations deliberately and MUST include reviewed mapper/codec fixture updates and compatibility evidence for authorization, update recovery, account-bound SQLite state, send/refetch operations, and paired backup restore. Generated gotd sources remain dependency-owned under `internal/adapter/telegram` use and MUST NOT be copied into public contracts or edited by hand.

The exact dependency-update, scoped test, default-quality, and live-smoke commands are maintained in the [gotd upgrade runbook](runbooks/telegram-mtproto.md#gotd-upgrade). The dependency diff, old/new layer, fixture provenance, migration/restore result, and live-smoke result are review evidence; none of them is a production release claim.

## Release artifacts

Release builds run on a hosted, isolated CI platform from a reviewed immutable revision. The release record includes:

- binaries for declared targets;
- source revision and build command;
- module and tool dependency lock information;
- CycloneDX or equivalent SBOM;
- cryptographic checksums;
- signatures verifiable without repository write access;
- SLSA provenance, targeting Build L2 or documenting the gap;
- vulnerability scan and accepted-risk record;
- configuration and persistence compatibility statement;
- smoke and restore evidence.

Artifacts are reproducible where the Go toolchain and target allow it. A comparison mismatch is investigated and documented before release.

## Merge and completion rule

A change is complete only when source, tests, generated artifacts, specifications, operator material, and removal of obsolete paths agree. Verification reports exact commands and observed results. Passing a narrower test does not justify claiming the full gate passed.
