package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func scanInbox(row interface{ Scan(...any) error }) (InboxRow, bool, error) {
	var r InboxRow
	var available int64
	var lease, done sql.NullInt64
	var payload []byte
	e := row.Scan(&r.ID, &r.SourceKey, &r.Conversation, &r.Sequence, &payload, &available, &r.Status, &r.Owner, &lease, &r.Attempts, &done, &r.Error)
	if errors.Is(e, sql.ErrNoRows) {
		return r, false, nil
	}
	r.Payload = payload
	r.AvailableAt = time.Unix(0, available).UTC()
	if lease.Valid {
		v := time.Unix(0, lease.Int64).UTC()
		r.LeaseUntil = &v
	}
	if done.Valid {
		v := time.Unix(0, done.Int64).UTC()
		r.CompletedAt = &v
	}
	return r, e == nil, e
}

// GetInbox returns a normalized inbox row.
func (s *Store) GetInbox(ctx context.Context, id string) (InboxRow, bool, error) {
	return scanInbox(s.db.QueryRowContext(ctx, "SELECT id,source_key,conversation,sequence,payload,available_at,status,coalesce(owner,''),lease_until,attempts,completed_at,coalesce(error,'') FROM inbox WHERE account_key=? AND id=?", s.accountKey, id))
}

// ClaimNext transactionally claims the oldest eligible conversation head.
func (s *Store) ClaimNext(ctx context.Context, owner string, now time.Time, lease time.Duration) (InboxRow, bool, error) {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return InboxRow{}, false, e
	}
	defer func() { _ = tx.Rollback() }()
	r, ok, e := scanInbox(tx.QueryRowContext(ctx, `SELECT i.id,i.source_key,i.conversation,i.sequence,i.payload,i.available_at,i.status,coalesce(i.owner,''),i.lease_until,i.attempts,i.completed_at,coalesce(i.error,'') FROM inbox i WHERE i.account_key=? AND i.status IN ('pending','claimed') AND i.available_at<=? AND (i.status='pending' OR i.lease_until<=?) AND NOT EXISTS (SELECT 1 FROM inbox h WHERE h.account_key=i.account_key AND h.conversation=i.conversation AND h.sequence<i.sequence AND h.status NOT IN ('terminal','failed')) ORDER BY i.available_at,i.conversation,i.sequence LIMIT 1`, s.accountKey, now.UnixNano(), now.UnixNano()))
	if e != nil || !ok {
		return r, ok, e
	}
	until := now.Add(lease)
	res, e := tx.ExecContext(ctx, "UPDATE inbox SET status='claimed',owner=?,lease_until=?,attempts=attempts+1 WHERE account_key=? AND id=? AND (status='pending' OR lease_until<=?)", owner, until.UnixNano(), s.accountKey, r.ID, now.UnixNano())
	if e != nil {
		return r, false, e
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return r, false, nil
	}
	if e = tx.Commit(); e != nil {
		return r, false, e
	}
	r.Status = "claimed"
	r.Owner = owner
	r.LeaseUntil = &until
	r.Attempts++
	return r, true, nil
}

// ReleaseClaim returns a claimed row with a retry schedule.
func (s *Store) ReleaseClaim(ctx context.Context, id, owner string, next time.Time, reason string) error {
	return s.claimUpdate(ctx, id, owner, "UPDATE inbox SET status='pending',owner=NULL,lease_until=NULL,available_at=?,error=? WHERE account_key=? AND id=? AND status='claimed' AND owner=?", next.UnixNano(), reason)
}

// CompleteClaim marks an owned claim terminal.
func (s *Store) CompleteClaim(ctx context.Context, id, owner string, at time.Time) error {
	return s.claimUpdate(ctx, id, owner, "UPDATE inbox SET status='terminal',owner=NULL,lease_until=NULL,completed_at=? WHERE account_key=? AND id=? AND status='claimed' AND owner=?", at.UnixNano())
}

// FailClaim marks an owned claim permanently failed.
func (s *Store) FailClaim(ctx context.Context, id, owner, reason string, at time.Time) error {
	return s.claimUpdate(ctx, id, owner, "UPDATE inbox SET status='failed',owner=NULL,lease_until=NULL,completed_at=?,error=? WHERE account_key=? AND id=? AND status='claimed' AND owner=?", at.UnixNano(), reason)
}
func (s *Store) claimUpdate(ctx context.Context, id, owner, q string, a ...any) error {
	a = append(a, s.accountKey, id, owner)
	r, e := s.db.ExecContext(ctx, q, a...)
	if e != nil {
		return e
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return ErrClaimConflict
	}
	return nil
}

// Compact applies journal and participant-payload retention.
func (s *Store) Compact(ctx context.Context, now time.Time) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer func() { _ = tx.Rollback() }()
	compact := now.Add(-7 * 24 * time.Hour).UnixNano()
	expire := now.Add(-30 * 24 * time.Hour).UnixNano()
	journal := now.Add(-24 * time.Hour).UnixNano()
	if _, e = tx.ExecContext(ctx, "UPDATE inbox SET payload=NULL WHERE account_key=? AND status IN ('terminal','failed') AND completed_at<?", s.accountKey, compact); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "UPDATE quarantine SET payload=NULL WHERE account_key=? AND created_at<?", s.accountKey, compact); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "DELETE FROM inbox WHERE account_key=? AND status IN ('terminal','failed') AND completed_at<?", s.accountKey, expire); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "DELETE FROM quarantine WHERE account_key=? AND created_at<?", s.accountKey, expire); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "DELETE FROM journal WHERE account_key=? AND normalized_at IS NOT NULL AND normalized_at<?", s.accountKey, journal); e != nil {
		return e
	}
	return tx.Commit()
}

// EraseAccount deletes all mutable data for the bound account.
func (s *Store) EraseAccount(ctx context.Context) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer func() { _ = tx.Rollback() }()
	for _, table := range []string{"update_state", "channel_state", "user_hash", "peers", "journal", "inbox", "quarantine", "locators", "consistency", "sends"} {
		if _, e = tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE account_key=?", s.accountKey); e != nil {
			return e
		}
	}
	return tx.Commit()
}
