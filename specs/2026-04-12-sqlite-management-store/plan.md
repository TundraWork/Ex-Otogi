# Plan: SQLite Management Store via sqlc

## Milestone 1: Project Scaffolding and Dependencies

Add Go dependencies and sqlc configuration.

- Add `modernc.org/sqlite` and `github.com/pressly/goose/v3` to `go.mod`.
- Add `github.com/sqlc-dev/sqlc` to the tooling pinned in `mise.toml` (or as a go install in the Makefile).
- Create `sqlc.yaml` at project root pointing to `internal/kernel/management/queries/` and outputting to `internal/sqlc/management/`.
- Add `sqlc generate` to the Makefile `generate` target.
- Verify `go build ./...` compiles cleanly.

**Success criterion**: `go build ./...` and `make generate` succeed.

## Milestone 2: Schema and Migrations

Define the SQLite schema and goose migration files.

- Create `internal/kernel/management/migrations/001_create_tables.sql` with the `events`, `artifacts`, and `snapshots` table definitions and indexes from the design.
- Add `//go:embed` directive to embed the migrations directory.
- Verify the migration runs against a fresh SQLite database in a test.

**Success criterion**: A test opens a fresh SQLite database, runs the embedded migration, and confirms the tables exist.

## Milestone 3: sqlc Query Definitions and Code Generation

Write SQL query files and generate Go code.

- Create `internal/kernel/management/queries/events.sql` with queries for:
  - `InsertEvent`
  - `GetEvent`
  - `ListEvents` (using the "empty-string-means-no-filter" SQL pattern: `WHERE (:category = '' OR category = :category) AND ... AND id > :after_id ORDER BY id ASC LIMIT :limit`)
  - `GetTraceEvents`
  - `CountEventsByCategory`
  - `CountRecentErrors`
  - `CountDistinctTraceIDs`
  - `MinEventID` / `MaxEventID`
  - `CountEvents`
- Create `internal/kernel/management/queries/artifacts.sql` with queries for:
  - `InsertArtifact`
  - `GetArtifact`
  - `ListArtifactIDsByEventID`
- Create `internal/kernel/management/queries/snapshots.sql` with queries for:
  - `UpsertSnapshot`
  - `ListSnapshots` (with dynamic WHERE for namespace, key, module)
- Run `sqlc generate` and commit the generated code under `internal/sqlc/management/`.
- Verify the generated code compiles.

**Success criterion**: `sqlc generate` produces code in `internal/sqlc/management/` and `go build ./...` succeeds.

## Milestone 4: SQLiteStore Implementation

Implement the `SQLiteStore` struct that satisfies `panel.Recorder` and `panel.Query`.

- Create `internal/kernel/management/sqlite_store.go` with:
  - `SQLiteStore` struct holding `*sql.DB` and `*sqlc.Queries`.
  - `NewSQLiteStore(ctx context.Context, dbPath string) (*SQLiteStore, error)` that opens SQLite, enables WAL mode, runs goose migrations, and initializes sqlc queries.
  - `Close() error` method.
- Implement `panel.Recorder`:
  - `RecordEvent`: insert into `events`, return the event with the auto-generated ID.
  - `RecordArtifact`: insert into `artifacts`, verify the referenced event exists.
  - `UpsertSnapshot`: upsert into `snapshots`.
- Implement `panel.Query`:
  - `ListEvents`: use the sqlc-generated query with the "empty-string-means-no-filter" WHERE pattern, return `EventPage`.
  - `GetEvent`: fetch by ID.
  - `GetTrace`: fetch by trace_id.
  - `GetArtifact`: fetch by ID.
  - `ListSnapshots`: fetch with filters.
  - `GetOverview`: aggregate queries for counts and window IDs.
- Payload serialization/deserialization:
  - On write: `json.Marshal(payload)` into `payload_json`, store `payload_type` from the Go type name.
  - On read: `json.Unmarshal` into the correct payload struct based on `payload_type`.
  - `ArtifactIDs` reconstructed from the `artifacts` table at read time.
- `EventPage.CursorResetRequired` always `false` (no eviction).
- `EventPage.WindowStartID`/`WindowEndID` from `MinEventID`/`MaxEventID`.

**Success criterion**: `SQLiteStore` compiles and implements both interfaces.

## Milestone 5: Migrate Tests

Port and adapt the existing in-memory store tests to the SQLite store.

- Adapt tests from `internal/kernel/management/store_test.go`:
  - Each test creates a fresh `SQLiteStore` backed by a temporary SQLite file.
  - `TestRecordEventAssignsMonotonicIDs`
  - `TestListEventsOrderedAndFiltered`
  - `TestRecordArtifactLinksArtifactIDBackToEvent`
  - `TestArtifactLookup`
- Remove or skip `TestRetentionEvictsOldestEventsAndArtifacts` and `TestCursorResetRequiredWhenAfterIDFallsBehindWindow` since retention limits are removed.
- Add a new test verifying that data persists across store close/reopen.
- Verify all tests pass with `go test -race ./internal/kernel/management/...`.

**Success criterion**: All management store tests pass with `-race`.

## Milestone 6: Wire Up in Application

Replace the in-memory store with `SQLiteStore` in the application entrypoint.

- Update `cmd/bot/app.go`:
  - Add `management.database_path` config field (default `data/management.db`).
  - Replace `kernelmanagement.NewService(limits)` with `NewSQLiteStore(ctx, dbPath)`.
  - Remove the `Limits` struct usage and `maxEvents`/`maxArtifacts`/`maxArtifactBytes`/`maxSnapshots` config (accept but ignore for backward compatibility).
  - Register `SQLiteStore` as both `panel.ServiceRecorder` and `panel.ServiceQuery`.
  - Close `SQLiteStore` on shutdown.
- Ensure the `data/` directory exists before opening the database (create if needed).
- Verify the application starts and the management HTTP API works.

**Success criterion**: `go run ./cmd/bot` starts, management API responds to `/panel/overview`, events are persisted to SQLite.

## Milestone 7: Quality Verification

Run the full quality gate.

- `make quality-core` — doctor, fmt-check, lint, arch-check, test-race, test-leak.
- Fix any lint issues or test failures.
- Verify `go test -race ./...` passes across the entire project.
- Verify the management store tests still pass independently.

**Success criterion**: `make quality-core` exits 0.
