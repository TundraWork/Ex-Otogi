package store

import (
	"context"
	"fmt"
	"time"
)

// InboxRow is one persisted raw update.
type InboxRow struct {
	// ID orders rows by arrival.
	ID int64
	// Payload is the TL-encoded update.
	Payload []byte
	// ReceivedAt is when the row was persisted.
	ReceivedAt time.Time
}

// InsertInbox persists a raw update unless one with the same dedup key exists.
// It reports whether a row was inserted.
func (s *Store) InsertInbox(ctx context.Context, dedupKey string, payload []byte, at time.Time) (bool, error) {
	result, err := s.db.ExecContext(ctx, "INSERT INTO inbox (dedup_key, payload, received_at) VALUES (?, ?, ?) ON CONFLICT (dedup_key) DO NOTHING",
		dedupKey, payload, at.UnixMilli())
	if err != nil {
		return false, fmt.Errorf("insert inbox row: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("insert inbox row: %w", err)
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
