// Package modulestore persists module state: per-module key/value data in
// <data_dir>/modules.sqlite, and private databases at
// <data_dir>/modules/<id>.sqlite.
package modulestore

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite" // database/sql driver "sqlite"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Storage owns the shared module database and the private ones.
type Storage struct {
	dir string
	db  *sql.DB

	mu      sync.Mutex
	private map[otogi.ModuleID]*sql.DB
}

// Open opens <dir>/modules.sqlite, applying migrations.
func Open(ctx context.Context, dir string) (*Storage, error) {
	db, err := openSQLite(filepath.Join(dir, "modules.sqlite"))
	if err != nil {
		return nil, err
	}
	sub, err := fs.Sub(migrations, "migrations")
	if err == nil {
		var provider *goose.Provider
		if provider, err = goose.NewProvider(goose.DialectSQLite3, db, sub); err == nil {
			_, err = provider.Up(ctx)
		}
	}
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("module store: migrate: %w", err)
	}
	return &Storage{dir: dir, db: db, private: map[otogi.ModuleID]*sql.DB{}}, nil
}

func openSQLite(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("module store: create directory: %w", err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, fmt.Errorf("module store: open %s: %w", path, err)
	}
	return db, nil
}

// Store returns the key/value store of one module.
func (s *Storage) Store(module otogi.ModuleID) otogi.Store {
	return kv{db: s.db, module: string(module)}
}

// Database opens, once, the private database of one module.
func (s *Storage) Database(_ context.Context, module otogi.ModuleID) (*sql.DB, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if db, ok := s.private[module]; ok {
		return db, nil
	}
	db, err := openSQLite(filepath.Join(s.dir, "modules", string(module)+".sqlite"))
	if err != nil {
		return nil, err
	}
	s.private[module] = db
	return db, nil
}

// Close closes every database.
func (s *Storage) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	errs := []error{s.db.Close()}
	for _, db := range s.private {
		errs = append(errs, db.Close())
	}
	return errors.Join(errs...)
}

type kv struct {
	db     *sql.DB
	module string
}

func (s kv) Get(ctx context.Context, key string) ([]byte, bool, error) {
	var value []byte
	err := s.db.QueryRowContext(ctx, "SELECT value FROM kv WHERE module = ? AND key = ?", s.module, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("module store: get %q: %w", key, err)
	}
	return value, true, nil
}

func (s kv) Put(ctx context.Context, key string, value []byte) error {
	if value == nil {
		value = []byte{}
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO kv (module, key, value, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (module, key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		s.module, key, value, time.Now().UnixMilli())
	if err != nil {
		return fmt.Errorf("module store: put %q: %w", key, err)
	}
	return nil
}

func (s kv) Delete(ctx context.Context, key string) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM kv WHERE module = ? AND key = ?", s.module, key); err != nil {
		return fmt.Errorf("module store: delete %q: %w", key, err)
	}
	return nil
}

func (s kv) List(ctx context.Context, prefix string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT key FROM kv WHERE module = ? AND instr(key, ?) = 1 ORDER BY key", s.module, prefix)
	if err != nil {
		return nil, fmt.Errorf("module store: list %q: %w", prefix, err)
	}
	defer func() { _ = rows.Close() }()
	keys := []string{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("module store: list %q: %w", prefix, err)
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("module store: list %q: %w", prefix, err)
	}
	return keys, nil
}
