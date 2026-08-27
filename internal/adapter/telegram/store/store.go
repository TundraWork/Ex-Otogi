package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gotd/td/telegram/updates"
	_ "modernc.org/sqlite"
)

// CurrentSchemaVersion is the only schema version supported by this experimental store.
const CurrentSchemaVersion = 1

var (
	// ErrInvalidPath reports a non-local, non-regular, or URI-like database path.
	ErrInvalidPath = errors.New("telegram store: path must be a local regular file")
	// ErrAccountMismatch reports a database bound to another deployment account.
	ErrAccountMismatch = errors.New("telegram store: account binding mismatch")
	// ErrStateNotFound reports a partial gotd state mutation without initial state.
	ErrStateNotFound = errors.New("telegram store: update state not found")
	// ErrStartupOverflow reports more unnormalized journal rows than startup can buffer.
	ErrStartupOverflow = errors.New("telegram store: startup journal buffer overflow")
	// ErrClaimConflict reports a stale or foreign inbox claim owner.
	ErrClaimConflict = errors.New("telegram store: claim conflict")
	// ErrSendConflict reports an idempotency digest or compare-and-swap conflict.
	ErrSendConflict = errors.New("telegram store: send intent conflict")
	// ErrInvalidTransition reports an unsupported send state edge.
	ErrInvalidTransition = errors.New("telegram store: invalid send transition")
)

// Config configures a durable account-bound SQLite store.
type Config struct {
	// Path is the absolute local SQLite file path.
	Path string
	// AccountKey is the immutable deployment account key.
	AccountKey string
	// GotdVersion records the gotd module version that created the binding.
	GotdVersion string
	// AuthorizedUserID is the immutable authorized Telegram user ID.
	AuthorizedUserID int64
	// TGLayer records the Telegram API layer used by the binding.
	TGLayer int
	// MaxReaders bounds concurrent read connections in addition to one writer.
	MaxReaders int
	// BusyTimeout bounds SQLite lock waiting.
	BusyTimeout time.Duration
}

// DatabaseInfo exposes effective durability and connection settings.
type DatabaseInfo struct {
	// SchemaVersion is the installed schema version.
	SchemaVersion int
	// JournalMode is SQLite's effective journal mode.
	JournalMode string
	// ForeignKeys reports whether foreign keys are enabled.
	ForeignKeys bool
	// BusyTimeoutMS is the configured lock wait in milliseconds.
	BusyTimeoutMS int
	// MaxOpenConnections is the bounded pool size.
	MaxOpenConnections int
}

// Store persists Telegram adapter state for one deployment account.
type Store struct {
	db               *sql.DB
	accountKey       string
	authorizedUserID int64
	maxConns, busyMS int
}

// Open validates path and binding, applies the current schema, and opens the store.
func Open(ctx context.Context, cfg Config) (*Store, error) {
	if err := validatePath(cfg.Path); err != nil {
		return nil, err
	}
	if cfg.AccountKey == "" {
		return nil, fmt.Errorf("%w: empty account key", ErrInvalidPath)
	}
	if cfg.MaxReaders <= 0 {
		cfg.MaxReaders = 4
	}
	if cfg.BusyTimeout <= 0 {
		cfg.BusyTimeout = 5 * time.Second
	}
	busy := int(cfg.BusyTimeout / time.Millisecond)
	dsn := "file:" + filepath.ToSlash(cfg.Path) + "?_foreign_keys=on&_journal_mode=WAL&_busy_timeout=" + fmt.Sprint(busy)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1 + cfg.MaxReaders)
	db.SetMaxIdleConns(1 + cfg.MaxReaders)
	s := &Store{db: db, accountKey: cfg.AccountKey, authorizedUserID: cfg.AuthorizedUserID, maxConns: 1 + cfg.MaxReaders, busyMS: busy}
	if err = s.migrate(ctx, cfg); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}
func validatePath(path string) error {
	if path == "" || path == ":memory:" || strings.ContainsAny(path, "?#") || strings.HasPrefix(path, "file:") || !filepath.IsAbs(path) {
		return ErrInvalidPath
	}
	if i, e := os.Lstat(path); e == nil {
		if i.Mode()&os.ModeSymlink != 0 || !i.Mode().IsRegular() {
			return ErrInvalidPath
		}
	} else if !os.IsNotExist(e) {
		return fmt.Errorf("%w: %w", ErrInvalidPath, e)
	}
	p := filepath.Dir(path)
	i, e := os.Stat(p)
	if e != nil || !i.IsDir() {
		return ErrInvalidPath
	}
	if e = validateLocalFilesystem(p); e != nil {
		return fmt.Errorf("%w: %w", ErrInvalidPath, e)
	}
	return nil
}
func (s *Store) migrate(ctx context.Context, cfg Config) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer func() { _ = tx.Rollback() }()
	if _, e = tx.ExecContext(ctx, schema); e != nil {
		return e
	}
	var version int
	e = tx.QueryRowContext(ctx, "SELECT version FROM schema_meta WHERE singleton=1").Scan(&version)
	if errors.Is(e, sql.ErrNoRows) {
		_, e = tx.ExecContext(ctx, "INSERT INTO schema_meta(singleton,version) VALUES(1,?)", CurrentSchemaVersion)
	}
	if e != nil {
		return e
	}
	if version != 0 && version != CurrentSchemaVersion {
		return fmt.Errorf("unsupported schema version %d", version)
	}
	var key string
	var uid int64
	e = tx.QueryRowContext(ctx, "SELECT account_key,authorized_user_id FROM binding WHERE singleton=1").Scan(&key, &uid)
	if errors.Is(e, sql.ErrNoRows) {
		_, e = tx.ExecContext(ctx, "INSERT INTO binding(singleton,account_key,authorized_user_id,gotd_version,tg_layer) VALUES(1,?,?,?,?)", cfg.AccountKey, cfg.AuthorizedUserID, cfg.GotdVersion, cfg.TGLayer)
	} else if e == nil && (key != cfg.AccountKey || uid != cfg.AuthorizedUserID) {
		return ErrAccountMismatch
	}
	if e != nil {
		return e
	}
	return tx.Commit()
}

