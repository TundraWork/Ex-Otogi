// Package store provides durable account-scoped Telegram SQLite storage.
package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Peer is a durable Telegram peer record.
type Peer struct {
	// Kind is the Telegram peer kind.
	Kind string
	// ID is the provider peer ID.
	ID int64
	// AccessHash is the provider access hash.
	AccessHash int64
	// Payload is bounded peer reconstruction data.
	Payload []byte
}

// JournalRow is a raw transport update packet.
type JournalRow struct {
	// ID is the monotonic receipt ID.
	ID int64
	// Payload is deterministic TL packet data.
	Payload []byte
	// ReceivedAt is the durable receipt time.
	ReceivedAt time.Time
	// NormalizedAt is set after normalized durable capture.
	NormalizedAt *time.Time
}

// InboxInput describes a normalized event for durable insertion.
type InboxInput struct {
	// ID is the stable runtime event ID.
	ID string
	// SourceKey is the stable provider deduplication key.
	SourceKey string
	// Conversation is the canonical conversation ID.
	Conversation string
	// Sequence orders events within the conversation.
	Sequence int64
	// Payload is normalized event data.
	Payload []byte
	// AvailableAt is the next dispatch eligibility time.
	AvailableAt time.Time
}

// InboxRow is a persisted normalized event and its dispatch state.
type InboxRow struct {
	// InboxInput contains immutable normalized event data.
	InboxInput
	// Status is the dispatch lifecycle state.
	Status string
	// Owner is the current claim token.
	Owner string
	// Error is the last bounded failure classification.
	Error string
	// LeaseUntil is the claim expiry time.
	LeaseUntil *time.Time
	// Attempts counts successful claims.
	Attempts int
	// CompletedAt is the terminal transition time.
	CompletedAt *time.Time
}

// QuarantineInput describes an unsupported raw update.
type QuarantineInput struct {
	// SourceKey is the stable provider deduplication key.
	SourceKey string
	// Kind identifies the unsupported update shape.
	Kind string
	// Payload is bounded deterministic TL data.
	Payload []byte
}

// Locator maps a provider message identity to conversation/refetch data.
type Locator struct {
	// Namespace is the provider message namespace.
	Namespace string
	// MessageID is the provider message ID.
	MessageID int64
	// Conversation is the canonical conversation ID.
	Conversation string
	// Payload is opaque refetch data.
	Payload []byte
}

// Capture is one atomic normalized-capture transaction.
type Capture struct {
	// JournalID is the source transport journal row.
	JournalID int64
	// Inbox is an optional normalized routed event.
	Inbox *InboxInput
	// Quarantine is an optional unsupported raw update.
	Quarantine *QuarantineInput
	// Locators are provider identities learned during mapping.
	Locators []Locator
	// Box identifies the common or channel consistency stream.
	Box string
	// Position is the last normalized source position.
	Position int64
}

// PutPeer upserts an account-scoped peer record.
func (s *Store) PutPeer(ctx context.Context, p Peer) error {
	_, e := s.db.ExecContext(ctx, "INSERT INTO peers(account_key,kind,peer_id,access_hash,payload) VALUES(?,?,?,?,?) ON CONFLICT(account_key,kind,peer_id) DO UPDATE SET access_hash=excluded.access_hash,payload=excluded.payload", s.accountKey, p.Kind, p.ID, p.AccessHash, p.Payload)
	return e
}

// GetPeer returns an account-scoped peer record.
func (s *Store) GetPeer(ctx context.Context, k string, id int64) (Peer, bool, error) {
	p := Peer{Kind: k, ID: id}
	e := s.db.QueryRowContext(ctx, "SELECT access_hash,payload FROM peers WHERE account_key=? AND kind=? AND peer_id=?", s.accountKey, k, id).Scan(&p.AccessHash, &p.Payload)
	if errors.Is(e, sql.ErrNoRows) {
		return p, false, nil
	}
	return p, e == nil, e
}

// AppendJournal durably appends a raw transport packet.
func (s *Store) AppendJournal(ctx context.Context, p []byte, at time.Time) (int64, error) {
	r, e := s.db.ExecContext(ctx, "INSERT INTO journal(account_key,payload,received_at) VALUES(?,?,?)", s.accountKey, p, at.UnixNano())
	if e != nil {
		return 0, e
	}
	return r.LastInsertId()
}

// StartupJournalIDs returns pending journal IDs in receipt order and detects overflow.
func (s *Store) StartupJournalIDs(ctx context.Context, limit int) ([]int64, error) {
	rows, e := s.db.QueryContext(ctx, "SELECT id FROM journal WHERE account_key=? AND normalized_at IS NULL ORDER BY id LIMIT ?", s.accountKey, limit+1)
	if e != nil {
		return nil, e
	}
	defer func() { _ = rows.Close() }()
	var ids []int64
	for rows.Next() {
		var id int64
		if e = rows.Scan(&id); e != nil {
			return nil, e
		}
		ids = append(ids, id)
	}
	if len(ids) > limit {
		return ids[:limit], ErrStartupOverflow
	}
	return ids, rows.Err()
}

