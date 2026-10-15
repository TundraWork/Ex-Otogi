package todou

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/pressly/goose/v3"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

//go:embed migrations/*.sql
var migrations embed.FS

// store is the module's private database: cursor, links, queue, and outbox.
type store struct{ db *sql.DB }

func openStore(ctx context.Context, db *sql.DB) (*store, error) {
	sub, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return nil, fmt.Errorf("todou store: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, sub)
	if err != nil {
		return nil, fmt.Errorf("todou store: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return nil, fmt.Errorf("todou store: migrate: %w", err)
	}
	return &store{db: db}, nil
}

// outboxRow is one rendered digest message for one conversation.
type outboxRow struct {
	ID       int64
	To       otogi.Destination
	Message  otogi.Message
	Attempts int
}

// queued is an entry waiting for its conversation's next digest.
type queued struct {
	ID    int64
	To    otogi.Destination
	Entry entry
}

func (s *store) cursor(ctx context.Context) (string, error) {
	var cursor string
	err := s.db.QueryRowContext(ctx, "SELECT cursor FROM state").Scan(&cursor)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("todou store: read cursor: %w", err)
	}
	return cursor, nil
}

// ingest records a watch batch: its entries, queued for each linked
// conversation, and the cursor after them, in one transaction, so a crash
// either replays the batch or keeps it, never neither and never twice.
func (s *store) ingest(ctx context.Context, cursor string, entries []queued, now time.Time) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		for _, q := range entries {
			if _, err := tx.ExecContext(ctx, "INSERT INTO queue (platform, account, conversation, project, entry, created_at) VALUES (?, ?, ?, ?, ?, ?)",
				q.To.Platform, q.To.Account, q.To.Conversation, q.Entry.Project, q.Entry.raw, now.UnixMilli()); err != nil {
				return fmt.Errorf("todou store: queue entry: %w", err)
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO state (singleton, cursor, batch_at) VALUES (1, ?, ?)
			ON CONFLICT (singleton) DO UPDATE SET cursor = excluded.cursor, batch_at = excluded.batch_at`,
			cursor, now.UnixMilli()); err != nil {
			return fmt.Errorf("todou store: save cursor: %w", err)
		}
		return nil
	})
}

// backlog lists the conversations with queued entries or undelivered rows.
func (s *store) backlog(ctx context.Context) ([]otogi.Destination, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT platform, account, conversation FROM queue
		UNION SELECT platform, account, conversation FROM outbox WHERE sent_at IS NULL AND failed_at IS NULL`)
	if err != nil {
		return nil, fmt.Errorf("todou store: backlog: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []otogi.Destination
	for rows.Next() {
		var to otogi.Destination
		if err := rows.Scan(&to.Platform, &to.Account, &to.Conversation); err != nil {
			return nil, fmt.Errorf("todou store: backlog: %w", err)
		}
		out = append(out, to)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("todou store: backlog: %w", err)
	}
	return out, nil
}

// queue returns the entries queued for a conversation, oldest first.
func (s *store) queue(ctx context.Context, to otogi.Destination) ([]queued, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, entry FROM queue WHERE platform = ? AND account = ? AND conversation = ? ORDER BY id",
		to.Platform, to.Account, to.Conversation)
	if err != nil {
		return nil, fmt.Errorf("todou store: queue: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []queued
	for rows.Next() {
		q := queued{To: to}
		var raw string
		if err := rows.Scan(&q.ID, &raw); err != nil {
			return nil, fmt.Errorf("todou store: queue: %w", err)
		}
		if q.Entry, err = decodeEntry(raw); err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("todou store: queue: %w", err)
	}
	return out, nil
}

// lastSent is when a digest was last delivered to a conversation, or zero.
func (s *store) lastSent(ctx context.Context, to otogi.Destination) (time.Time, error) {
	var at sql.NullInt64
	err := s.db.QueryRowContext(ctx, "SELECT MAX(sent_at) FROM outbox WHERE platform = ? AND account = ? AND conversation = ?",
		to.Platform, to.Account, to.Conversation).Scan(&at)
	if err != nil {
		return time.Time{}, fmt.Errorf("todou store: last sent: %w", err)
	}
	if !at.Valid {
		return time.Time{}, nil
	}
	return time.UnixMilli(at.Int64), nil
}

// digest turns a conversation's queued entries up to and including id upTo
// into outbox rows, in one transaction.
func (s *store) digest(ctx context.Context, to otogi.Destination, upTo int64, messages []otogi.Message, now time.Time) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		for _, m := range messages {
			message, err := json.Marshal(m)
			if err != nil {
				return fmt.Errorf("todou store: encode message: %w", err)
			}
			if _, err := tx.ExecContext(ctx, "INSERT INTO outbox (platform, account, conversation, message, created_at) VALUES (?, ?, ?, ?, ?)",
				to.Platform, to.Account, to.Conversation, string(message), now.UnixMilli()); err != nil {
				return fmt.Errorf("todou store: insert outbox: %w", err)
			}
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM queue WHERE platform = ? AND account = ? AND conversation = ? AND id <= ?",
			to.Platform, to.Account, to.Conversation, upTo); err != nil {
			return fmt.Errorf("todou store: dequeue: %w", err)
		}
		return nil
	})
}

// pending returns a conversation's undelivered outbox rows, oldest first.
func (s *store) pending(ctx context.Context, to otogi.Destination) ([]outboxRow, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, message, attempts FROM outbox WHERE platform = ? AND account = ? AND conversation = ? AND sent_at IS NULL AND failed_at IS NULL ORDER BY id",
		to.Platform, to.Account, to.Conversation)
	if err != nil {
		return nil, fmt.Errorf("todou store: pending: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []outboxRow
	for rows.Next() {
		r := outboxRow{To: to}
		var message string
		if err := rows.Scan(&r.ID, &message, &r.Attempts); err != nil {
			return nil, fmt.Errorf("todou store: pending: %w", err)
		}
		if err := json.Unmarshal([]byte(message), &r.Message); err != nil {
			return nil, fmt.Errorf("todou store: decode outbox %d: %w", r.ID, err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("todou store: pending: %w", err)
	}
	return out, nil
}

func (s *store) markSent(ctx context.Context, id int64, now time.Time) error {
	if _, err := s.db.ExecContext(ctx, "UPDATE outbox SET sent_at = ? WHERE id = ?", now.UnixMilli(), id); err != nil {
		return fmt.Errorf("todou store: mark sent: %w", err)
	}
	return nil
}

// attemptFailed counts a failed delivery and reports whether the row has now
// used up maxAttempts and is marked failed.
func (s *store) attemptFailed(ctx context.Context, id int64, cause error, maxAttempts int, now time.Time) (bool, error) {
	var attempts int
	err := s.db.QueryRowContext(ctx, "UPDATE outbox SET attempts = attempts + 1, last_error = ? WHERE id = ? RETURNING attempts", cause.Error(), id).Scan(&attempts)
	if err != nil {
		return false, fmt.Errorf("todou store: record attempt: %w", err)
	}
	if attempts < maxAttempts {
		return false, nil
	}
	if _, err := s.db.ExecContext(ctx, "UPDATE outbox SET failed_at = ? WHERE id = ?", now.UnixMilli(), id); err != nil {
		return true, fmt.Errorf("todou store: mark failed: %w", err)
	}
	return true, nil
}

func (s *store) tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("todou store: begin: %w", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("todou store: commit: %w", err)
	}
	return nil
}

// link is a project linked to a conversation.
type link struct {
	To      otogi.Destination
	Project string
}

func (s *store) addLink(ctx context.Context, l link, by string, now time.Time) (bool, error) {
	result, err := s.db.ExecContext(ctx, "INSERT INTO links (platform, account, conversation, project, created_by, created_at) VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT DO NOTHING",
		l.To.Platform, l.To.Account, l.To.Conversation, l.Project, by, now.UnixMilli())
	if err != nil {
		return false, fmt.Errorf("todou store: add link: %w", err)
	}
	n, _ := result.RowsAffected()
	return n == 1, nil
}

// removeLink removes a link and the entries of its project still queued for
// the conversation.
func (s *store) removeLink(ctx context.Context, l link) (removed bool, err error) {
	err = s.tx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, "DELETE FROM links WHERE platform = ? AND account = ? AND conversation = ? AND project = ?",
			l.To.Platform, l.To.Account, l.To.Conversation, l.Project)
		if err != nil {
			return fmt.Errorf("todou store: remove link: %w", err)
		}
		n, _ := result.RowsAffected()
		removed = n == 1
		if _, err := tx.ExecContext(ctx, "DELETE FROM queue WHERE platform = ? AND account = ? AND conversation = ? AND project = ?",
			l.To.Platform, l.To.Account, l.To.Conversation, l.Project); err != nil {
			return fmt.Errorf("todou store: remove link: %w", err)
		}
		return nil
	})
	return removed, err
}

