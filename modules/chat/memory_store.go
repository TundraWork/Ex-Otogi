package chat

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// memoryEntry is one thing an agent remembers of a chat.
type memoryEntry struct {
	ID       int64
	Text     string
	People   map[string]string // the actor IDs it is about, to their names when noted
	Class    string            // what kind of thing it is: a name in memoryClasses
	HalfLife float64           // days for its clarity to halve, from when it was last reviewed
	Reviewed time.Time         // when it was noted, brought up again, or recalled last
	Created  time.Time
	Updated  time.Time
}

// memoryMark is how far an agent has taken a chat into its memory: the last
// message taken, and how many times in a row the batch after it was
// answered with nothing usable.
type memoryMark struct {
	At       time.Time
	SourceID string
	Unusable int
}

// markOf is the mark just past l.
func markOf(l line) memoryMark { return memoryMark{At: l.At, SourceID: l.SourceID} }

// before reports whether l comes before the mark, in transcript order.
func (k memoryMark) before(l line) bool {
	return k.At.Before(l.At) || k.At.Equal(l.At) && k.SourceID < l.SourceID
}

// book returns an agent's memory of a chat, oldest entry first.
func (s *store) book(ctx context.Context, profile string, to otogi.Destination) ([]memoryEntry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, text, people, class, half_life, reviewed_at, created_at, updated_at FROM memories
		WHERE profile = ? AND platform = ? AND account = ? AND conversation = ? ORDER BY id`,
		profile, to.Platform, to.Account, to.Conversation)
	if err != nil {
		return nil, fmt.Errorf("chat store: read memory: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []memoryEntry
	for rows.Next() {
		var e memoryEntry
		var people string
		var reviewed, created, updated int64
		if err := rows.Scan(&e.ID, &e.Text, &people, &e.Class, &e.HalfLife, &reviewed, &created, &updated); err != nil {
			return nil, fmt.Errorf("chat store: read memory: %w", err)
		}
		if err := json.Unmarshal([]byte(people), &e.People); err != nil {
			return nil, fmt.Errorf("chat store: decode memory people: %w", err)
		}
		e.Reviewed, e.Created, e.Updated = time.UnixMilli(reviewed), time.UnixMilli(created), time.UnixMilli(updated)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("chat store: read memory: %w", err)
	}
	return out, nil
}

// mark returns how far an agent has taken a chat into its memory; the zero
// mark when it has taken nothing.
func (s *store) mark(ctx context.Context, profile string, to otogi.Destination) (memoryMark, error) {
	var k memoryMark
	var at int64
	err := s.db.QueryRowContext(ctx, `SELECT at, source_id, unusable FROM memory_marks
		WHERE profile = ? AND platform = ? AND account = ? AND conversation = ?`,
		profile, to.Platform, to.Account, to.Conversation).Scan(&at, &k.SourceID, &k.Unusable)
	switch {
	case err == nil:
		k.At = time.UnixMilli(at)
		return k, nil
	case errors.Is(err, sql.ErrNoRows):
		return memoryMark{}, nil
	}
	return memoryMark{}, fmt.Errorf("chat store: read memory mark: %w", err)
}

// books returns the chats an agent remembers something of.
func (s *store) books(ctx context.Context, profile string) ([]otogi.Destination, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT DISTINCT platform, account, conversation FROM memories WHERE profile = ? ORDER BY platform, account, conversation", profile)
	if err != nil {
		return nil, fmt.Errorf("chat store: read memories: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []otogi.Destination
	for rows.Next() {
		var d otogi.Destination
		if err := rows.Scan(&d.Platform, &d.Account, &d.Conversation); err != nil {
			return nil, fmt.Errorf("chat store: read memories: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// chats returns the chats the transcript keeps.
func (s *store) chats(ctx context.Context) ([]otogi.Destination, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT DISTINCT platform, account, conversation FROM transcript ORDER BY platform, account, conversation")
	if err != nil {
		return nil, fmt.Errorf("chat store: read chats: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []otogi.Destination
	for rows.Next() {
		var d otogi.Destination
		if err := rows.Scan(&d.Platform, &d.Account, &d.Conversation); err != nil {
			return nil, fmt.Errorf("chat store: read chats: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// linesAfter returns up to n messages of the chat after the mark, oldest
// first, deleted ones left out.
func (s *store) linesAfter(ctx context.Context, to otogi.Destination, k memoryMark, n int) ([]line, error) {
	at := k.At.UnixMilli()
	if k.At.IsZero() {
		at = -1
	}
	return s.lines(ctx, lineColumns+`
		WHERE t.platform = ? AND t.account = ? AND t.conversation = ? AND t.deleted = 0 AND (t.at > ? OR t.at = ? AND t.source_id > ?)
		ORDER BY t.at, t.source_id LIMIT ?`,
		to.Platform, to.Account, to.Conversation, at, at, k.SourceID, n)
}

// linesBefore returns up to n messages of the chat before l, oldest first,
// deleted ones left out.
func (s *store) linesBefore(ctx context.Context, to otogi.Destination, l line, n int) ([]line, error) {
	lines, err := s.lines(ctx, lineColumns+`
		WHERE t.platform = ? AND t.account = ? AND t.conversation = ? AND t.deleted = 0 AND (t.at < ? OR t.at = ? AND t.source_id < ?)
		ORDER BY t.at DESC, t.source_id DESC LIMIT ?`,
		to.Platform, to.Account, to.Conversation, l.At.UnixMilli(), l.At.UnixMilli(), l.SourceID, n)
	slices.Reverse(lines)
	return lines, err
}

// newest returns the last n messages of the chat, oldest first, deleted
// ones left out.
func (s *store) newest(ctx context.Context, to otogi.Destination, n int) ([]line, error) {
	lines, err := s.lines(ctx, lineColumns+`
		WHERE t.platform = ? AND t.account = ? AND t.conversation = ? AND t.deleted = 0
		ORDER BY t.at DESC, t.source_id DESC LIMIT ?`,
		to.Platform, to.Account, to.Conversation, n)
	slices.Reverse(lines)
	return lines, err
}

// shownNames is the name each of actors last sent a message to the chat
// under; one with no message kept there is left out.
func (s *store) shownNames(ctx context.Context, to otogi.Destination, actors []string) (map[string]string, error) {
	out := make(map[string]string, len(actors))
	if len(actors) == 0 {
		return out, nil
	}
	args := []any{to.Platform, to.Account, to.Conversation}
	for _, a := range actors {
		args = append(args, a)
	}
	// SQLite takes the bare actor_name from the row MAX(at) picks.
	rows, err := s.db.QueryContext(ctx, `SELECT actor_id, actor_name, MAX(at) FROM transcript
		WHERE platform = ? AND account = ? AND conversation = ? AND outgoing = 0 AND deleted = 0
		AND actor_id IN (?`+strings.Repeat(", ?", len(actors)-1)+`) GROUP BY actor_id`, args...)
	if err != nil {
		return nil, fmt.Errorf("chat store: read names: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, name string
		var at int64
		if err := rows.Scan(&id, &name, &at); err != nil {
			return nil, fmt.Errorf("chat store: read names: %w", err)
		}
		out[id] = name
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("chat store: read names: %w", err)
	}
	return out, nil
}

// memoryChange is one change to a book: an entry to insert (ID 0), to
// replace, or, with only ID set, to delete.
type memoryChange struct {
	entry  memoryEntry
	delete bool
}

// keepMemory applies changes to an agent's memory of a chat and, when next
// is set, moves its mark there, in one transaction: a batch is taken
// whole or not at all.
func (s *store) keepMemory(ctx context.Context, profile string, to otogi.Destination, changes []memoryChange, next *memoryMark) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("chat store: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, c := range changes {
		e := c.entry
		people, err := json.Marshal(e.People)
		if err != nil {
			return fmt.Errorf("chat store: encode memory people: %w", err)
		}
		switch {
		case c.delete:
			_, err = tx.ExecContext(ctx, "DELETE FROM memories WHERE id = ?", e.ID)
		case e.ID == 0:
			_, err = tx.ExecContext(ctx, `INSERT INTO memories (profile, platform, account, conversation, text, people, class, half_life, reviewed_at, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				profile, to.Platform, to.Account, to.Conversation, e.Text, string(people), e.Class, e.HalfLife, e.Reviewed.UnixMilli(), e.Created.UnixMilli(), e.Updated.UnixMilli())
		default:
			_, err = tx.ExecContext(ctx, "UPDATE memories SET text = ?, people = ?, class = ?, half_life = ?, reviewed_at = ?, created_at = ?, updated_at = ? WHERE id = ?",
				e.Text, string(people), e.Class, e.HalfLife, e.Reviewed.UnixMilli(), e.Created.UnixMilli(), e.Updated.UnixMilli(), e.ID)
		}
		if err != nil {
			return fmt.Errorf("chat store: keep memory: %w", err)
		}
	}
	if next != nil {
		if err := setMark(ctx, tx, profile, to, *next); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("chat store: %w", err)
	}
	return nil
}

