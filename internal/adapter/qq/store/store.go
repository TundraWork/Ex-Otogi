// Package store persists the QQ adapter's state in one SQLite database per
// bot: the gateway session, the durable inbox, message references, and
// recorded gaps.
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
	"time"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite" // database/sql driver "sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

// ErrAccountMismatch reports a database that belongs to another bot.
var ErrAccountMismatch = errors.New("qq store: database belongs to another bot")

// Retention periods applied by Prune.
const (
	InboxRetention = 7 * 24 * time.Hour
	RefRetention   = 30 * 24 * time.Hour
	// SendRetention outlasts any replay of the event that caused a send.
	SendRetention = 7 * 24 * time.Hour
)

// Store is the adapter's SQLite state. It is safe for concurrent use; a single
// connection serializes access, which is ample for one bot.
type Store struct {
	db *sql.DB
}

// Open opens or creates the database at path, creating its directory with
// mode 0700, and applies pending migrations.
func Open(ctx context.Context, path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("qq store: create directory: %w", err)
	}
	dsn := "file:" + filepath.ToSlash(path) +
		"?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("qq store: open: %w", err)
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
		return fmt.Errorf("qq store: migrations: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, sub)
	if err != nil {
		return fmt.Errorf("qq store: migrations: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("qq store: migrate: %w", err)
	}
	return nil
}

// Bind records which bot owns the database on first use and refuses a
// database that already belongs to another account key or AppID: its
// session, inbox, and references mean nothing to another bot.
func (s *Store) Bind(ctx context.Context, account, appID string) error {
	var gotAccount, gotApp string
	err := s.db.QueryRowContext(ctx, "SELECT account, app_id FROM binding").Scan(&gotAccount, &gotApp)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return s.exec(ctx, "qq store: bind", "INSERT INTO binding (singleton, account, app_id) VALUES (1, ?, ?)", account, appID)
	case err != nil:
		return fmt.Errorf("qq store: read binding: %w", err)
	case gotAccount != account || gotApp != appID:
		return fmt.Errorf("%w: database has (%q, app %s), want (%q, app %s)", ErrAccountMismatch, gotAccount, gotApp, account, appID)
	}
	return nil
}

// Session is the stored gateway session.
type Session struct {
	ID        string
	Seq       int64
	UpdatedAt time.Time
}

// Session returns the stored gateway session, if any.
func (s *Store) Session(ctx context.Context) (Session, bool, error) {
	var sess Session
	var at int64
	err := s.db.QueryRowContext(ctx, "SELECT session_id, seq, updated_at FROM session").Scan(&sess.ID, &sess.Seq, &at)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Session{}, false, nil
	case err != nil:
		return Session{}, false, fmt.Errorf("read session: %w", err)
	}
	sess.UpdatedAt = time.UnixMilli(at).UTC()
	return sess, true, nil
}

// SetSession stores the gateway session and its last sequence number.
func (s *Store) SetSession(ctx context.Context, sess Session) error {
	return s.exec(ctx, "store session", `INSERT INTO session (singleton, session_id, seq, updated_at) VALUES (1, ?, ?, ?)
		ON CONFLICT (singleton) DO UPDATE SET session_id = excluded.session_id, seq = excluded.seq, updated_at = excluded.updated_at`,
		sess.ID, sess.Seq, sess.UpdatedAt.UnixMilli())
}

// ClearSession forgets the gateway session, which can no longer be resumed.
func (s *Store) ClearSession(ctx context.Context) error {
	return s.exec(ctx, "clear session", "DELETE FROM session")
}

// InboxRow is one persisted dispatch.
type InboxRow struct {
	// ID orders rows by arrival.
	ID int64
	// Payload is the dispatch as QQ sent it.
	Payload []byte
	// ReceivedAt is when the row was persisted.
	ReceivedAt time.Time
}

