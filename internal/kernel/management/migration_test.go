package management

import (
	"database/sql"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/pressly/goose/v3"
)

func TestMigrationRunsOnFreshDatabase(t *testing.T) {
	t.Parallel()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	migrationsDir, err := fs.Sub(MigrationsFS, "migrations")
	if err != nil {
		t.Fatalf("sub filesystem: %v", err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, migrationsDir)
	if err != nil {
		t.Fatalf("create goose provider: %v", err)
	}
	if _, err := provider.Up(t.Context()); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	var objectName string
	for _, table := range []string{"management_meta", "events", "artifacts", "snapshots"} {
		row := db.QueryRow("SELECT name FROM sqlite_master WHERE type=\"table\" AND name=?", table)
		if err := row.Scan(&objectName); err != nil {
			t.Fatalf("table %q not found after migration: %v", table, err)
		}
	}

	indexes := []string{
		"idx_events_occurred_at", "idx_events_kind", "idx_events_trace_id",
		"idx_events_category", "idx_events_conversation_id", "idx_events_module",
		"idx_artifacts_event_id", "idx_snapshots_namespace", "idx_snapshots_module",
	}
	for _, index := range indexes {
		row := db.QueryRow("SELECT name FROM sqlite_master WHERE type=\"index\" AND name=?", index)
		if err := row.Scan(&objectName); err != nil {
			t.Fatalf("index %q not found after migration: %v", index, err)
		}
	}
}

func TestSQLiteStoreRejectsUnmarkedLegacyDatabase(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy-management.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open legacy sqlite: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE events (id INTEGER PRIMARY KEY AUTOINCREMENT)`); err != nil {
		t.Fatalf("prepare legacy database: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy sqlite: %v", err)
	}

	_, err = NewSQLiteStore(t.Context(), dbPath)
	if err == nil {
		t.Fatal("NewSQLiteStore error = nil, want reset instruction")
	}
	if !strings.Contains(err.Error(), "reset "+dbPath) {
		t.Fatalf("error = %q, want explicit reset path", err)
	}
}
