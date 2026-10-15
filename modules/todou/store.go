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

// store is the module's private database: cursor, links, and outbox.
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

// outboxRow is one rendered notification for one conversation.
type outboxRow struct {
	ID       int64
	To       otogi.Destination
	Message  otogi.Message
	Attempts int
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

// commit records a successful poll: its notifications and the cursor after
// them, in one transaction, so a crash either replays the poll or delivers
// the notifications, never neither and never twice.
func (s *store) commit(ctx context.Context, cursor string, rows []outboxRow, now time.Time) (err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("todou store: begin: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	for _, r := range rows {
		message, err := json.Marshal(r.Message)
		if err != nil {
			return fmt.Errorf("todou store: encode message: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO outbox (platform, account, conversation, message, created_at) VALUES (?, ?, ?, ?, ?)",
			r.To.Platform, r.To.Account, r.To.Conversation, string(message), now.UnixMilli()); err != nil {
			return fmt.Errorf("todou store: insert outbox: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO state (singleton, cursor, polled_at, poll_error) VALUES (1, ?, ?, '')
		ON CONFLICT (singleton) DO UPDATE SET cursor = excluded.cursor, polled_at = excluded.polled_at, poll_error = ''`,
		cursor, now.UnixMilli()); err != nil {
		return fmt.Errorf("todou store: save cursor: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("todou store: commit: %w", err)
	}
	return nil
}

// pollFailed records why the last poll failed, for /todou status.
func (s *store) pollFailed(ctx context.Context, cause error) error {
	_, err := s.db.ExecContext(ctx, "UPDATE state SET poll_error = ?", cause.Error())
	if err != nil {
		return fmt.Errorf("todou store: record poll error: %w", err)
	}
	return nil
}

func (s *store) pending(ctx context.Context) ([]outboxRow, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, platform, account, conversation, message, attempts FROM outbox WHERE sent_at IS NULL AND failed_at IS NULL ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("todou store: pending: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []outboxRow
	for rows.Next() {
		var r outboxRow
		var message string
		if err := rows.Scan(&r.ID, &r.To.Platform, &r.To.Account, &r.To.Conversation, &message, &r.Attempts); err != nil {
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

func (s *store) removeLink(ctx context.Context, l link) (bool, error) {
	result, err := s.db.ExecContext(ctx, "DELETE FROM links WHERE platform = ? AND account = ? AND conversation = ? AND project = ?",
		l.To.Platform, l.To.Account, l.To.Conversation, l.Project)
	if err != nil {
		return false, fmt.Errorf("todou store: remove link: %w", err)
	}
	n, _ := result.RowsAffected()
	return n == 1, nil
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

// summary is what /todou status shows about delivery.
type summary struct {
	HasCursor        bool
	PolledAt         time.Time
	PollError        string
	Pending, Failed  int
	LastFailureError string
}

func (s *store) summary(ctx context.Context) (summary, error) {
	var sum summary
	var polled int64
	err := s.db.QueryRowContext(ctx, "SELECT polled_at, poll_error FROM state").Scan(&polled, &sum.PollError)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return summary{}, fmt.Errorf("todou store: summary: %w", err)
	default:
		sum.HasCursor = true
		if polled > 0 {
			sum.PolledAt = time.UnixMilli(polled)
		}
	}
	err = s.db.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM outbox WHERE sent_at IS NULL AND failed_at IS NULL),
		(SELECT COUNT(*) FROM outbox WHERE failed_at IS NOT NULL),
		COALESCE((SELECT last_error FROM outbox WHERE failed_at IS NOT NULL ORDER BY failed_at DESC LIMIT 1), '')`).
		Scan(&sum.Pending, &sum.Failed, &sum.LastFailureError)
	if err != nil {
		return summary{}, fmt.Errorf("todou store: summary: %w", err)
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
