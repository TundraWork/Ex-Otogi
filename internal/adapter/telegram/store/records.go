package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// PutLocator records which conversation a message ID belongs to in a box
// ("common" or a channel), so later deletions can be routed.
func (s *Store) PutLocator(ctx context.Context, box string, messageID int, conversation string, at time.Time) error {
	return s.exec(ctx, "put locator", `INSERT INTO locators (box, message_id, conversation, created_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (box, message_id) DO UPDATE SET conversation = excluded.conversation`, box, messageID, conversation, at.UnixMilli())
}

// FindLocator returns the conversation recorded for a message ID in a box.
func (s *Store) FindLocator(ctx context.Context, box string, messageID int) (string, bool, error) {
	var conversation string
	err := s.db.QueryRowContext(ctx, "SELECT conversation FROM locators WHERE box = ? AND message_id = ?", box, messageID).Scan(&conversation)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("find locator: %w", err)
	}
	return conversation, true, nil
}

// Sent is a message the adapter sent, recorded by idempotency key.
type Sent struct {
	// Key is the send's idempotency key.
	Key string
	// Conversation is the destination.
	Conversation string
	// MessageID is Telegram's ID for the sent message.
	MessageID int
}

// FindSent returns the recorded send for key.
func (s *Store) FindSent(ctx context.Context, key string) (Sent, bool, error) {
	sent := Sent{Key: key}
	err := s.db.QueryRowContext(ctx, "SELECT conversation, message_id FROM sends WHERE key = ?", key).Scan(&sent.Conversation, &sent.MessageID)
	if errors.Is(err, sql.ErrNoRows) {
		return Sent{}, false, nil
	}
	if err != nil {
		return Sent{}, false, fmt.Errorf("find sent message: %w", err)
	}
	return sent, true, nil
}

// PutSent records a completed send.
func (s *Store) PutSent(ctx context.Context, sent Sent, at time.Time) error {
	return s.exec(ctx, "put sent message", "INSERT INTO sends (key, conversation, message_id, sent_at) VALUES (?, ?, ?, ?) ON CONFLICT (key) DO NOTHING",
		sent.Key, sent.Conversation, sent.MessageID, at.UnixMilli())
}

// RecordGap records that Telegram could not provide missed updates. channelID
// is zero for the common box.
func (s *Store) RecordGap(ctx context.Context, kind string, channelID int64, at time.Time) error {
	return s.exec(ctx, "record gap", "INSERT INTO gaps (kind, channel_id, detected_at) VALUES (?, ?, ?)", kind, channelID, at.UnixMilli())
}

// Retention periods applied by Prune.
const (
	InboxRetention   = 7 * 24 * time.Hour
	LocatorRetention = 30 * 24 * time.Hour
	SentRetention    = 7 * 24 * time.Hour
)

// Prune deletes finished inbox rows, locators, and sent records older than
// their retention period. Pending inbox rows are never deleted.
func (s *Store) Prune(ctx context.Context, now time.Time) error {
	return errors.Join(
		s.exec(ctx, "prune inbox", "DELETE FROM inbox WHERE state != 'pending' AND finished_at < ?", now.Add(-InboxRetention).UnixMilli()),
		s.exec(ctx, "prune locators", "DELETE FROM locators WHERE created_at < ?", now.Add(-LocatorRetention).UnixMilli()),
		s.exec(ctx, "prune sends", "DELETE FROM sends WHERE sent_at < ?", now.Add(-SentRetention).UnixMilli()),
	)
}
