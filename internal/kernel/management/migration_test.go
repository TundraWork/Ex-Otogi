package management

import (
	"database/sql"
	"io/fs"
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

	var tableName string
	tables := []string{"events", "artifacts", "snapshots"}
	for _, table := range tables {
		row := db.QueryRow(
			"SELECT name FROM sqlite_master WHERE type='table' AND name=?",
			table,
		)
		if err := row.Scan(&tableName); err != nil {
			t.Fatalf("table %q not found after migration: %v", table, err)
		}
	}

	indexes := []string{
		"idx_events_occurred_at",
		"idx_events_kind",
		"idx_events_trace_id",
		"idx_events_category",
		"idx_events_conversation_id",
		"idx_events_module",
		"idx_artifacts_event_id",
		"idx_snapshots_namespace",
		"idx_snapshots_module",
	}
	for _, idx := range indexes {
		row := db.QueryRow(
			"SELECT name FROM sqlite_master WHERE type='index' AND name=?",
			idx,
		)
		if err := row.Scan(&tableName); err != nil {
			t.Fatalf("index %q not found after migration: %v", idx, err)
		}
	}
}
