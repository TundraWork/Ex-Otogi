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
const CurrentSchemaVersion = 4

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
	// ErrInputTooLarge reports input that exceeds a durable storage bound.
	ErrInputTooLarge = errors.New("telegram store: input too large")
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
	created, err := prepareDatabaseFile(cfg.Path)
	if err != nil {
		return nil, err
	}
	if cfg.AccountKey == "" {
		if created {
			_ = os.Remove(cfg.Path)
		}
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

func prepareDatabaseFile(path string) (bool, error) {
	if err := validatePath(path); err != nil {
		return false, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err == nil {
		return true, f.Close()
	}
	if !os.IsExist(err) {
		return false, fmt.Errorf("%w: create database: %w", ErrInvalidPath, err)
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		return false, fmt.Errorf("%w: existing database must be regular mode 0600", ErrInvalidPath)
	}
	if !ownedByCurrentUser(info) {
		return false, fmt.Errorf("%w: existing database must be owned by current user", ErrInvalidPath)
	}
	return false, nil
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
		version = CurrentSchemaVersion
	}
	if e != nil {
		return e
	}
	if version == 2 {
		if _, e = tx.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS recovery(account_key TEXT NOT NULL,box TEXT NOT NULL,position INTEGER NOT NULL DEFAULT 0,state TEXT NOT NULL,cause TEXT NOT NULL DEFAULT '',acknowledged INTEGER NOT NULL DEFAULT 0,PRIMARY KEY(account_key,box))"); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "ALTER TABLE quarantine ADD COLUMN reprocessed_at INTEGER"); e != nil && !strings.Contains(e.Error(), "duplicate column name") {
			return e
		}
		if _, e = tx.ExecContext(ctx, "UPDATE schema_meta SET version=4 WHERE singleton=1"); e != nil { return e }
		version = 4
	}
	if version == 3 {
		if _, e = tx.ExecContext(ctx, "ALTER TABLE quarantine ADD COLUMN reprocessed_at INTEGER"); e != nil && !strings.Contains(e.Error(), "duplicate column name") {
			return e
		}
		if _, e = tx.ExecContext(ctx, "UPDATE schema_meta SET version=4 WHERE singleton=1"); e != nil { return e }
		version = 4
	}
	if version == 4 {
		if _, e = tx.ExecContext(ctx, "INSERT INTO recovery(account_key,box,position,state,cause,acknowledged) SELECT account_key,box,position,'degraded',reason,0 FROM consistency WHERE degraded=1 ON CONFLICT(account_key,box) DO NOTHING"); e != nil { return e }
	}
	if version != CurrentSchemaVersion {
		return fmt.Errorf("unsupported schema version %d", version)
	}
	var key, gotdVersion string
	if e = validateCurrentSchema(ctx, tx); e != nil {
		return e
	}
	var uid int64
	var layer int
	e = tx.QueryRowContext(ctx, "SELECT account_key,authorized_user_id,gotd_version,tg_layer FROM binding WHERE singleton=1").Scan(&key, &uid, &gotdVersion, &layer)
	if errors.Is(e, sql.ErrNoRows) {
		_, e = tx.ExecContext(ctx, "INSERT INTO binding(singleton,account_key,authorized_user_id,gotd_version,tg_layer) VALUES(1,?,?,?,?)", cfg.AccountKey, cfg.AuthorizedUserID, cfg.GotdVersion, cfg.TGLayer)
	} else if e == nil && (key != cfg.AccountKey || uid != cfg.AuthorizedUserID || gotdVersion != cfg.GotdVersion || layer != cfg.TGLayer) {
		return fmt.Errorf("%w: database=(%q,%d,%q,%d) requested=(%q,%d,%q,%d)", ErrAccountMismatch, key, uid, gotdVersion, layer, cfg.AccountKey, cfg.AuthorizedUserID, cfg.GotdVersion, cfg.TGLayer)
	}
	if e != nil {
		return e
	}
	return tx.Commit()
}

type tableLayout struct {
	columns    []string
	primaryKey []string
}

