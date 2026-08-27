# Task 6 Report

Completed final verification and operator documentation for the experimental lean core runtime.

## Delivered

- Added `docs/runbooks/core-runtime-smoke.md` with exact build, run, formatting, gate, smoke, and stale-`GOROOT` commands.
- Recorded the real executable output, `echo: hello`, and explained what the composition smoke exercises.
- Updated governing documentation with the exact implemented milestone targets and an explicit distinction from deferred release gates.
- Explicitly documented the experimental, non-conforming status and deferred persistence, platform, operations, security, telemetry, and release areas. No placeholder or fake-success target was added.

## Verification

- `make format && make fmt-check` — passed; formatting produced no tracked source changes and the formatting check was clean.
- `make lint` — passed with `0 issues`; the exported/package documentation checker passed.
- `make arch-check` — passed.
- `make test` — passed for every package.
- `make test-race` — passed for every package under the race detector.
- `make test-leak` — passed for `internal/kernel` and `internal/adapter/scripted`.
- `make smoke` — passed its exact-output assertion.
- `./scripts/quality/go.sh run ./cmd/otogi` — passed and printed exactly `echo: hello`.
- `./scripts/quality/go.sh build -o /tmp/ex-otogi ./cmd/otogi && /tmp/ex-otogi` — passed the documented build/run procedure and printed exactly `echo: hello`.
- `./scripts/quality/go.sh run ./scripts/quality/cmd/doccheck` — passed, inspecting package comments, exported declarations, interface methods, and exported fields.
- `./scripts/quality/go.sh list ./...` — enumerated all nine repository packages covered by the checks.
- `GOROOT=/definitely/stale make quality` — passed the `fmt-check` formatting check, lint/documentation, architecture, unit, race, leak, and smoke gates while proving the launchers ignore a stale external `GOROOT`.
- `git diff --check` — passed.

## Deferred and non-conforming scope

This remains an experimental event-bridge milestone, not a production-ready or conforming Ex-Otogi release. SQLite and durability, real platform drivers, long-lived signal handling, authentication and management, telemetry, security/release scanning, backup/restore, fuzz/load/fault suites, generated-file verification, SBOMs, signatures, provenance, and release verification remain deferred. The milestone `quality` target must not be interpreted as the full release gate specified in `docs/quality.md`.

## Concerns

The committed Go launchers may download Go 1.27.0, and the lint launcher may download pinned `golangci-lint` v2.13.1, on first use. These downloads require network access unless the relevant caches are already populated.

### Review fix round 1

- Corrected the governing milestone description to enumerate the actual `quality` dependencies without self-reference.
- Recorded that `arch-check` implements the experimental milestone architecture contract and limited the absent full-release command list to `verify-generated`, `test-fuzz-smoke`, `security`, and `release-verify`.
- Documented the smoke target's actual POSIX command-substitution semantics: trailing newlines are removed before the remaining standard output is compared, and a nonzero executable exit fails the target.
- Corrected stale-`GOROOT` evidence to identify `fmt-check` as the formatting gate exercised by `quality`.
- `git diff --check && make quality` — passed after the documentation corrections; lint reported `0 issues`, documentation and architecture checks passed, and unit, race, leak, and smoke gates passed.