// Journal returns a raw transport journal row.
func (s *Store) Journal(ctx context.Context, id int64) (JournalRow, bool, error) {
	var r JournalRow
	var received int64
	var norm sql.NullInt64
	e := s.db.QueryRowContext(ctx, "SELECT id,payload,received_at,normalized_at FROM journal WHERE account_key=? AND id=?", s.accountKey, id).Scan(&r.ID, &r.Payload, &received, &norm)
	if errors.Is(e, sql.ErrNoRows) {
		return r, false, nil
	}
	r.ReceivedAt = time.Unix(0, received).UTC()
	if norm.Valid {
		v := time.Unix(0, norm.Int64).UTC()
		r.NormalizedAt = &v
	}
	return r, e == nil, e
}

// MarkJournalNormalized records successful durable normalization.
func (s *Store) MarkJournalNormalized(ctx context.Context, id int64, at time.Time) error {
	_, e := s.db.ExecContext(ctx, "UPDATE journal SET normalized_at=? WHERE account_key=? AND id=?", at.UnixNano(), s.accountKey, id)
	return e
}

type sqlExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func insertInbox(ctx context.Context, x sqlExecer, a string, in InboxInput) (bool, error) {
	r, e := x.ExecContext(ctx, "INSERT INTO inbox(id,account_key,source_key,conversation,sequence,payload,available_at,status) VALUES(?,?,?,?,?,?,?,'pending') ON CONFLICT(account_key,source_key) DO NOTHING", in.ID, a, in.SourceKey, in.Conversation, in.Sequence, in.Payload, in.AvailableAt.UnixNano())
	if e != nil {
		return false, e
	}
	n, e := r.RowsAffected()
	return n == 1, e
}

// EnqueueInbox inserts a normalized event using stable source deduplication.
func (s *Store) EnqueueInbox(ctx context.Context, in InboxInput) (bool, error) {
	return insertInbox(ctx, s.db, s.accountKey, in)
}

// Capture atomically persists normalized capture data and progress.
func (s *Store) Capture(ctx context.Context, c Capture) (bool, error) {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return false, e
	}
	defer func() { _ = tx.Rollback() }()
	inserted := false
	if c.Inbox != nil {
		inserted, e = insertInbox(ctx, tx, s.accountKey, *c.Inbox)
		if e != nil {
			return false, e
		}
	}
	now := time.Now().UTC()
	if c.Quarantine != nil {
		_, e = tx.ExecContext(ctx, "INSERT INTO quarantine(account_key,source_key,kind,payload,created_at) VALUES(?,?,?,?,?) ON CONFLICT(account_key,source_key) DO NOTHING", s.accountKey, c.Quarantine.SourceKey, c.Quarantine.Kind, c.Quarantine.Payload, now.UnixNano())
		if e != nil {
			return false, e
		}
	}
	for _, l := range c.Locators {
		h := sha256.Sum256(l.Payload)
		_, e = tx.ExecContext(ctx, "INSERT INTO locators(account_key,namespace,message_id,conversation,payload,payload_hash) VALUES(?,?,?,?,?,?) ON CONFLICT(account_key,namespace,message_id) DO UPDATE SET conversation=excluded.conversation,payload=excluded.payload,payload_hash=excluded.payload_hash", s.accountKey, l.Namespace, l.MessageID, l.Conversation, l.Payload, h[:])
		if e != nil {
			return false, e
		}
	}
	if c.JournalID != 0 {
		var result sql.Result
		result, e = tx.ExecContext(ctx, "UPDATE journal SET normalized_at=? WHERE account_key=? AND id=?", now.UnixNano(), s.accountKey, c.JournalID)
		if e != nil {
			return false, e
		}
		var affected int64
		affected, e = result.RowsAffected()
		if e != nil {
			return false, e
		}
		if affected != 1 {
			return false, fmt.Errorf("capture journal %d does not belong to account", c.JournalID)
		}
	}
	if c.Box != "" {
		_, e = tx.ExecContext(ctx, "INSERT INTO consistency(account_key,box,position) VALUES(?,?,?) ON CONFLICT(account_key,box) DO UPDATE SET position=max(position,excluded.position)", s.accountKey, c.Box, c.Position)
		if e != nil {
			return false, e
		}
	}
	return inserted, tx.Commit()
}

// GetLocator resolves an account-scoped provider message locator.
func (s *Store) GetLocator(ctx context.Context, n string, id int64) (Locator, bool, error) {
	l := Locator{Namespace: n, MessageID: id}
	e := s.db.QueryRowContext(ctx, "SELECT conversation,payload FROM locators WHERE account_key=? AND namespace=? AND message_id=?", s.accountKey, n, id).Scan(&l.Conversation, &l.Payload)
	if errors.Is(e, sql.ErrNoRows) {
		return l, false, nil
	}
	return l, e == nil, e
}

// RecordGap marks a consistency box degraded at a durable boundary.
func (s *Store) RecordGap(ctx context.Context, b string, p int64, r string) error {
	_, e := s.db.ExecContext(ctx, "INSERT INTO consistency(account_key,box,position,degraded,reason) VALUES(?,?,?,1,?) ON CONFLICT(account_key,box) DO UPDATE SET position=max(position,excluded.position),degraded=1,reason=excluded.reason", s.accountKey, b, p, r)
	return e
}

// Consistency returns normalized position and degradation status.
func (s *Store) Consistency(ctx context.Context, b string) (int64, bool, string, error) {
	var p int64
	var d int
	var r string
	e := s.db.QueryRowContext(ctx, "SELECT position,degraded,reason FROM consistency WHERE account_key=? AND box=?", s.accountKey, b).Scan(&p, &d, &r)
	if errors.Is(e, sql.ErrNoRows) {
		return 0, false, "", nil
	}
	return p, d != 0, r, e
}
