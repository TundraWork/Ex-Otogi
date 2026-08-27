# Final fix report

## Findings fixed

- A `Stop` that interrupts an in-flight `Drain` now marks that drain as interrupted. `Drain` returns `context.Canceled` instead of reporting a clean drain, while `Stop` remains idempotent and the existing first retained terminal-cause behavior is unchanged.
- The architecture checker now allows kernel, module, and adapter production packages to import only the repository contracts package (`pkg/otogi`). Any other repository-local `internal/*`, `modules/*`, or `pkg/*` import is rejected. Command composition remains unrestricted.

## Red evidence

- `./scripts/quality/go.sh test ./internal/kernel -run TestStopInterruptingDrainRetainsCancellation -count=1` failed with `Stop() = <nil>` before the lifecycle fix (the original test assertion expected the interrupted operation's cause; the final regression correctly scopes that requirement to `Drain`).
- `./scripts/quality/go.sh test ./scripts/quality -run TestArchitectureFixturesFail -count=1` failed because both `arch_local_internal` and `arch_local_pkg` unexpectedly passed before the checker fix.

## Green evidence

- Focused kernel lifecycle regressions passed:
  `./scripts/quality/go.sh test ./internal/kernel -run 'TestStopInterruptingDrainRetainsCancellation|TestStopCauseWinsOverOlderDrainCancellation|TestConcurrentStopJoinsWorkerAfterEarlierStopTimeout' -count=1`
- Kernel race suite passed:
  `./scripts/quality/go.sh test -race ./internal/kernel`
- Kernel leak-enabled package suite passed:
  `./scripts/quality/go.sh test ./internal/kernel`
- Architecture fixtures and repository check passed:
  `./scripts/quality/go.sh test ./scripts/quality -run TestArchitectureFixturesFail -count=1`
  `./scripts/quality/go.sh run ./scripts/quality/cmd/archcheck`
- Full stale-GOROOT gate passed:
  `GOROOT=/stale make quality`

## Self-review

- The kernel change adds only one mutex-protected lifecycle fact and changes only the interrupted drain's return value; it does not replace or overwrite `terminalErr`.
- The arch rule is expressed as an allowlist for the three restricted source families, so future repository-local package paths are denied without enumerating them. Existing sibling fixtures remain in the quality fixture table.
- Changed files are limited to the kernel lifecycle implementation/regression, arch checker/fixtures, and this report.
