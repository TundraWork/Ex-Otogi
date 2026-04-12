# Proposal: Replace In-Memory Management Store with SQLite via sqlc

## Background

The management observability store (`internal/kernel/management/store.go`) currently uses a pure in-memory data structure with `sync.RWMutex` for all event, artifact, and snapshot storage. This approach has inherent limitations:

- **Data loss on restart**: All management data is lost when the process restarts.
- **Unbounded memory growth under load**: Despite the sliding-window retention, high-throughput scenarios can still cause memory pressure.
- **No query flexibility**: Filtering, pagination, and aggregation are implemented as Go loops (e.g., `matchesEventQuery`, linear scan in `GetEvent`), which limits both performance and expressiveness.

## Goal

Replace the in-memory management store with SQLite as the persistence backend, using sqlc to generate type-safe Go code from SQL queries. Move all filtering, pagination, and aggregation logic from Go code into SQL queries managed by sqlc.

## Scope

1. Introduce `modernc.org/sqlite` (pure-Go SQLite driver) and `github.com/sqlc-dev/sqlc` as project dependencies.
2. Replace `internal/kernel/management/store.go` (the `Service` struct and its methods) with a SQLite-backed implementation that implements the same `panel.Recorder` and `panel.Query` interfaces.
3. Define SQL schema and sqlc query files for events, artifacts, and snapshots.
4. Use sqlc-generated code for all CRUD operations, replacing the current Go-loop filtering in `ListEvents`, `GetTrace`, `ListSnapshots`, `GetOverview`, etc.
5. Retain the existing sliding-window retention semantics via SQL-based eviction (e.g., `DELETE FROM events WHERE id < (SELECT MAX(id) - :max_events + 1 FROM events)`).
6. Update `cmd/bot/app.go` to configure the SQLite database path and initialize the new store.
7. Ensure all existing tests pass with the new store implementation.

## Non-Goals

- Replacing the `eventcache` module's in-memory article cache (that is a separate cache with different semantics).
- Changing the `panel.Recorder` or `panel.Query` interfaces in `pkg/otogi/management/`.
- Modifying the HTTP API routes or frontend.
- Adding WAL mode or other advanced SQLite tuning (can be done in follow-up).
