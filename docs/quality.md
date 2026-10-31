# Quality

## Toolchain

The `go` directive in `go.mod` pins the Go toolchain (currently 1.27.0); an older local Go switches to it automatically. golangci-lint is pinned in `tools/go.mod` and runs through `go tool -modfile=tools/go.mod golangci-lint`, so its dependencies stay out of the main module. Upgrade Go before the pinned release leaves upstream security support.

## Gates

| Command | What it does |
| --- | --- |
| `make format` | Apply formatting (`golangci-lint fmt`, gofmt) |
| `make lint` | golangci-lint: formatting, static analysis, Godoc (`revive`), and import boundaries (`depguard`) |
| `make test` | All tests, including goroutine leak checks (`goleak`) and the cross-protocol corpus |
| `make test-race` | All tests under the race detector |
| `make test-fuzz-smoke` | Short runs of the fuzz targets |
| `make smoke` | Run the offline smoke binary and check its output |
| `make quality` | `lint`, `test-race`, `test-fuzz-smoke`, `smoke` |
| `make telegram-smoke` | Opt-in live Telegram check; never part of `quality` |

`make quality` must pass before a change is complete. It never contacts Telegram.

## Testing approach

- Test behavior at real boundaries: kernel routing, driver mapping, SQLite stores with a real database, lifecycle start and stop.
- Use fakes with real state rather than call-sequence mocks.
- Use the race detector for code that owns goroutines or shared state, and `goleak` for long-lived components.
- Keep fuzz targets for parsers and decoders with real input space, such as Telegram entity conversion and Markdown formatting.
- Do not test impossible cases, private structure, or documentation wording. A test must catch a plausible regression.
- Wait on observable signals, never on sleeps.
- A flaky test is a bug: fix it, don't retry it.

## Lint

The pinned linter set runs with narrow, commented suppressions only. A linter that mostly produces busywork can be removed from the config deliberately; don't silence it file by file.

## Releases

Not defined yet. A release is a `go build` of `cmd/otogi` from a tagged commit, plus the docs. Add process only when it solves a real problem.
