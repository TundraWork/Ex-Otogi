# Task 3 Report

## Status

The lean kernel is implemented under `internal/kernel` with private validated configuration, bounded nonblocking admission, sorted serial routing, scoped and joined invocations, bounded defensive delivery and panic histories, and a single owned lifecycle worker. No adapter, scripted source, executable, or tooling scope is included.

## Commit

`refactor: implement lean event kernel`

## TDD

- Construction/admission tests were written first and failed because kernel declarations did not exist; focused tests passed after inert construction and bounded admission were implemented.
- Routing/invocation/history tests were written next and failed because snapshots and scoped invocation did not exist; focused tests passed after sorted isolation, message scoping, panic recovery, and bounded rings were implemented.
- Lifecycle/race/leak tests were written last and initially timed out because an empty drain could not wake the worker; focused tests passed after lifecycle wakeup, terminal-cause retention, and worker ownership were implemented.

## Verification

- `go test ./internal/kernel -count=1 -timeout=60s`: passed.
- `go test -race ./internal/kernel -count=1 -timeout=120s`: passed.
- `go test -race ./internal/kernel -count=10 -timeout=120s`: passed during stress verification.
- `go.uber.org/goleak` is integrated through `TestMain` and passed in the unit and race runs.
- `go doc -all ./internal/kernel`: confirmed package and exported API documentation.

## Review fixes

- Invocation close now prevents new sends, cancels, and joins in-flight sends.
- Duplicate effect slots return `ErrInvalidState`.
- Delivery history stores stable error identities rather than arbitrary module errors.
- Completed workers win over already-canceled observer contexts, preserving repeatable `Wait` results.

## Concerns

None.
