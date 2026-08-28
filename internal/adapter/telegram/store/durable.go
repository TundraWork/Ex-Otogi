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

const (
	// MaxRawFieldBytes bounds raw update, peer, quarantine, and send fields.
	MaxRawFieldBytes = 64 << 10
	// MaxEventPayloadBytes bounds normalized event payloads.
	MaxEventPayloadBytes = 1 << 20
	// MaxLocatorPayloadBytes bounds locator refetch data.
	MaxLocatorPayloadBytes = 16 << 10
	maxTextBytes           = 4096
	maxCaptureLocators     = 1024
	// MaxCaptureRows bounds one manager delivery before capture allocation or SQL.
	MaxCaptureRows = 1024
)

func validateBytes(field string, value []byte, max int) error {
	if len(value) > max {
		return fmt.Errorf("%w: %s is %d bytes (max %d)", ErrInputTooLarge, field, len(value), max)
	}
	return nil
}

func validateText(field, value string) error {
	return validateBytes(field, []byte(value), maxTextBytes)
}

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
	// PayloadHash retains the content hash after payload compaction.
	PayloadHash []byte
	// CreatedAt is the first observation time.
	CreatedAt time.Time
	// UpdatedAt is the most recent observation time.
	UpdatedAt time.Time
}

// SendCorrelation binds Telegram's random_id acknowledgement to a provider message ID.
type SendCorrelation struct {
	RandomID  int64
	MessageID int64
}

// Capture is one atomic normalized-capture transaction.
type Capture struct {
	// JournalID is the source transport journal row.
	JournalID int64
	// KeepJournalPending defers normalization when more manager outputs remain.
	KeepJournalPending bool
	// Peers are Telegram entities learned in the same durable capture.
	Peers []Peer
	// Inbox is an optional normalized routed event.
	Inbox *InboxInput
	// Quarantine is an optional unsupported raw update.
	Quarantine *QuarantineInput
	// Locators are provider identities learned during mapping.
	Locators []Locator
	// SendCorrelations durably complete outbound intents acknowledged by this update.
	SendCorrelations []SendCorrelation
	// Box identifies the common or channel consistency stream.
	Box string
	// Position is the last normalized source position.
	Position int64
	// CapturedAt is the durable capture time; zero uses the current UTC time.
	CapturedAt time.Time
}