// Receive persists a dispatch unless one with the same dedup key exists, and
// in the same transaction takes its sequence number as the session's last.
// It reports whether a row was inserted.
func (s *Store) Receive(ctx context.Context, dedupKey string, payload []byte, sess Session) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("receive dispatch: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, "INSERT INTO inbox (dedup_key, payload, received_at) VALUES (?, ?, ?) ON CONFLICT (dedup_key) DO NOTHING",
		dedupKey, payload, sess.UpdatedAt.UnixMilli())
	if err != nil {
		return false, fmt.Errorf("receive dispatch: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("receive dispatch: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO session (singleton, session_id, seq, updated_at) VALUES (1, ?, ?, ?)
		ON CONFLICT (singleton) DO UPDATE SET session_id = excluded.session_id, seq = excluded.seq, updated_at = excluded.updated_at`,
		sess.ID, sess.Seq, sess.UpdatedAt.UnixMilli()); err != nil {
		return false, fmt.Errorf("receive dispatch: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("receive dispatch: %w", err)
	}
	return n == 1, nil
}

// PendingInbox returns up to limit pending rows with ID greater than after, in order.
func (s *Store) PendingInbox(ctx context.Context, after int64, limit int) ([]InboxRow, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, payload, received_at FROM inbox WHERE state = 'pending' AND id > ? ORDER BY id LIMIT ?", after, limit)
	if err != nil {
		return nil, fmt.Errorf("list pending inbox: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []InboxRow
	for rows.Next() {
		var row InboxRow
		var received int64
		if err := rows.Scan(&row.ID, &row.Payload, &received); err != nil {
			return nil, fmt.Errorf("list pending inbox: %w", err)
		}
		row.ReceivedAt = time.UnixMilli(received).UTC()
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list pending inbox: %w", err)
	}
	return out, nil
}

// MarkInboxDone marks a row processed.
func (s *Store) MarkInboxDone(ctx context.Context, id int64, at time.Time) error {
	return s.exec(ctx, "mark inbox done", "UPDATE inbox SET state = 'done', finished_at = ? WHERE id = ?", at.UnixMilli(), id)
}

// MarkInboxFailed marks a row that cannot be processed, keeping the reason.
func (s *Store) MarkInboxFailed(ctx context.Context, id int64, reason string, at time.Time) error {
	return s.exec(ctx, "mark inbox failed", "UPDATE inbox SET state = 'failed', error = ?, finished_at = ? WHERE id = ?", reason, at.UnixMilli(), id)
}

// PutRef records the message a REFIDX names.
func (s *Store) PutRef(ctx context.Context, refIdx, messageID, conversation string, at time.Time) error {
	return s.exec(ctx, "put ref", "INSERT INTO refs (ref_idx, message_id, conversation, created_at) VALUES (?, ?, ?, ?) ON CONFLICT (ref_idx) DO NOTHING",
		refIdx, messageID, conversation, at.UnixMilli())
}

// FindRef returns the message ID a REFIDX names.
func (s *Store) FindRef(ctx context.Context, refIdx string) (string, bool, error) {
	var id string
	err := s.db.QueryRowContext(ctx, "SELECT message_id FROM refs WHERE ref_idx = ?", refIdx).Scan(&id)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", false, nil
	case err != nil:
		return "", false, fmt.Errorf("find ref: %w", err)
	}
	return id, true, nil
}

// FindRefIdx returns the REFIDX of a message, which a quote names.
func (s *Store) FindRefIdx(ctx context.Context, messageID string) (string, bool, error) {
	var idx string
	err := s.db.QueryRowContext(ctx, "SELECT ref_idx FROM refs WHERE message_id = ? LIMIT 1", messageID).Scan(&idx)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", false, nil
	case err != nil:
		return "", false, fmt.Errorf("find ref idx: %w", err)
	}
	return idx, true, nil
}

// InsertInbox persists a payload the driver made itself, such as the event
// for a message it sent, unless one with the same dedup key exists.
func (s *Store) InsertInbox(ctx context.Context, dedupKey string, payload []byte, at time.Time) error {
	return s.exec(ctx, "insert inbox row", "INSERT INTO inbox (dedup_key, payload, received_at) VALUES (?, ?, ?) ON CONFLICT (dedup_key) DO NOTHING",
		dedupKey, payload, at.UnixMilli())
}

// PutOrigin records the QQ message behind a submitted event.
func (s *Store) PutOrigin(ctx context.Context, eventID, messageID, conversation string, at time.Time) error {
	return s.exec(ctx, "put origin", "INSERT INTO origins (event_id, message_id, conversation, received_at) VALUES (?, ?, ?, ?) ON CONFLICT (event_id) DO NOTHING",
		eventID, messageID, conversation, at.UnixMilli())
}

// Send is a claimed send.
type Send struct {
	Key, Conversation string
	// MsgID and MsgSeq make it a passive reply; MsgID is empty for a
	// proactive send.
	MsgID  string
	MsgSeq int
	// MessageID is the message QQ created, once sent.
	MessageID string
	Sent      bool
}

// Window is how long, and how many times, a message may be replied to
// passively.
type Window struct {
	For     time.Duration
	Replies int
}

// ClaimSend returns the send for key, claiming it if it is new: as a passive
// reply to the message behind origin when that message is in conversation,
// still inside window, and has replies left, and as a proactive send
// otherwise. existed reports a send claimed before, by an earlier attempt.
func (s *Store) ClaimSend(ctx context.Context, key, conversation, origin string, window Window, now time.Time) (send Send, existed bool, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Send{}, false, fmt.Errorf("claim send: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	send = Send{Key: key}
	var state string
	err = tx.QueryRowContext(ctx, "SELECT conversation, msg_id, msg_seq, message_id, state FROM sends WHERE key = ?", key).
		Scan(&send.Conversation, &send.MsgID, &send.MsgSeq, &send.MessageID, &state)
	switch {
	case err == nil:
		send.Sent = state == "sent"
		return send, true, nil
	case !errors.Is(err, sql.ErrNoRows):
		return Send{}, false, fmt.Errorf("claim send: %w", err)
	}
	send.Conversation = conversation
	var messageID string
	var replies int
	err = tx.QueryRowContext(ctx, "SELECT message_id, replies FROM origins WHERE event_id = ? AND conversation = ? AND received_at > ?",
		origin, conversation, now.Add(-window.For).UnixMilli()).Scan(&messageID, &replies)
	switch {
	case err == nil && replies < window.Replies:
		send.MsgID, send.MsgSeq = messageID, replies+1
		if _, err := tx.ExecContext(ctx, "UPDATE origins SET replies = replies + 1 WHERE event_id = ?", origin); err != nil {
			return Send{}, false, fmt.Errorf("claim send: %w", err)
		}
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return Send{}, false, fmt.Errorf("claim send: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO sends (key, conversation, msg_id, msg_seq, state, created_at) VALUES (?, ?, ?, ?, 'pending', ?)",
		key, conversation, send.MsgID, send.MsgSeq, now.UnixMilli()); err != nil {
		return Send{}, false, fmt.Errorf("claim send: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Send{}, false, fmt.Errorf("claim send: %w", err)
	}
	return send, false, nil
}

// MakeProactive turns a claimed send into a proactive one, after QQ refused
// it as a passive reply.
func (s *Store) MakeProactive(ctx context.Context, key string) error {
	return s.exec(ctx, "make send proactive", "UPDATE sends SET msg_id = '', msg_seq = 0 WHERE key = ?", key)
}

// CompleteSend records the message a send created.
func (s *Store) CompleteSend(ctx context.Context, key, messageID string) error {
	return s.exec(ctx, "complete send", "UPDATE sends SET state = 'sent', message_id = ? WHERE key = ?", messageID, key)
}

// ReleaseSend forgets a send QQ refused, so a later attempt may claim it
// afresh.
func (s *Store) ReleaseSend(ctx context.Context, key string) error {
	return s.exec(ctx, "release send", "DELETE FROM sends WHERE key = ? AND state = 'pending'", key)
}

// RecordGap records that events may have been lost.
func (s *Store) RecordGap(ctx context.Context, kind, detail string, at time.Time) error {
	return s.exec(ctx, "record gap", "INSERT INTO gaps (kind, detail, detected_at) VALUES (?, ?, ?)", kind, detail, at.UnixMilli())
}

// Prune deletes finished inbox rows and old references.
func (s *Store) Prune(ctx context.Context, now time.Time) error {
	return errors.Join(
		s.exec(ctx, "prune inbox", "DELETE FROM inbox WHERE state != 'pending' AND finished_at < ?", now.Add(-InboxRetention).UnixMilli()),
		s.exec(ctx, "prune refs", "DELETE FROM refs WHERE created_at < ?", now.Add(-RefRetention).UnixMilli()),
		s.exec(ctx, "prune origins", "DELETE FROM origins WHERE received_at < ?", now.Add(-SendRetention).UnixMilli()),
		s.exec(ctx, "prune sends", "DELETE FROM sends WHERE created_at < ?", now.Add(-SendRetention).UnixMilli()),
	)
}

// Counts are the inbox and gap totals shown in status.
type Counts struct {
	PendingInbox int `json:"pending_inbox"`
	FailedInbox  int `json:"failed_inbox"`
	Gaps         int `json:"gaps"`
}

// Counts returns the inbox and gap totals.
func (s *Store) Counts(ctx context.Context) (Counts, error) {
	var c Counts
	err := s.db.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM inbox WHERE state = 'pending'),
		(SELECT COUNT(*) FROM inbox WHERE state = 'failed'),
		(SELECT COUNT(*) FROM gaps)`).Scan(&c.PendingInbox, &c.FailedInbox, &c.Gaps)
	if err != nil {
		return Counts{}, fmt.Errorf("count records: %w", err)
	}
	return c, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) exec(ctx context.Context, op, query string, args ...any) error {
	if _, err := s.db.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	return nil
}
