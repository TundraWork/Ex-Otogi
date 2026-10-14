# Quality

## Toolchain

`scripts/quality/go.sh` pins the Go toolchain (currently Go 1.27.0) and ignores an inherited `GOROOT`. The golangci-lint version is pinned in `scripts/quality/golangci-lint.sh`. Upgrade Go before the pinned release leaves upstream security support.

## Gates

| Command | What it does |
| --- | --- |
| `make format` | Apply `gofmt` |
| `make fmt-check` | Fail on unformatted files |
| `make lint` | golangci-lint plus the exported-Godoc check |
| `make arch-check` | Enforce the import rules in `architecture.md` |
| `make test` | All unit and integration tests |
| `make test-race` | All tests under the race detector |
| `make test-fuzz-smoke` | Short runs of the fuzz targets |
| `make test-corpus` | Cross-protocol rich event corpus |
| `make test-leak` | Goroutine leak checks for long-lived components |
| `make smoke` | Run the offline scripted bot and check its output |
| `make quality` | All of the above except `format` |
| `make telegram-smoke` | Opt-in live Telegram check; never part of `quality` |

`make quality` must pass before a change is complete. `make quality` never contacts Telegram.

## Testing approach

- Test behavior at real boundaries: kernel routing, driver mapping, SQLite stores with a real database, lifecycle start and stop.
- Use fakes with real state rather than call-sequence mocks.
- Use the race detector for code that owns goroutines or shared state, and `goleak` for long-lived components.
- Keep fuzz targets for parsers and decoders with real input space (entity conversion, durable codecs).
- Do not test impossible cases, private structure, or documentation wording. A test must catch a plausible regression.
- Wait on observable signals, never on sleeps.
- A flaky test is a bug: fix it, don't retry it.

## Lint

The pinned linter set runs with narrow, commented suppressions only. A linter that mostly produces busywork can be removed from the config deliberately; don't silence it file by file.

## Releases

Not defined yet. A release is a `go build` of `cmd/otogi` from a tagged commit, plus the docs. Add process only when it solves a real problem.
