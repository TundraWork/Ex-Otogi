package store

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"time"
)

// SendStatus is a durable outbound intent state.
type SendStatus string

// Send intent states define the strict outbound lifecycle.
const (
	SendCreated         SendStatus = "created"
	SendInFlight        SendStatus = "in_flight"
	SendSucceeded       SendStatus = "succeeded"
	SendRetryable       SendStatus = "retryable_at"
	SendAmbiguous       SendStatus = "ambiguous"
	SendPermanentFailed SendStatus = "permanent_failed"
)

// SendIntent is a durable idempotent outbound request.
type SendIntent struct {
	// Key is the caller idempotency key.
	Key string
	// RequestDigest is the canonical request digest.
	RequestDigest []byte
	// RandomID is the stable Telegram random_id.
	RandomID int64
	// PeerLocator is durable peer reconstruction data.
	PeerLocator []byte
	// Status is the durable lifecycle state.
	Status SendStatus
	// RetryAt is the exact retry eligibility time.
	RetryAt *time.Time
	// Error is the bounded failure classification.
	Error string
	// ProviderMessageID is the correlated Telegram message ID.
	ProviderMessageID int64
	// CreatedAt is the creation time.
	CreatedAt time.Time
	// UpdatedAt is the last transition time.
	UpdatedAt time.Time
}

// SendTransition carries data recorded with a state transition.
type SendTransition struct {
	// At is the transition time.
	At time.Time
	// RetryAt is the exact retry eligibility time when applicable.
	RetryAt *time.Time
	// Error is the bounded failure classification.
	Error string
	// ProviderMessageID is an optional correlated Telegram message ID.
	ProviderMessageID int64
}

func scanSend(row interface{ Scan(...any) error }) (SendIntent, bool, error) {
	var x SendIntent
	var status string
	var retry, provider sql.NullInt64
	var created, updated int64
	e := row.Scan(&x.Key, &x.RequestDigest, &x.RandomID, &x.PeerLocator, &status, &retry, &x.Error, &provider, &created, &updated)
	if errors.Is(e, sql.ErrNoRows) {
		return x, false, nil
	}
	x.Status = SendStatus(status)
	x.CreatedAt = time.Unix(0, created).UTC()
	x.UpdatedAt = time.Unix(0, updated).UTC()
	if retry.Valid {
		v := time.Unix(0, retry.Int64).UTC()
		x.RetryAt = &v
	}
	if provider.Valid {
		x.ProviderMessageID = provider.Int64
	}
	return x, e == nil, e
}

// CreateSendIntent creates or returns an idempotent intent and rejects digest conflicts.
func (s *Store) CreateSendIntent(ctx context.Context, in SendIntent, at time.Time) (SendIntent, bool, error) {
	result, e := s.db.ExecContext(ctx, "INSERT INTO sends(account_key,key,digest,random_id,peer_locator,status,created_at,updated_at) VALUES(?,?,?,?,?,'created',?,?) ON CONFLICT(account_key,key) DO NOTHING", s.accountKey, in.Key, in.RequestDigest, in.RandomID, in.PeerLocator, at.UnixNano(), at.UnixNano())
	if e != nil {
		return in, false, e
	}
	affected, e := result.RowsAffected()
	if e != nil {
		return in, false, e
	}
	existing, ok, e := s.GetSendIntent(ctx, in.Key)
	if e != nil {
		return existing, false, e
	}
	if !ok {
		return existing, false, errors.New("telegram store: inserted send intent not found")
	}
	if !bytes.Equal(existing.RequestDigest, in.RequestDigest) {
		return existing, false, ErrSendConflict
	}
	return existing, affected == 1, nil
}

// GetSendIntent looks up an intent by idempotency key.
func (s *Store) GetSendIntent(ctx context.Context, key string) (SendIntent, bool, error) {
	return scanSend(s.db.QueryRowContext(ctx, "SELECT key,digest,random_id,peer_locator,status,retry_at,coalesce(error,''),provider_message_id,created_at,updated_at FROM sends WHERE account_key=? AND key=?", s.accountKey, key))
}
func validTransition(from, to SendStatus) bool {
	switch from {
	case SendCreated:
		return to == SendInFlight
	case SendInFlight:
		return to == SendSucceeded || to == SendRetryable || to == SendAmbiguous || to == SendPermanentFailed
	case SendRetryable:
		return to == SendInFlight
	case SendAmbiguous:
		return to == SendInFlight || to == SendSucceeded || to == SendPermanentFailed
	}
	return false
}

// TransitionSend atomically applies a valid compare-and-swap state transition.
func (s *Store) TransitionSend(ctx context.Context, key string, from, to SendStatus, t SendTransition) error {
	if !validTransition(from, to) {
		return ErrInvalidTransition
	}
	var retry any
	if t.RetryAt != nil {
		retry = t.RetryAt.UnixNano()
	}
	var terminal any
	if to == SendSucceeded || to == SendPermanentFailed {
		terminal = t.At.UnixNano()
	}
	var provider any
	if t.ProviderMessageID != 0 {
		provider = t.ProviderMessageID
	}
	r, e := s.db.ExecContext(ctx, "UPDATE sends SET status=?,retry_at=?,error=?,provider_message_id=?,updated_at=?,terminal_at=? WHERE account_key=? AND key=? AND status=?", to, retry, t.Error, provider, t.At.UnixNano(), terminal, s.accountKey, key, from)
	if e != nil {
		return e
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return ErrSendConflict
	}
	return nil
}

// RecoverableSends returns nonterminal intents eligible for startup reconciliation.
func (s *Store) RecoverableSends(ctx context.Context, now time.Time) ([]SendIntent, error) {
	rows, e := s.db.QueryContext(ctx, "SELECT key,digest,random_id,peer_locator,status,retry_at,coalesce(error,''),provider_message_id,created_at,updated_at FROM sends WHERE account_key=? AND (status IN ('created','in_flight','ambiguous') OR (status='retryable_at' AND retry_at<=?)) ORDER BY created_at", s.accountKey, now.UnixNano())
	if e != nil {
		return nil, e
	}
	defer func() { _ = rows.Close() }()
	var out []SendIntent
	for rows.Next() {
		x, _, e := scanSend(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// PurgeTerminalSends removes terminal intents older than thirty days.
func (s *Store) PurgeTerminalSends(ctx context.Context, now time.Time) error {
	_, e := s.db.ExecContext(ctx, "DELETE FROM sends WHERE account_key=? AND status IN ('succeeded','permanent_failed') AND terminal_at<?", s.accountKey, now.Add(-30*24*time.Hour).UnixNano())
	return e
}
