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

// store is the module's private database: each agent's cursor, links,
// queue, and outbox.
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
	Entry entry // with its agent
}

func (s *store) cursor(ctx context.Context, agent string) (string, error) {
	var cursor string
	err := s.db.QueryRowContext(ctx, "SELECT cursor FROM cursors WHERE agent = ?", agent).Scan(&cursor)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("todou store: read cursor: %w", err)
	}
	return cursor, nil
}

// ingest records a watch batch of an agent: its entries, queued for each
// linked conversation, and the cursor after them, in one transaction, so a
// crash either replays the batch or keeps it, never neither and never twice.
func (s *store) ingest(ctx context.Context, agent, cursor string, entries []queued, now time.Time) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		for _, q := range entries {
			if _, err := tx.ExecContext(ctx, "INSERT INTO queue (platform, account, conversation, project, agent, entry, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
				q.To.Platform, q.To.Account, q.To.Conversation, q.Entry.Project, agent, q.Entry.raw, now.UnixMilli()); err != nil {
				return fmt.Errorf("todou store: queue entry: %w", err)
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO cursors (agent, cursor, batch_at) VALUES (?, ?, ?)
			ON CONFLICT (agent) DO UPDATE SET cursor = excluded.cursor, batch_at = excluded.batch_at`,
			agent, cursor, now.UnixMilli()); err != nil {
			return fmt.Errorf("todou store: save cursor: %w", err)
		}
		return nil
	})
}

// unassigned counts the links, queued entries, and cursors from before
// agents, which name none.
func (s *store) unassigned(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM links WHERE agent = '') + (SELECT COUNT(*) FROM queue WHERE agent = '') +
		(SELECT COUNT(*) FROM cursors WHERE agent = '')`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("todou store: count rows without an agent: %w", err)
	}
	return n, nil
}

// adopt gives the rows from before agents to agent.
func (s *store) adopt(ctx context.Context, agent string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		for _, q := range []string{"UPDATE links SET agent = ? WHERE agent = ''", "UPDATE queue SET agent = ? WHERE agent = ''",
			"UPDATE OR IGNORE cursors SET agent = ? WHERE agent = ''", "DELETE FROM cursors WHERE agent = '' AND ? <> ''"} {
			if _, err := tx.ExecContext(ctx, q, agent); err != nil {
				return fmt.Errorf("todou store: assign rows to %s: %w", agent, err)
			}
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
	rows, err := s.db.QueryContext(ctx, "SELECT id, agent, entry FROM queue WHERE platform = ? AND account = ? AND conversation = ? ORDER BY id",
		to.Platform, to.Account, to.Conversation)
	if err != nil {
		return nil, fmt.Errorf("todou store: queue: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []queued
	for rows.Next() {
		q := queued{To: to}
		var agent, raw string
		if err := rows.Scan(&q.ID, &agent, &raw); err != nil {
			return nil, fmt.Errorf("todou store: queue: %w", err)
		}
		if q.Entry, err = decodeEntry(raw); err != nil {
			return nil, err
		}
		q.Entry.agent = agent
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

// cooldown returns a conversation's cooldown between digests, if one is set.
func (s *store) cooldown(ctx context.Context, to otogi.Destination) (time.Duration, bool, error) {
	var ms int64
	err := s.db.QueryRowContext(ctx, "SELECT cooldown_ms FROM cooldowns WHERE platform = ? AND account = ? AND conversation = ?",
		to.Platform, to.Account, to.Conversation).Scan(&ms)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return 0, false, nil
	case err != nil:
		return 0, false, fmt.Errorf("todou store: cooldown: %w", err)
	}
	return time.Duration(ms) * time.Millisecond, true, nil
}

// setCooldown sets a conversation's cooldown.
func (s *store) setCooldown(ctx context.Context, to otogi.Destination, d time.Duration, by string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO cooldowns (platform, account, conversation, cooldown_ms, set_by, set_at) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (platform, account, conversation) DO UPDATE SET cooldown_ms = excluded.cooldown_ms, set_by = excluded.set_by, set_at = excluded.set_at`,
		to.Platform, to.Account, to.Conversation, d.Milliseconds(), by, now.UnixMilli())
	if err != nil {
		return fmt.Errorf("todou store: set cooldown: %w", err)
	}
	return nil
}

// clearCooldown returns a conversation to the configured cooldown.
func (s *store) clearCooldown(ctx context.Context, to otogi.Destination) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM cooldowns WHERE platform = ? AND account = ? AND conversation = ?",
		to.Platform, to.Account, to.Conversation); err != nil {
		return fmt.Errorf("todou store: clear cooldown: %w", err)
	}
	return nil
}

// link is a project linked to a conversation through an agent. A silent
// link grants the chat access to the project without its activity.
type link struct {
	To      otogi.Destination
	Project string
	Agent   string
	Silent  bool
}

// setLink links a project to a conversation, replacing any link of the
// project there, and returns the link it replaced, if any.
func (s *store) setLink(ctx context.Context, l link, by string, now time.Time) (old *link, err error) {
	err = s.tx(ctx, func(tx *sql.Tx) error {
		prev := l
		err := tx.QueryRowContext(ctx, "SELECT agent, silent FROM links WHERE platform = ? AND account = ? AND conversation = ? AND project = ?",
			l.To.Platform, l.To.Account, l.To.Conversation, l.Project).Scan(&prev.Agent, &prev.Silent)
		switch {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return fmt.Errorf("todou store: read link: %w", err)
		default:
			old = &prev
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO links (platform, account, conversation, project, agent, silent, created_by, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (platform, account, conversation, project) DO UPDATE SET agent = excluded.agent, silent = excluded.silent,
			created_by = excluded.created_by, created_at = excluded.created_at`,
			l.To.Platform, l.To.Account, l.To.Conversation, l.Project, l.Agent, l.Silent, by, now.UnixMilli())
		if err != nil {
			return fmt.Errorf("todou store: set link: %w", err)
		}
		return nil
	})
	return old, err
}

// removeLink removes a link and the entries of its project still queued for
// the conversation.
func (s *store) removeLink(ctx context.Context, to otogi.Destination, project string) (removed bool, err error) {
	err = s.tx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, "DELETE FROM links WHERE platform = ? AND account = ? AND conversation = ? AND project = ?",
			to.Platform, to.Account, to.Conversation, project)
		if err != nil {
			return fmt.Errorf("todou store: remove link: %w", err)
		}
		n, _ := result.RowsAffected()
		removed = n == 1
		if _, err := tx.ExecContext(ctx, "DELETE FROM queue WHERE platform = ? AND account = ? AND conversation = ? AND project = ?",
			to.Platform, to.Account, to.Conversation, project); err != nil {
			return fmt.Errorf("todou store: remove link: %w", err)
		}
		return nil
	})
	return removed, err
}

