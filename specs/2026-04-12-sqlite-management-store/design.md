# Design: SQLite Management Store via sqlc

## Architecture

Replace `internal/kernel/management/store.go` (in-memory `Service`) with a SQLite-backed implementation using sqlc-generated query code. The new store implements the same `panel.Recorder` and `panel.Query` interfaces without behavioral changes to callers.

### Component Layout

```
internal/
  sqlc/management/          # sqlc-generated code (Querier interface, struct types, SQL impl)
  kernel/management/
    store.go                 # SQLiteStore struct implementing Recorder + Query
    store_test.go            # Tests migrated from in-memory store tests
    migrations/              # Embedded goose SQL migration files
    doc.go
pkg/otogi/management/        # Unchanged: interfaces, types, payloads
```

### SQLite Driver

`modernc.org/sqlite` — pure-Go, no CGo. Enables simple cross-compilation and single-binary deployment.

### Schema Management

`github.com/pressly/goose/v3` with embedded migration files. On startup, the store opens (or creates) the SQLite file and runs pending migrations. The first migration creates all tables.

### Dynamic Filtering with sqlc

`ListEvents` and `ListSnapshots` require optional filter parameters. sqlc does not support dynamic query building natively. The approach is to use the "empty-string-means-no-filter" SQL pattern:

```sql
WHERE (:category = '' OR category = :category)
  AND (:kind = '' OR kind = :kind)
  AND (:trace_id = '' OR trace_id = :trace_id)
  AND (:conversation_id = '' OR conversation_id = :conversation_id)
  AND (:module = '' OR module = :module)
  AND (:level = '' OR level = :level)
  AND id > :after_id
ORDER BY id ASC
LIMIT :limit;
```

This maps directly to the existing `EventQuery` struct where empty string fields mean "no filter". SQLite's query planner handles this pattern efficiently with the defined indexes.

### sqlc Configuration

`sqlc.yaml` at project root configures:
- SQL source: `internal/kernel/management/queries/`
- Generated output: `internal/sqlc/management/`
- Engine: sqlite

sqlc generates a `Querier` interface and a concrete `Queries` struct with type-safe methods for every query.

### Database Schema

Three tables mirroring the current in-memory data:

**events**
```sql
CREATE TABLE events (
  id              INTEGER PRIMARY KEY AUTOINCREMENT,
  occurred_at     DATETIME NOT NULL,
  trace_id        TEXT NOT NULL DEFAULT '',
  parent_event_id INTEGER,
  category        TEXT NOT NULL,
  kind            TEXT NOT NULL,
  level           TEXT NOT NULL,
  module          TEXT NOT NULL DEFAULT '',
  component       TEXT NOT NULL DEFAULT '',
  tenant_id       TEXT NOT NULL DEFAULT '',
  platform        TEXT NOT NULL DEFAULT '',
  conversation_id TEXT NOT NULL DEFAULT '',
  actor_id        TEXT NOT NULL DEFAULT '',
  summary         TEXT NOT NULL DEFAULT '',
  payload_type    TEXT NOT NULL DEFAULT '',
  payload_json    TEXT NOT NULL DEFAULT '{}'
);
```

**artifacts**
```sql
CREATE TABLE artifacts (
  id         TEXT PRIMARY KEY,
  event_id   INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
  kind       TEXT NOT NULL,
  created_at DATETIME NOT NULL,
  content    TEXT NOT NULL DEFAULT ''
);
```

**snapshots**
```sql
CREATE TABLE snapshots (
  namespace    TEXT NOT NULL,
  key          TEXT NOT NULL,
  module       TEXT NOT NULL DEFAULT '',
  updated_at   DATETIME NOT NULL,
  summary      TEXT NOT NULL DEFAULT '',
  payload_type TEXT NOT NULL DEFAULT '',
  payload_json TEXT NOT NULL DEFAULT '{}',
  PRIMARY KEY (namespace, key)
);
```

### Indexes

```sql
CREATE INDEX idx_events_occurred_at ON events(occurred_at);
CREATE INDEX idx_events_kind ON events(kind);
CREATE INDEX idx_events_trace_id ON events(trace_id);
CREATE INDEX idx_events_category ON events(category);
CREATE INDEX idx_events_conversation_id ON events(conversation_id);
CREATE INDEX idx_events_module ON events(module);
CREATE INDEX idx_artifacts_event_id ON artifacts(event_id);
CREATE INDEX idx_snapshots_namespace ON snapshots(namespace);
CREATE INDEX idx_snapshots_module ON snapshots(module);
```

These indexes cover all query patterns in `EventQuery`, `TraceQuery`, and `SnapshotQuery`.

### Payload Storage

The current `panel.Event.Payload` and `panel.Snapshot.Payload` are `any`. The SQLite store serializes them as JSON text in `payload_json` with the concrete type name stored in `payload_type`. Deserialization uses `payload_type` to reconstruct the correct Go struct. Unknown payload types fall back to `json.RawMessage`.

`panel.ArtifactIDs` on `Event` is reconstructed at read time by joining against the `artifacts` table rather than stored as a column, keeping the schema normalized.

### Retention

Retention limits are removed. The SQLite database retains all data. The config fields `max_events`, `max_artifacts`, `max_artifact_bytes`, and `max_snapshots` become no-ops (accepted but ignored for backward compatibility). This eliminates the sliding-window eviction logic and simplifies `EventPage.WindowStartID`/`WindowEndID` to always reflect the full database range. `CursorResetRequired` is always `false`.

### Interface Compatibility

The new `SQLiteStore` implements `panel.Recorder` and `panel.Query` without interface changes. The HTTP routes and frontend require no modifications. `panel.Overview` fields retain their semantics:
- `WindowStartID` / `WindowEndID`: min/max event IDs in the database.
- `TotalEvents`: `SELECT COUNT(*) FROM events`.
- `EventCountsByCategory`, `RecentErrorCount`, `RecentTraceCount`: computed via aggregate SQL queries.
- `ActiveInflightByComponent`: computed the same way as before from event data.

### Concurrency

`database/sql` connection pool handles concurrent access. SQLite in WAL mode (enabled via PRAGMA on open) allows concurrent reads with a single writer. The store holds a `*sql.DB` and delegates all serialization to SQLite.

### Initialization and Shutdown

- `NewSQLiteStore(ctx, dbPath)` opens the database, runs migrations, and returns the store.
- The `OnShutdown` equivalent closes the `*sql.DB`.
- The database file path is configurable via `management.database_path` in the config file. Default: `data/management.db`.

### Build Integration

- `sqlc generate` added to the `generate` Makefile target.
- Generated code committed to the repository (standard sqlc practice).
- `goose` migration files embedded via `//go:embed`.