func validateCurrentSchema(ctx context.Context, tx *sql.Tx) error {
	expected := map[string]tableLayout{
		"schema_meta":         {[]string{"singleton", "version"}, []string{"singleton"}},
		"binding":             {[]string{"singleton", "account_key", "authorized_user_id", "gotd_version", "tg_layer"}, []string{"singleton"}},
		"update_state":        {[]string{"account_key", "user_id", "pts", "qts", "date", "seq"}, []string{"account_key", "user_id"}},
		"channel_state":       {[]string{"account_key", "user_id", "channel_id", "pts"}, []string{"account_key", "user_id", "channel_id"}},
		"channel_access_hash": {[]string{"account_key", "user_id", "channel_id", "access_hash"}, []string{"account_key", "user_id", "channel_id"}},
		"user_hash":           {[]string{"account_key", "user_id", "target_id", "access_hash"}, []string{"account_key", "user_id", "target_id"}},
		"peers":               {[]string{"account_key", "kind", "peer_id", "access_hash", "payload"}, []string{"account_key", "kind", "peer_id"}},
		"journal":             {[]string{"id", "account_key", "payload", "received_at", "normalized_at"}, []string{"id"}},
		"inbox":               {[]string{"id", "account_key", "source_key", "conversation", "sequence", "payload", "available_at", "status", "owner", "lease_until", "attempts", "completed_at", "error"}, []string{"id"}},
		"quarantine":          {[]string{"id", "account_key", "source_key", "kind", "payload", "created_at", "reprocessed_at"}, []string{"id"}},
		"locators":            {[]string{"account_key", "namespace", "message_id", "conversation", "payload", "payload_hash", "created_at", "updated_at"}, []string{"account_key", "namespace", "message_id", "conversation"}},
		"consistency":         {[]string{"account_key", "box", "position", "degraded", "reason"}, []string{"account_key", "box"}},
		"recovery":            {[]string{"account_key", "box", "position", "state", "cause", "acknowledged"}, []string{"account_key", "box"}},
		"sends":               {[]string{"account_key", "key", "digest", "random_id", "peer_locator", "status", "retry_at", "error", "provider_message_id", "created_at", "updated_at", "terminal_at"}, []string{"account_key", "key"}},
	}
	for table, layout := range expected {
		rows, err := tx.QueryContext(ctx, "PRAGMA table_info("+table+")")
		if err != nil {
			return fmt.Errorf("inspect schema table %s: %w", table, err)
		}
		var columns []string
		primaryKey := make([]string, len(layout.primaryKey))
		for rows.Next() {
			var cid, notNull, keyPosition int
			var name, kind string
			var defaultValue any
			if err = rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &keyPosition); err != nil {
				_ = rows.Close()
				return err
			}
			columns = append(columns, name)
			if keyPosition > 0 {
				if keyPosition > len(primaryKey) {
					_ = rows.Close()
					return fmt.Errorf("unsupported schema layout for %s: primary key position %d", table, keyPosition)
				}
				primaryKey[keyPosition-1] = name
			}
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return err
		}
		if strings.Join(columns, ",") != strings.Join(layout.columns, ",") || strings.Join(primaryKey, ",") != strings.Join(layout.primaryKey, ",") {
			return fmt.Errorf("unsupported schema layout for %s: columns %v primary key %v", table, columns, primaryKey)
		}
	}
	return nil
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
CREATE TABLE IF NOT EXISTS channel_state(account_key TEXT NOT NULL,user_id INTEGER NOT NULL,channel_id INTEGER NOT NULL,pts INTEGER NOT NULL,PRIMARY KEY(account_key,user_id,channel_id));
CREATE TABLE IF NOT EXISTS channel_access_hash(account_key TEXT NOT NULL,user_id INTEGER NOT NULL,channel_id INTEGER NOT NULL,access_hash INTEGER NOT NULL,PRIMARY KEY(account_key,user_id,channel_id));
CREATE TABLE IF NOT EXISTS user_hash(account_key TEXT NOT NULL,user_id INTEGER NOT NULL,target_id INTEGER NOT NULL,access_hash INTEGER NOT NULL,PRIMARY KEY(account_key,user_id,target_id));
CREATE TABLE IF NOT EXISTS peers(account_key TEXT NOT NULL,kind TEXT NOT NULL,peer_id INTEGER NOT NULL,access_hash INTEGER NOT NULL,payload BLOB,PRIMARY KEY(account_key,kind,peer_id));
CREATE TABLE IF NOT EXISTS journal(id INTEGER PRIMARY KEY AUTOINCREMENT,account_key TEXT NOT NULL,payload BLOB NOT NULL,received_at INTEGER NOT NULL,normalized_at INTEGER);
CREATE TABLE IF NOT EXISTS inbox(id TEXT PRIMARY KEY,account_key TEXT NOT NULL,source_key TEXT NOT NULL,conversation TEXT NOT NULL,sequence INTEGER NOT NULL,payload BLOB,available_at INTEGER NOT NULL,status TEXT NOT NULL,owner TEXT,lease_until INTEGER,attempts INTEGER NOT NULL DEFAULT 0,completed_at INTEGER,error TEXT,UNIQUE(account_key,source_key));
CREATE TABLE IF NOT EXISTS quarantine(id INTEGER PRIMARY KEY AUTOINCREMENT,account_key TEXT NOT NULL,source_key TEXT NOT NULL,kind TEXT NOT NULL,payload BLOB,created_at INTEGER NOT NULL,reprocessed_at INTEGER,UNIQUE(account_key,source_key));
CREATE TABLE IF NOT EXISTS locators(account_key TEXT NOT NULL,namespace TEXT NOT NULL,message_id INTEGER NOT NULL,conversation TEXT NOT NULL,payload BLOB,payload_hash BLOB NOT NULL,created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL,PRIMARY KEY(account_key,namespace,message_id,conversation));
CREATE TABLE IF NOT EXISTS consistency(account_key TEXT NOT NULL,box TEXT NOT NULL,position INTEGER NOT NULL DEFAULT 0,degraded INTEGER NOT NULL DEFAULT 0,reason TEXT NOT NULL DEFAULT '',PRIMARY KEY(account_key,box));
CREATE TABLE IF NOT EXISTS recovery(account_key TEXT NOT NULL,box TEXT NOT NULL,position INTEGER NOT NULL DEFAULT 0,state TEXT NOT NULL,cause TEXT NOT NULL DEFAULT '',acknowledged INTEGER NOT NULL DEFAULT 0,PRIMARY KEY(account_key,box));
CREATE TABLE IF NOT EXISTS sends(account_key TEXT NOT NULL,key TEXT NOT NULL,digest BLOB NOT NULL,random_id INTEGER NOT NULL,peer_locator BLOB,status TEXT NOT NULL,retry_at INTEGER,error TEXT,provider_message_id INTEGER,created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL,terminal_at INTEGER,PRIMARY KEY(account_key,key));
`