// links returns every link, or those of one conversation when to is set.
func (s *store) links(ctx context.Context, to *otogi.Destination) ([]link, error) {
	query, args := "SELECT platform, account, conversation, project, agent, silent FROM links ORDER BY project, created_at", []any{}
	if to != nil {
		query = "SELECT platform, account, conversation, project, agent, silent FROM links WHERE platform = ? AND account = ? AND conversation = ? ORDER BY project"
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
		if err := rows.Scan(&l.To.Platform, &l.To.Account, &l.To.Conversation, &l.Project, &l.Agent, &l.Silent); err != nil {
			return nil, fmt.Errorf("todou store: links: %w", err)
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("todou store: links: %w", err)
	}
	return out, nil
}

// summary is what /todou status shows of one conversation's delivery.
type summary struct {
	Queued, Pending  int
	Failed           int
	LastSent         time.Time
	LastFailureError string
}

func (s *store) summary(ctx context.Context, to otogi.Destination) (summary, error) {
	var sum summary
	var sent sql.NullInt64
	chat := []any{to.Platform, to.Account, to.Conversation}
	err := s.db.QueryRowContext(ctx, `SELECT
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

// batchAt is when an agent's watcher last handed over a batch, or zero.
func (s *store) batchAt(ctx context.Context, agent string) (time.Time, error) {
	var at int64
	err := s.db.QueryRowContext(ctx, "SELECT batch_at FROM cursors WHERE agent = ?", agent).Scan(&at)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return time.Time{}, nil
	case err != nil:
		return time.Time{}, fmt.Errorf("todou store: last batch: %w", err)
	}
	return time.UnixMilli(at), nil
}

// prune deletes delivered outbox rows older than retention.
func (s *store) prune(ctx context.Context, before time.Time) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM outbox WHERE sent_at IS NOT NULL AND sent_at < ?", before.UnixMilli()); err != nil {
		return fmt.Errorf("todou store: prune: %w", err)
	}
	return nil
}