// links returns every link, or those of one conversation when to is set.
func (s *store) links(ctx context.Context, to *otogi.Destination) ([]link, error) {
	query, args := "SELECT platform, account, conversation, project FROM links ORDER BY project, created_at", []any{}
	if to != nil {
		query = "SELECT platform, account, conversation, project FROM links WHERE platform = ? AND account = ? AND conversation = ? ORDER BY project"
		args = []any{to.Platform, to.Account, to.Conversation}
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("todou store: links: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []link
	for rows.Next() {
		var l link
		if err := rows.Scan(&l.To.Platform, &l.To.Account, &l.To.Conversation, &l.Project); err != nil {
			return nil, fmt.Errorf("todou store: links: %w", err)
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("todou store: links: %w", err)
	}
	return out, nil
}

// summary is what /todou status shows: the watcher's position and one
// conversation's delivery.
type summary struct {
	HasCursor        bool
	BatchAt          time.Time
	Queued, Pending  int
	Failed           int
	LastSent         time.Time
	LastFailureError string
}

func (s *store) summary(ctx context.Context, to otogi.Destination) (summary, error) {
	var sum summary
	var batch int64
	err := s.db.QueryRowContext(ctx, "SELECT batch_at FROM state").Scan(&batch)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return summary{}, fmt.Errorf("todou store: summary: %w", err)
	default:
		sum.HasCursor, sum.BatchAt = true, time.UnixMilli(batch)
	}
	var sent sql.NullInt64
	chat := []any{to.Platform, to.Account, to.Conversation}
	err = s.db.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM queue WHERE platform = ?1 AND account = ?2 AND conversation = ?3),
		(SELECT COUNT(*) FROM outbox WHERE platform = ?1 AND account = ?2 AND conversation = ?3 AND sent_at IS NULL AND failed_at IS NULL),
		(SELECT COUNT(*) FROM outbox WHERE platform = ?1 AND account = ?2 AND conversation = ?3 AND failed_at IS NOT NULL),
		(SELECT MAX(sent_at) FROM outbox WHERE platform = ?1 AND account = ?2 AND conversation = ?3),
		COALESCE((SELECT last_error FROM outbox WHERE platform = ?1 AND account = ?2 AND conversation = ?3 AND failed_at IS NOT NULL ORDER BY failed_at DESC LIMIT 1), '')`, chat...).
		Scan(&sum.Queued, &sum.Pending, &sum.Failed, &sent, &sum.LastFailureError)
	if err != nil {
		return summary{}, fmt.Errorf("todou store: summary: %w", err)
	}
	if sent.Valid {
		sum.LastSent = time.UnixMilli(sent.Int64)
	}
	return sum, nil
}

// prune deletes delivered outbox rows older than retention.
func (s *store) prune(ctx context.Context, before time.Time) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM outbox WHERE sent_at IS NOT NULL AND sent_at < ?", before.UnixMilli()); err != nil {
		return fmt.Errorf("todou store: prune: %w", err)
	}
	return nil
}
