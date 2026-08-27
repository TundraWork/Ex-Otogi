# Task 2 Report

## Status

Lean public contracts are implemented under `pkg/otogi` with private immutable values, defensive descriptor copies, invariant-preserving results, scoped messaging, stable errors, and concurrency-safe event IDs. No EventSink, extension graph, diagnostics/evidence, outcome hierarchy, persistence, or tooling API is present.

## Commit

`refactor: define lean runtime contracts`

## Verification

- Tests were written first and observed failing because the package contracts did not exist.
- `go test ./pkg/otogi`: passed.
- `go test -race ./pkg/otogi`: passed.
- `go vet ./pkg/otogi`: passed.
- `go doc -all ./pkg/otogi`: confirmed package and exported API documentation.
- A forbidden-contract search under `pkg/otogi` returned no matches.
- Review fix red: adding a temporary exported `EventSink` made `TestPublicAPIHasOnlyLeanContracts` fail with the unexpected export.
- Review fix red: adding a temporary method to `Event` made `TestPublicInterfacesHaveExactMethodShapes/Event` fail with `has 15 methods, want 14`.
- Review fix green: focused API allowlist/interface-shape tests, full package unit tests, and uncached race tests passed after removing both mutations.

## Module normalization

Go 1.27 `go mod tidy` removes the redundant `toolchain go1.27.0` declaration when `go 1.27.0` already selects the exact toolchain. Task 2 retains canonical `go 1.27.0` in `go.mod` and `.go-version` remains `1.27.0`; this normalization is included because otherwise package tests report that `go.mod` needs updates.

## Concerns

None.
