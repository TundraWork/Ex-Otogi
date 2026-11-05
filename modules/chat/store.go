package chat

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"time"

	"github.com/pressly/goose/v3"

	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

//go:embed migrations/*.sql
var migrations embed.FS

// store is the module's private database: the transcript, threads, and the
// messages agents sent.
type store struct{ db *sql.DB }

func openStore(ctx context.Context, db *sql.DB) (*store, error) {
	sub, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return nil, fmt.Errorf("chat store: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, sub)
	if err != nil {
		return nil, fmt.Errorf("chat store: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return nil, fmt.Errorf("chat store: migrate: %w", err)
	}
	return &store{db: db}, nil
}

// line is one transcript message.
type line struct {
	SourceID  string
	At        time.Time
	ActorID   string
	ActorName string
	Outgoing  bool
	Text      string
	Media     []otogi.Media
	ReplyTo   string
}

// record keeps a created or edited message.
func (s *store) record(ctx context.Context, to otogi.Destination, l line) error {
	media, err := json.Marshal(l.Media)
	if err != nil {
		return fmt.Errorf("chat store: encode media: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO transcript (platform, account, conversation, source_id, at, actor_id, actor_name, outgoing, text, media, reply_to)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (platform, account, conversation, source_id) DO UPDATE SET text = excluded.text, media = excluded.media`,
		to.Platform, to.Account, to.Conversation, l.SourceID, l.At.UnixMilli(), l.ActorID, l.ActorName, l.Outgoing, l.Text, string(media), l.ReplyTo)
	if err != nil {
		return fmt.Errorf("chat store: record message: %w", err)
	}
	return nil
}

// forget marks a deleted message.
func (s *store) forget(ctx context.Context, platform otogi.PlatformID, account otogi.AccountID, sourceID string) error {
	if _, err := s.db.ExecContext(ctx, "UPDATE transcript SET deleted = 1 WHERE platform = ? AND account = ? AND source_id = ?", platform, account, sourceID); err != nil {
		return fmt.Errorf("chat store: forget message: %w", err)
	}
	return nil
}

// message returns a transcript message, if it is kept and not deleted.
func (s *store) message(ctx context.Context, to otogi.Destination, sourceID string) (line, bool, error) {
	lines, err := s.lines(ctx, "SELECT source_id, at, actor_id, actor_name, outgoing, text, media, reply_to FROM transcript WHERE platform = ? AND account = ? AND conversation = ? AND source_id = ? AND deleted = 0",
		to.Platform, to.Account, to.Conversation, sourceID)
	if err != nil || len(lines) == 0 {
		return line{}, false, err
	}
	return lines[0], true, nil
}

// recent returns up to n messages of the conversation before the given
// time, oldest first.
func (s *store) recent(ctx context.Context, to otogi.Destination, before time.Time, n int) ([]line, error) {
	lines, err := s.lines(ctx, `SELECT source_id, at, actor_id, actor_name, outgoing, text, media, reply_to FROM transcript
		WHERE platform = ? AND account = ? AND conversation = ? AND at < ? AND deleted = 0 ORDER BY at DESC LIMIT ?`,
		to.Platform, to.Account, to.Conversation, before.UnixMilli(), n)
	slices.Reverse(lines)
	return lines, err
}

func (s *store) lines(ctx context.Context, query string, args ...any) ([]line, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("chat store: read transcript: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []line
	for rows.Next() {
		var l line
		var at int64
		var media string
		if err := rows.Scan(&l.SourceID, &at, &l.ActorID, &l.ActorName, &l.Outgoing, &l.Text, &media, &l.ReplyTo); err != nil {
			return nil, fmt.Errorf("chat store: read transcript: %w", err)
		}
		l.At = time.UnixMilli(at)
		if err := json.Unmarshal([]byte(media), &l.Media); err != nil {
			return nil, fmt.Errorf("chat store: decode media: %w", err)
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("chat store: read transcript: %w", err)
	}
	return out, nil
}

// agentTurn finds the turn an agent message answers from, and its profile.
func (s *store) agentTurn(ctx context.Context, ref otogi.EventRef) (id int64, profile string, ok bool, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT t.id, t.profile FROM sent s JOIN turns t ON t.id = s.turn
		WHERE s.platform = ? AND s.account = ? AND s.conversation = ? AND s.source_id = ?`,
		ref.Platform, ref.Account, ref.Conversation, ref.SourceEvent).Scan(&id, &profile)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", false, nil
	}
	if err != nil {
		return 0, "", false, fmt.Errorf("chat store: find agent message: %w", err)
	}
	return id, profile, true, nil
}

// storedTurn is a thread turn with what the transcript knows it by.
type storedTurn struct {
	ID       int64
	Turn     llm.Turn
	SourceID string   // the user message, for user turns
	Sent     []string // the agent messages answering from it
}

// path returns the thread from its start to turn id, oldest first. A thread
// whose start was pruned begins at its oldest kept turn.
func (s *store) path(ctx context.Context, id int64) ([]storedTurn, error) {
	var out []storedTurn
	for id != 0 {
		var raw string
		var parent sql.NullInt64
		t := storedTurn{ID: id}
		err := s.db.QueryRowContext(ctx, "SELECT parent, turn, source_id FROM turns WHERE id = ?", id).Scan(&parent, &raw, &t.SourceID)
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("chat store: read turn %d: %w", id, err)
		}
		if err := json.Unmarshal([]byte(raw), &t.Turn); err != nil {
			return nil, fmt.Errorf("chat store: decode turn %d: %w", id, err)
		}
		rows, err := s.db.QueryContext(ctx, "SELECT source_id FROM sent WHERE turn = ?", id)
		if err != nil {
			return nil, fmt.Errorf("chat store: read sent: %w", err)
		}
		for rows.Next() {
			var source string
			if err := rows.Scan(&source); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("chat store: read sent: %w", err)
			}
			t.Sent = append(t.Sent, source)
		}
		_ = rows.Close()
		out = append(out, t)
		id = parent.Int64
	}
	slices.Reverse(out)
	return out, nil
}

// thread adds turns after parent (0 starts a thread) and maps the agent's
// sent messages to the last one, in one transaction. It returns the new
// turns' IDs.
func (s *store) thread(ctx context.Context, profile string, to otogi.Destination, parent int64, turns []storedTurn, sent []otogi.EventRef, now time.Time) ([]int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("chat store: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var ids []int64
	for _, t := range turns {
		raw, err := json.Marshal(t.Turn)
		if err != nil {
			return nil, fmt.Errorf("chat store: encode turn: %w", err)
		}
		var p any
		if parent != 0 {
			p = parent
		}
		res, err := tx.ExecContext(ctx, "INSERT INTO turns (profile, platform, account, conversation, parent, turn, source_id, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
			profile, to.Platform, to.Account, to.Conversation, p, string(raw), t.SourceID, now.UnixMilli())
		if err != nil {
			return nil, fmt.Errorf("chat store: add turn: %w", err)
		}
		if parent, err = res.LastInsertId(); err != nil {
			return nil, fmt.Errorf("chat store: add turn: %w", err)
		}
		ids = append(ids, parent)
	}
	for _, ref := range sent {
		if ref.SourceEvent == "" {
			continue // a send Telegram confirmed without naming the message
		}
		if _, err := tx.ExecContext(ctx, "INSERT OR REPLACE INTO sent (platform, account, conversation, source_id, turn) VALUES (?, ?, ?, ?, ?)",
			ref.Platform, ref.Account, ref.Conversation, ref.SourceEvent, parent); err != nil {
			return nil, fmt.Errorf("chat store: map sent message: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("chat store: %w", err)
	}
	return ids, nil
}

// prune deletes what is older than cutoff.
func (s *store) prune(ctx context.Context, cutoff time.Time) (int64, error) {
	var total int64
	for _, q := range []string{
		"DELETE FROM transcript WHERE at < ?",
		"DELETE FROM sent WHERE turn IN (SELECT id FROM turns WHERE created_at < ?)",
		"DELETE FROM turns WHERE created_at < ?",
	} {
		res, err := s.db.ExecContext(ctx, q, cutoff.UnixMilli())
		if err != nil {
			return total, fmt.Errorf("chat store: prune: %w", err)
		}
		n, _ := res.RowsAffected()
		total += n
	}
	return total, nil
}

// access returns where agents were allowed or denied with /chat.
func (s *store) access(ctx context.Context) (map[string]map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT profile, chat, allowed FROM access")
	if err != nil {
		return nil, fmt.Errorf("chat store: access: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]map[string]bool{}
	for rows.Next() {
		var profile, chat string
		var allowed bool
		if err := rows.Scan(&profile, &chat, &allowed); err != nil {
			return nil, fmt.Errorf("chat store: access: %w", err)
		}
		if out[profile] == nil {
			out[profile] = map[string]bool{}
		}
		out[profile][chat] = allowed
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("chat store: access: %w", err)
	}
	return out, nil
}

// setAccess records that an operator allowed or denied an agent in a chat.
func (s *store) setAccess(ctx context.Context, profile, chat string, allowed bool, by string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO access (profile, chat, allowed, by, at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (profile, chat) DO UPDATE SET allowed = excluded.allowed, by = excluded.by, at = excluded.at`,
		profile, chat, allowed, by, at.UnixMilli())
	if err != nil {
		return fmt.Errorf("chat store: set access: %w", err)
	}
	return nil
}
