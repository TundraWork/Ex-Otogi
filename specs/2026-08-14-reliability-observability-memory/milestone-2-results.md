# Milestone 2 Results: Latest-First Observability

## Contract

`GET /panel/events` now has three explicit modes:

- no cursor: newest matching page;
- `after_id`: the nearest matching events above the high-water cursor, for lossless polling;
- `before_id`: matching events below the low-water cursor, for history.

`after_id` and `before_id` are mutually exclusive. All responses present items newest-first and return unambiguous `OldestID` and `NewestID` cursors plus `HasOlder` and `HasNewer` continuation flags.

The Events view performs one initial latest-page request, retains independent high and low cursors, polls only newer records, and fetches older records only when the operator requests them. Poll requests have a single in-flight guard.

## Performance baseline

Command:

```sh
go test ./internal/kernel/management -run '^$' \
  -bench BenchmarkSQLiteListEvents100K -benchtime=100x -count=1
```

Reference environment: Apple M3 Pro, darwin/arm64, Go 1.26.1, SQLite database with 100,000 events.

| Query | p95 | Mean |
|---|---:|---:|
| latest 50 | 0.539 ms | 0.403 ms |
| newer 50 | 0.511 ms | 0.406 ms |
| older 50 | 0.507 ms | 0.401 ms |
| filtered latest 50 | 0.548 ms | 0.429 ms |

All shapes are well below the initial 100 ms p95 target. The SQLite integer primary-key access path is sufficient, so this milestone adds no speculative composite indexes.
