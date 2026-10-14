package modulestore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// SetTimer persists a timer, replacing one with the same key.
func (s *Storage) SetTimer(ctx context.Context, module otogi.ModuleID, key string, at time.Time, payload []byte) error {
	if payload == nil {
		payload = []byte{}
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO timers (module, key, at, attempt_at, payload) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (module, key) DO UPDATE SET at = excluded.at, attempt_at = excluded.attempt_at, payload = excluded.payload`,
		module, key, at.UnixMilli(), at.UnixMilli(), payload)
	if err != nil {
		return fmt.Errorf("module store: set timer %q: %w", key, err)
	}
	return nil
}

// CancelTimer removes a timer.
func (s *Storage) CancelTimer(ctx context.Context, module otogi.ModuleID, key string) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM timers WHERE module = ? AND key = ?", module, key); err != nil {
		return fmt.Errorf("module store: cancel timer %q: %w", key, err)
	}
	return nil
}

// DueTimers calls fn for every timer of modules whose next attempt is at or
// before now. Timers of other modules (for example, disabled ones) stay put.
func (s *Storage) DueTimers(ctx context.Context, now time.Time, modules []otogi.ModuleID, fn func(otogi.ModuleID, otogi.Timer)) error {
	if len(modules) == 0 {
		return nil
	}
	args := []any{now.UnixMilli()}
	for _, m := range modules {
		args = append(args, m)
	}
	query := "SELECT module, key, at, payload FROM timers WHERE attempt_at <= ? AND module IN (?" + strings.Repeat(", ?", len(modules)-1) + ") ORDER BY attempt_at"
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("module store: due timers: %w", err)
	}
	type due struct {
		module otogi.ModuleID
		timer  otogi.Timer
	}
	var timers []due
	for rows.Next() {
		var d due
		var at int64
		if err := rows.Scan(&d.module, &d.timer.Key, &at, &d.timer.Payload); err != nil {
			_ = rows.Close()
			return fmt.Errorf("module store: due timers: %w", err)
		}
		d.timer.At = time.UnixMilli(at).UTC()
		timers = append(timers, d)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return fmt.Errorf("module store: due timers: %w", err)
	}
	for _, d := range timers {
		fn(d.module, d.timer)
	}
	return nil
}

// FinishTimer deletes a timer after a successful run, unless it was replaced
// with a new due time meanwhile.
func (s *Storage) FinishTimer(ctx context.Context, module otogi.ModuleID, key string, at time.Time) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM timers WHERE module = ? AND key = ? AND at = ?", module, key, at.UnixMilli()); err != nil {
		return fmt.Errorf("module store: finish timer %q: %w", key, err)
	}
	return nil
}

// RetryTimer postpones the next attempt of a failed run, keeping its due time.
func (s *Storage) RetryTimer(ctx context.Context, module otogi.ModuleID, key string, at, next time.Time) error {
	_, err := s.db.ExecContext(ctx, "UPDATE timers SET attempt_at = ? WHERE module = ? AND key = ? AND at = ?", next.UnixMilli(), module, key, at.UnixMilli())
	if err != nil {
		return fmt.Errorf("module store: retry timer %q: %w", key, err)
	}
	return nil
}
