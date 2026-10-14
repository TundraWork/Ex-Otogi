// Package management provides the structured event log and the HTTP API
// operators use to query it and check status.
package management

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite" // database/sql driver "sqlite"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

//go:embed migrations/*.sql
var migrations embed.FS

// EventLog is the SQLite-backed event log. It implements otogi.Recorder.
type EventLog struct {
	db  *sql.DB
	log *slog.Logger

	mu        sync.Mutex
	lastError error
	errorAt   time.Time
}

var _ otogi.Recorder = (*EventLog)(nil)

// OpenEventLog opens or creates the event log at path and applies migrations.
func OpenEventLog(ctx context.Context, path string, log *slog.Logger) (*EventLog, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("event log: create directory: %w", err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("event log: open: %w", err)
	}
	db.SetMaxOpenConns(1)
	sub, err := fs.Sub(migrations, "migrations")
	if err == nil {
		var provider *goose.Provider
		if provider, err = goose.NewProvider(goose.DialectSQLite3, db, sub); err == nil {
			_, err = provider.Up(ctx)
		}
	}
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("event log: migrate: %w", err)
	}
	return &EventLog{db: db, log: log}, nil
}

// Close closes the database.
func (l *EventLog) Close() error { return l.db.Close() }

// Record writes r. A failure is logged and kept for Health; it never reaches
// the caller.
func (l *EventLog) Record(ctx context.Context, r otogi.Record) {
	payload, err := json.Marshal(r.Payload)
	if err != nil {
		payload, _ = json.Marshal(map[string]string{"unencodable_payload": fmt.Sprintf("%+v", r.Payload), "error": err.Error()})
	}
	_, err = l.db.ExecContext(ctx, `INSERT INTO records (at, kind, component, event, platform, account, conversation, outcome, duration_ms, payload)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.At.UnixMilli(), r.Kind, r.Component, r.Event, r.Platform, r.Account, r.Conversation, r.Outcome, r.Duration.Milliseconds(), string(payload))
	if err != nil {
		l.log.Error("event log: write failed", "kind", r.Kind, "event", r.Event, "error", err)
		l.mu.Lock()
		l.lastError, l.errorAt = err, time.Now()
		l.mu.Unlock()
	}
}

// Entry is a stored record as the API returns it.
type Entry struct {
	ID           int64           `json:"id"`
	At           time.Time       `json:"at"`
	Kind         string          `json:"kind"`
	Component    string          `json:"component"`
	Event        string          `json:"event,omitempty"`
	Platform     string          `json:"platform,omitempty"`
	Account      string          `json:"account,omitempty"`
	Conversation string          `json:"conversation,omitempty"`
	Outcome      string          `json:"outcome,omitempty"`
	DurationMS   int64           `json:"duration_ms,omitempty"`
	Payload      json.RawMessage `json:"payload"`
}

// Query selects records. Zero fields do not filter.
type Query struct {
	Kind, Component, Event, Platform, Account, Conversation, Outcome string
	// Text matches records whose payload contains it.
	Text         string
	Since, Until time.Time
	// Before pages to records older than this ID; After polls records newer
	// than this ID. At most one may be set.
	Before, After int64
	Limit         int
}

// Page is one page of records, newest first.
type Page struct {
	Records []Entry `json:"records"`
	// More reports whether further records match in the paging direction.
	More bool `json:"more"`
	// OldestRetained is the ID of the oldest record still kept.
	OldestRetained int64 `json:"oldest_retained"`
}

// MaxPageSize bounds Query.Limit.
const MaxPageSize = 500

// Query returns matching records, newest first.
func (l *EventLog) Query(ctx context.Context, q Query) (Page, error) {
	if q.Before != 0 && q.After != 0 {
		return Page{}, fmt.Errorf("before and after are exclusive: %w", otogi.ErrInvalid)
	}
	if q.Limit <= 0 {
		q.Limit = 100
	}
	q.Limit = min(q.Limit, MaxPageSize)
	var where []string
	var args []any
	for _, f := range []struct{ column, value string }{
		{"kind", q.Kind}, {"component", q.Component}, {"event", q.Event}, {"platform", q.Platform},
		{"account", q.Account}, {"conversation", q.Conversation}, {"outcome", q.Outcome},
	} {
		if f.value != "" {
			where, args = append(where, f.column+" = ?"), append(args, f.value)
		}
	}
	if q.Text != "" {
		where, args = append(where, "payload LIKE ? ESCAPE '\\'"), append(args, "%"+likeEscaper.Replace(q.Text)+"%")
	}
	if !q.Since.IsZero() {
		where, args = append(where, "at >= ?"), append(args, q.Since.UnixMilli())
	}
	if !q.Until.IsZero() {
		where, args = append(where, "at < ?"), append(args, q.Until.UnixMilli())
	}
	order := "DESC"
	switch {
	case q.Before != 0:
		where, args = append(where, "id < ?"), append(args, q.Before)
	case q.After != 0:
		// Poll upward from the cursor so no record is skipped, then present
		// newest first like every page.
		where, args, order = append(where, "id > ?"), append(args, q.After), "ASC"
	}
	query := "SELECT id, at, kind, component, event, platform, account, conversation, outcome, duration_ms, payload FROM records"
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY id " + order + " LIMIT ?"
	entries, err := l.scan(ctx, query, append(args, q.Limit+1)...)
	if err != nil {
		return Page{}, err
	}
	page := Page{More: len(entries) > q.Limit}
	page.Records = entries[:min(len(entries), q.Limit)]
	if order == "ASC" {
		for i, j := 0, len(page.Records)-1; i < j; i, j = i+1, j-1 {
			page.Records[i], page.Records[j] = page.Records[j], page.Records[i]
		}
	}
	page.OldestRetained, err = l.oldest(ctx)
	return page, err
}

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// Trace returns every record of one event, in order.
func (l *EventLog) Trace(ctx context.Context, event string) ([]Entry, error) {
	return l.scan(ctx, "SELECT id, at, kind, component, event, platform, account, conversation, outcome, duration_ms, payload FROM records WHERE event = ? ORDER BY id", event)
}

func (l *EventLog) scan(ctx context.Context, query string, args ...any) ([]Entry, error) {
	rows, err := l.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("event log: query: %w", err)
	}
	defer func() { _ = rows.Close() }()
	entries := []Entry{}
	for rows.Next() {
		var e Entry
		var at int64
		var payload string
		if err := rows.Scan(&e.ID, &at, &e.Kind, &e.Component, &e.Event, &e.Platform, &e.Account, &e.Conversation, &e.Outcome, &e.DurationMS, &payload); err != nil {
			return nil, fmt.Errorf("event log: scan: %w", err)
		}
		e.At, e.Payload = time.UnixMilli(at).UTC(), json.RawMessage(payload)
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("event log: query: %w", err)
	}
	return entries, nil
}

func (l *EventLog) oldest(ctx context.Context) (int64, error) {
	var id sql.NullInt64
	if err := l.db.QueryRowContext(ctx, "SELECT MIN(id) FROM records").Scan(&id); err != nil {
		return 0, fmt.Errorf("event log: oldest: %w", err)
	}
	return id.Int64, nil
}

// Prune deletes records older than before.
func (l *EventLog) Prune(ctx context.Context, before time.Time) error {
	if _, err := l.db.ExecContext(ctx, "DELETE FROM records WHERE at < ?", before.UnixMilli()); err != nil {
		return fmt.Errorf("event log: prune: %w", err)
	}
	return nil
}

// RunPruner prunes records older than retention every hour until ctx ends.
func (l *EventLog) RunPruner(ctx context.Context, retention time.Duration) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		if err := l.Prune(ctx, time.Now().Add(-retention)); err != nil && !errors.Is(err, context.Canceled) {
			l.log.Warn("event log: prune failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Health describes the event log's state for status.
type Health struct {
	OK             bool      `json:"ok"`
	LastError      string    `json:"last_error,omitempty"`
	LastErrorAt    time.Time `json:"last_error_at,omitzero"`
	OldestRetained int64     `json:"oldest_retained"`
}

// Health reports the last write failure, if any, and the oldest record.
func (l *EventLog) Health(ctx context.Context) Health {
	l.mu.Lock()
	h := Health{OK: l.lastError == nil, LastErrorAt: l.errorAt}
	if l.lastError != nil {
		h.LastError = l.lastError.Error()
	}
	l.mu.Unlock()
	h.OldestRetained, _ = l.oldest(ctx)
	return h
}