// PutPeer upserts an account-scoped peer record.
func (s *Store) PutPeer(ctx context.Context, p Peer) error {
	if err := validateText("peer kind", p.Kind); err != nil {
		return err
	}
	if err := validateBytes("peer payload", p.Payload, MaxRawFieldBytes); err != nil {
		return err
	}
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

// AppendJournal durably appends a bounded raw transport packet.
func (s *Store) AppendJournal(ctx context.Context, p []byte, at time.Time) (int64, error) {
	if err := validateBytes("journal payload", p, MaxRawFieldBytes); err != nil {
		return 0, err
	}
	r, e := s.db.ExecContext(ctx, "INSERT INTO journal(account_key,payload,received_at) VALUES(?,?,?)", s.accountKey, p, at.UnixNano())
	if e != nil {
		return 0, fmt.Errorf("append journal for account %q: %w", s.accountKey, e)
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
	result, err := s.db.ExecContext(ctx, "UPDATE journal SET normalized_at=? WHERE account_key=? AND id=?", at.UnixNano(), s.accountKey, id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("journal %d does not belong to account %q", id, s.accountKey)
	}
	return nil
}

type sqlExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func validateInbox(in InboxInput) error {
	for field, value := range map[string]string{"inbox id": in.ID, "inbox source key": in.SourceKey, "inbox conversation": in.Conversation} {
		if err := validateText(field, value); err != nil {
			return err
		}
	}
	return validateBytes("inbox payload", in.Payload, MaxEventPayloadBytes)
}

func insertInbox(ctx context.Context, x sqlExecer, a string, in InboxInput) (bool, error) {
	if err := validateInbox(in); err != nil {
		return false, err
	}
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

// Capture atomically persists one normalized capture and progress.
func (s *Store) Capture(ctx context.Context, c Capture) (bool, error) {
	return s.CaptureBatch(ctx, []Capture{c})
}

// CaptureBatch atomically persists every normalized result derived from one
// manager delivery. The journal row is marked normalized only if all peers,
// locators, inbox/quarantine rows, and consistency markers commit together.
func (s *Store) CaptureBatch(ctx context.Context, captures []Capture) (bool, error) {
	if len(captures) == 0 {
		return false, fmt.Errorf("capture batch: empty")
	}
	if len(captures) > MaxCaptureRows {
		return false, fmt.Errorf("%w: capture count %d", ErrInputTooLarge, len(captures))
	}
	for _, c := range captures {
		if err := validateCapture(c); err != nil {
			return false, err
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	inserted := false
	for _, c := range captures {
		now := c.CapturedAt.UTC()
		if now.IsZero() {
			now = time.Now().UTC()
		}
		if !c.KeepJournalPending {
			result, err := tx.ExecContext(ctx, "UPDATE journal SET normalized_at=? WHERE account_key=? AND id=?", now.UnixNano(), s.accountKey, c.JournalID)
			if err != nil {
				return false, err
			}
			affected, err := result.RowsAffected()
			if err != nil {
				return false, err
			}
			if affected != 1 {
				return false, fmt.Errorf("capture journal %d does not belong to account %q", c.JournalID, s.accountKey)
			}
		}
		for _, p := range c.Peers {
			if _, err = tx.ExecContext(ctx, "INSERT INTO peers(account_key,kind,peer_id,access_hash,payload) VALUES(?,?,?,?,?) ON CONFLICT(account_key,kind,peer_id) DO UPDATE SET access_hash=excluded.access_hash,payload=excluded.payload", s.accountKey, p.Kind, p.ID, p.AccessHash, p.Payload); err != nil {
				return false, err
			}
		}
		if c.Inbox != nil {
			added, err := insertInbox(ctx, tx, s.accountKey, *c.Inbox)
			if err != nil {
				return false, err
			}
			inserted = inserted || added
		}
		if c.Quarantine != nil {
			if _, err = tx.ExecContext(ctx, "INSERT INTO quarantine(account_key,source_key,kind,payload,created_at) VALUES(?,?,?,?,?) ON CONFLICT(account_key,source_key) DO NOTHING", s.accountKey, c.Quarantine.SourceKey, c.Quarantine.Kind, c.Quarantine.Payload, now.UnixNano()); err != nil {
				return false, err
			}
		}
		for _, l := range c.Locators {
			h := sha256.Sum256(l.Payload)
			if _, err = tx.ExecContext(ctx, "INSERT INTO locators(account_key,namespace,message_id,conversation,payload,payload_hash,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(account_key,namespace,message_id,conversation) DO UPDATE SET payload=excluded.payload,payload_hash=excluded.payload_hash,updated_at=excluded.updated_at", s.accountKey, l.Namespace, l.MessageID, l.Conversation, l.Payload, h[:], now.UnixNano(), now.UnixNano()); err != nil {
				return false, err
			}
		}
		for _, correlation := range c.SendCorrelations {
			if correlation.RandomID <= 0 || correlation.MessageID <= 0 {
				return false, fmt.Errorf("invalid send correlation")
			}
			if _, err = tx.ExecContext(ctx, "UPDATE sends SET status='succeeded',provider_message_id=?,updated_at=?,terminal_at=? WHERE account_key=? AND random_id=? AND status IN ('in_flight','ambiguous')", correlation.MessageID, now.UnixNano(), now.UnixNano(), s.accountKey, correlation.RandomID); err != nil {
				return false, err
			}
		}
		if c.Box != "" {
			if _, err = tx.ExecContext(ctx, "INSERT INTO consistency(account_key,box,position) VALUES(?,?,?) ON CONFLICT(account_key,box) DO UPDATE SET position=max(position,excluded.position)", s.accountKey, c.Box, c.Position); err != nil {
				return false, err
			}
		}
	}
	return inserted, tx.Commit()
}

func validateCapture(c Capture) error {
	if c.JournalID <= 0 {
		return fmt.Errorf("capture journal %d: must be positive", c.JournalID)
	}
	if len(c.Locators) > maxCaptureLocators {
		return fmt.Errorf("%w: locator count %d", ErrInputTooLarge, len(c.Locators))
	}
	if len(c.SendCorrelations) > MaxCaptureRows {
		return fmt.Errorf("%w: send correlation count %d", ErrInputTooLarge, len(c.SendCorrelations))
	}
	if len(c.Peers) > maxCaptureLocators {
		return fmt.Errorf("%w: peer count %d", ErrInputTooLarge, len(c.Peers))
	}
	if err := validateText("consistency box", c.Box); err != nil {
		return err
	}
	if c.Inbox != nil {
		if err := validateInbox(*c.Inbox); err != nil {
			return err
		}
	}
	if c.Quarantine != nil {
		if err := validateText("quarantine source key", c.Quarantine.SourceKey); err != nil {
			return err
		}
		if err := validateText("quarantine kind", c.Quarantine.Kind); err != nil {
			return err
		}
		if err := validateBytes("quarantine payload", c.Quarantine.Payload, MaxRawFieldBytes); err != nil {
			return err
		}
	}
	for _, p := range c.Peers {
		if err := validateText("peer kind", p.Kind); err != nil {
			return err
		}
		if err := validateBytes("peer payload", p.Payload, MaxRawFieldBytes); err != nil {
			return err
		}
	}
	for _, l := range c.Locators {
		if err := validateText("locator namespace", l.Namespace); err != nil {
			return err
		}
		if err := validateText("locator conversation", l.Conversation); err != nil {
			return err
		}
		if err := validateBytes("locator payload", l.Payload, MaxLocatorPayloadBytes); err != nil {
			return err
		}
	}
	return nil
}

// GetLocator resolves an account-scoped provider message locator.
func (s *Store) GetLocator(ctx context.Context, namespace string, id int64, conversation string) (Locator, bool, error) {
	l := Locator{Namespace: namespace, MessageID: id, Conversation: conversation}
	var created, updated int64
	e := s.db.QueryRowContext(ctx, "SELECT payload,payload_hash,created_at,updated_at FROM locators WHERE account_key=? AND namespace=? AND message_id=? AND conversation=?", s.accountKey, namespace, id, conversation).Scan(&l.Payload, &l.PayloadHash, &created, &updated)
	if errors.Is(e, sql.ErrNoRows) {
		return l, false, nil
	}
	if e != nil {
		return l, false, e
	}
	l.CreatedAt = time.Unix(0, created).UTC()
	l.UpdatedAt = time.Unix(0, updated).UTC()
	return l, true, nil
}

// FindLocators returns every conversation matching a provider message identity.
// Common Telegram message IDs are conversation-scoped and may match more than one row.
func (s *Store) FindLocators(ctx context.Context, namespace string, id int64) ([]Locator, error) {
	if err := validateText("locator namespace", namespace); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT conversation,payload,payload_hash,created_at,updated_at FROM locators WHERE account_key=? AND namespace=? AND message_id=? ORDER BY conversation LIMIT ?", s.accountKey, namespace, id, MaxCaptureRows+1)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var locators []Locator
	for rows.Next() {
		locator := Locator{Namespace: namespace, MessageID: id}
		var created, updated int64
		if err := rows.Scan(&locator.Conversation, &locator.Payload, &locator.PayloadHash, &created, &updated); err != nil {
			return nil, err
		}
		locator.CreatedAt = time.Unix(0, created).UTC()
		locator.UpdatedAt = time.Unix(0, updated).UTC()
		locators = append(locators, locator)
		if len(locators) > MaxCaptureRows {
			return nil, fmt.Errorf("%w: locator matches %d", ErrInputTooLarge, len(locators))
		}
	}
	return locators, rows.Err()
}

// KnownPeers returns one stable bounded page and whether more peers remain.
func (s *Store) KnownPeers(ctx context.Context, offset, limit int) ([]Peer, bool, error) {
	if offset < 0 || limit <= 0 || limit > MaxCaptureRows {
		return nil, false, ErrInputTooLarge
	}
	rows, err := s.db.QueryContext(ctx, "SELECT kind,peer_id,access_hash,payload FROM peers WHERE account_key=? ORDER BY kind,peer_id LIMIT ? OFFSET ?", s.accountKey, limit+1, offset)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rows.Close() }()
	peers := make([]Peer, 0, limit+1)
	for rows.Next() {
		var peer Peer
		if err := rows.Scan(&peer.Kind, &peer.ID, &peer.AccessHash, &peer.Payload); err != nil {
			return nil, false, err
		}
		peers = append(peers, peer)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	more := len(peers) > limit
	if more {
		peers = peers[:limit]
	}
	return peers, more, nil
}

// RecoveryChannels returns all access-hash channels, using zero for a missing cursor.
func (s *Store) RecoveryChannels(ctx context.Context, userID int64) ([]struct {
	ID, AccessHash int64
	Pts            int
}, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT h.channel_id,h.access_hash,coalesce(c.pts,0) FROM channel_access_hash h LEFT JOIN channel_state c ON c.account_key=h.account_key AND c.user_id=h.user_id AND c.channel_id=h.channel_id WHERE h.account_key=? AND h.user_id=? ORDER BY h.channel_id", s.accountKey, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var result []struct {
		ID, AccessHash int64
		Pts            int
	}
	for rows.Next() {
		var v struct {
			ID, AccessHash int64
			Pts            int
		}
		if err := rows.Scan(&v.ID, &v.AccessHash, &v.Pts); err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	return result, rows.Err()
}

// RecordGap marks a consistency box degraded at a durable boundary.
func (s *Store) RecordGap(ctx context.Context, b string, p int64, r string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, "INSERT INTO consistency(account_key,box,position,degraded,reason) VALUES(?,?,?,1,?) ON CONFLICT(account_key,box) DO UPDATE SET position=max(position,excluded.position),degraded=1,reason=excluded.reason", s.accountKey, b, p, r); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO recovery(account_key,box,position,state,cause,acknowledged) VALUES(?,?,?,?,?,0) ON CONFLICT(account_key,box) DO UPDATE SET position=max(position,excluded.position),state=CASE WHEN recovery.state=? AND recovery.acknowledged=1 AND recovery.cause=excluded.cause AND recovery.position>=excluded.position THEN recovery.state ELSE excluded.state END,cause=CASE WHEN recovery.state=? AND recovery.acknowledged=1 AND recovery.cause=excluded.cause AND recovery.position>=excluded.position THEN recovery.cause ELSE excluded.cause END,acknowledged=CASE WHEN recovery.state=? AND recovery.acknowledged=1 AND recovery.cause=excluded.cause AND recovery.position>=excluded.position THEN 1 ELSE 0 END", s.accountKey, b, p, RecoveryDegraded, r, RecoveryPermanentDegraded, RecoveryPermanentDegraded, RecoveryPermanentDegraded); err != nil {
		return err
	}
	return tx.Commit()
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