// Close closes all SQLite connections.
func (s *Store) Close() error { return s.db.Close() }

// Info returns effective schema, pragma, and pool information.
func (s *Store) Info(ctx context.Context) (DatabaseInfo, error) {
	var i DatabaseInfo
	i.SchemaVersion = CurrentSchemaVersion
	i.MaxOpenConnections = s.maxConns
	i.BusyTimeoutMS = s.busyMS
	if e := s.db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&i.JournalMode); e != nil {
		return i, e
	}
	var fk int
	if e := s.db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); e != nil {
		return i, e
	}
	i.ForeignKeys = fk == 1
	return i, nil
}

var _ updates.StateStorage = (*Store)(nil)
var _ updates.ChannelAccessHasher = (*Store)(nil)
var _ updates.UserAccessHasher = (*Store)(nil)

const schema = `
CREATE TABLE IF NOT EXISTS schema_meta(singleton INTEGER PRIMARY KEY CHECK(singleton=1),version INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS binding(singleton INTEGER PRIMARY KEY CHECK(singleton=1),account_key TEXT NOT NULL,authorized_user_id INTEGER NOT NULL,gotd_version TEXT NOT NULL,tg_layer INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS update_state(account_key TEXT NOT NULL,user_id INTEGER NOT NULL,pts INTEGER NOT NULL,qts INTEGER NOT NULL,date INTEGER NOT NULL,seq INTEGER NOT NULL,PRIMARY KEY(account_key,user_id));
CREATE TABLE IF NOT EXISTS channel_state(account_key TEXT NOT NULL,user_id INTEGER NOT NULL,channel_id INTEGER NOT NULL,pts INTEGER NOT NULL,access_hash INTEGER,PRIMARY KEY(account_key,user_id,channel_id));
CREATE TABLE IF NOT EXISTS user_hash(account_key TEXT NOT NULL,user_id INTEGER NOT NULL,target_id INTEGER NOT NULL,access_hash INTEGER NOT NULL,PRIMARY KEY(account_key,user_id,target_id));
CREATE TABLE IF NOT EXISTS peers(account_key TEXT NOT NULL,kind TEXT NOT NULL,peer_id INTEGER NOT NULL,access_hash INTEGER NOT NULL,payload BLOB,PRIMARY KEY(account_key,kind,peer_id));
CREATE TABLE IF NOT EXISTS journal(id INTEGER PRIMARY KEY AUTOINCREMENT,account_key TEXT NOT NULL,payload BLOB NOT NULL,received_at INTEGER NOT NULL,normalized_at INTEGER);
CREATE TABLE IF NOT EXISTS inbox(id TEXT PRIMARY KEY,account_key TEXT NOT NULL,source_key TEXT NOT NULL,conversation TEXT NOT NULL,sequence INTEGER NOT NULL,payload BLOB,available_at INTEGER NOT NULL,status TEXT NOT NULL,owner TEXT,lease_until INTEGER,attempts INTEGER NOT NULL DEFAULT 0,completed_at INTEGER,error TEXT,UNIQUE(account_key,source_key));
CREATE INDEX IF NOT EXISTS inbox_order ON inbox(account_key,available_at,conversation,sequence);
CREATE TABLE IF NOT EXISTS quarantine(id INTEGER PRIMARY KEY AUTOINCREMENT,account_key TEXT NOT NULL,source_key TEXT NOT NULL,kind TEXT NOT NULL,payload BLOB,created_at INTEGER NOT NULL,UNIQUE(account_key,source_key));
CREATE TABLE IF NOT EXISTS locators(account_key TEXT NOT NULL,namespace TEXT NOT NULL,message_id INTEGER NOT NULL,conversation TEXT NOT NULL,payload BLOB,payload_hash BLOB,PRIMARY KEY(account_key,namespace,message_id));
CREATE TABLE IF NOT EXISTS consistency(account_key TEXT NOT NULL,box TEXT NOT NULL,position INTEGER NOT NULL DEFAULT 0,degraded INTEGER NOT NULL DEFAULT 0,reason TEXT NOT NULL DEFAULT '',PRIMARY KEY(account_key,box));
CREATE TABLE IF NOT EXISTS sends(account_key TEXT NOT NULL,key TEXT NOT NULL,digest BLOB NOT NULL,random_id INTEGER NOT NULL,peer_locator BLOB,status TEXT NOT NULL,retry_at INTEGER,error TEXT,provider_message_id INTEGER,created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL,terminal_at INTEGER,PRIMARY KEY(account_key,key));
`
