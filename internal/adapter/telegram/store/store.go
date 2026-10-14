// Package store persists the Telegram adapter's state in one SQLite database
// per account: gotd update state, peer access hashes, the durable inbox,
// message locators, sent messages, and recorded gaps.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite" // database/sql driver "sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

// ErrAccountMismatch reports a database that belongs to another account.
var ErrAccountMismatch = errors.New("telegram store: database belongs to another account")

// Store is the adapter's SQLite state. It is safe for concurrent use; a single
// connection serializes access, which is ample for one account.
type Store struct {
	db *sql.DB
}

// Open opens or creates the database at path, creating its directory with
// mode 0700, and applies pending migrations.
func Open(ctx context.Context, path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("telegram store: create directory: %w", err)
	}
	dsn := "file:" + filepath.ToSlash(path) +
		"?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("telegram store: open: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := migrate(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func migrate(ctx context.Context, db *sql.DB) error {
	sub, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return fmt.Errorf("telegram store: migrations: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, sub)
	if err != nil {
		return fmt.Errorf("telegram store: migrations: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("telegram store: migrate: %w", err)
	}
	return nil
}

// Bind records which account owns the database on first use and refuses a
// database that already belongs to another account key or Telegram user.
func (s *Store) Bind(ctx context.Context, account string, userID int64) error {
	var gotAccount string
	var gotUser int64
	err := s.db.QueryRowContext(ctx, "SELECT account, user_id FROM binding").Scan(&gotAccount, &gotUser)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		_, err = s.db.ExecContext(ctx, "INSERT INTO binding (singleton, account, user_id) VALUES (1, ?, ?)", account, userID)
		if err != nil {
			return fmt.Errorf("telegram store: bind: %w", err)
		}
		return nil
	case err != nil:
		return fmt.Errorf("telegram store: read binding: %w", err)
	case gotAccount != account || gotUser != userID:
		return fmt.Errorf("%w: database has (%q, user %d), want (%q, user %d)", ErrAccountMismatch, gotAccount, gotUser, account, userID)
	}
	return nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }
