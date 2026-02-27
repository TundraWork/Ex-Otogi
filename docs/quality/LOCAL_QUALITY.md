# Local Quality Gate

`make quality` is the canonical local quality command for Ex-Otogi.

## Quick Start

1. Install pinned local tools:
   ```sh
   make tools
   ```
2. Install git hook:
   ```sh
   make hooks-install
   ```
3. Run the full gate:
   ```sh
   make quality
   ```

## Gate Composition

- Hard-fail (`make quality-core`):
  - `make doctor`
  - `make fmt-check`
  - `make lint`
  - `make arch-check`
  - `make agent-guard`
  - `make test-race`
  - `make test-leak`
- Report-only:
  - `make coverage-report` (writes artifacts under `.cache/coverage`)
  - `make security-warn` (warnings only, no block)

## Triage Flow

1. Run `make quality`.
2. Fix hard-fail items in order: `doctor` -> formatting/lint -> architecture -> tests.
3. Review report-only output and create follow-up tasks when needed.

## LLM Change Contract

| Rule | Enforced by |
| --- | --- |
| Deterministic toolchain and tool versions | `make doctor` |
| Architecture boundaries (`pkg/otogi`, `internal/kernel`, `modules/*`, `pkg/llm`) | `make arch-check`, `depguard` |
| No added `panic(...)` in production code | `make agent-guard`, `forbidigo` |
| No added `TODO` / `FIXME` in production code | `make agent-guard` |
| Production changes must include test updates (unless excepted) | `make agent-guard`, `config/quality/test_required_exceptions.txt` |
| Race-safe behavior | `make test-race` |
| Leak baseline for concurrency-heavy packages | `make test-leak` |
| Coverage visibility | `make coverage-report` |
| Security visibility | `make security-warn` |

## Hook Behavior

- Pre-commit runs one authoritative hook: `make quality`.
- The hook uses staged diff mode (`QUALITY_DIFF_MODE=staged`) for changed-code policy checks.