// reviewMemory reviews the entries ids at t with boost, in one transaction.
// It goes by their half-life and review as they are now, since a batch may
// have rewritten or merged them after they were read, and changes only
// those two columns. An entry forgotten or merged away since is skipped.
func (s *store) reviewMemory(ctx context.Context, ids []int64, t time.Time, boost float64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("chat store: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, id := range ids {
		var e memoryEntry
		var reviewed int64
		err := tx.QueryRowContext(ctx, "SELECT half_life, reviewed_at FROM memories WHERE id = ?", id).Scan(&e.HalfLife, &reviewed)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return fmt.Errorf("chat store: review memory: %w", err)
		}
		e.Reviewed = time.UnixMilli(reviewed)
		review(&e, t, boost)
		if _, err := tx.ExecContext(ctx, "UPDATE memories SET half_life = ?, reviewed_at = ? WHERE id = ?", e.HalfLife, e.Reviewed.UnixMilli(), id); err != nil {
			return fmt.Errorf("chat store: review memory: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("chat store: %w", err)
	}
	return nil
}

// setUnusable keeps how many times in a row the batch after the mark was
// answered with nothing usable.
func (s *store) setUnusable(ctx context.Context, profile string, to otogi.Destination, k memoryMark) error {
	return setMark(ctx, s.db, profile, to, k)
}

// execer runs a statement, in a transaction or not.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func setMark(ctx context.Context, db execer, profile string, to otogi.Destination, k memoryMark) error {
	_, err := db.ExecContext(ctx, `INSERT INTO memory_marks (profile, platform, account, conversation, at, source_id, unusable)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (profile, platform, account, conversation) DO UPDATE SET at = excluded.at, source_id = excluded.source_id, unusable = excluded.unusable`,
		profile, to.Platform, to.Account, to.Conversation, k.At.UnixMilli(), k.SourceID, k.Unusable)
	if err != nil {
		return fmt.Errorf("chat store: keep memory mark: %w", err)
	}
	return nil
}
