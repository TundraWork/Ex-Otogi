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
- Follow-up red: adding a temporary exported `EventInputParams.Diagnostics` field made `TestPublicAPIHasOnlyLeanContracts` fail with that unexpected qualified field.
- Follow-up green: the allowlist now includes every intentional exported struct field as `Type.Field`; focused, full unit, and uncached race tests passed after removing the mutation.
- Second follow-up red: temporarily embedding the already-allowed `Result` type in `EventInputParams` made `TestPublicAPIHasOnlyLeanContracts` fail with unexpected `EventInputParams.Result`.
- Second follow-up green: anonymous embedded names are derived through identifier, selector, pointer, generic index, generic index-list, and parenthesized AST forms and still require an explicit qualified allowlist entry.

## Module normalization

Go 1.27 `go mod tidy` removes the redundant `toolchain go1.27.0` declaration when `go 1.27.0` already selects the exact toolchain. Task 2 retains canonical `go 1.27.0` in `go.mod` and `.go-version` remains `1.27.0`; this normalization is included because otherwise package tests report that `go.mod` needs updates.

## Concerns

None.
