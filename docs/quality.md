# Quality and Release

## Toolchain policy

The repository declares one Go language/toolchain version and uses a stable release that still receives upstream security fixes. Go supports the two most recent major releases; the project upgrades before its selected release leaves that window.

CI and local authoritative checks resolve identical tool versions from committed declarations and verify downloaded tools. Dependency resolution is reproducible; vendoring is optional, but an offline or proxied build path MUST be documented for releases.

Generated files record their generator and are reproducible from source. The gate fails when regeneration changes the tree.

## Experimental milestone gates

Until the implementation satisfies the full product boundary, commands introduced for the experimental vertical slice are milestone gates only, not the full release gate. They MUST NOT report deferred functionality as passing or be represented as satisfying the complete `quality` or `release-verify` contracts below. Every requirement in this document remains binding before a production-ready or conforming release claim.

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
