package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// RecoveryState is the persisted state of one required consistency box.
type RecoveryState string

// Persisted recovery states.
const (
	RecoveryDegraded          RecoveryState = "degraded"
	RecoveryResynchronizing   RecoveryState = "resynchronizing"
	RecoveryReady             RecoveryState = "ready"
	RecoveryPermanentDegraded RecoveryState = "permanent_degraded"
)

// QuarantineRow is one mapper-upgrade reprocessing source.
type QuarantineRow struct {
	// ID is the durable quarantine row identifier.
	ID int64
	// SourceKey is the stable source deduplication key.
	SourceKey string
	// Kind identifies the quarantined Telegram constructor family.
	Kind string
	// Payload contains bounded deterministic preserved bytes.
	Payload []byte
}

// RecoveryRecord is the operator-visible recovery status of one consistency box.
type RecoveryRecord struct {
	// Box identifies the common or channel consistency stream.
	Box string
	// Position is the last durable source position.
	Position int64
	// State is the persisted recovery lifecycle state.
	State RecoveryState
	// Cause describes why recovery is required or permanent.
	Cause string
	// Acknowledged records explicit operator acceptance of permanent degradation.
	Acknowledged bool
}

// BeginRecovery moves a degraded box into resynchronization without losing its cause.
func (s *Store) BeginRecovery(ctx context.Context, box string) error {
	return s.transitionRecovery(ctx, box, RecoveryResynchronizing, "", false, false)
}

// MarkRecoveryReady records a reconciled source position and clears degradation.
func (s *Store) MarkRecoveryReady(ctx context.Context, box string, position int64) error {
	if box == "" || position < 0 {
		return fmt.Errorf("invalid ready recovery record")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, "UPDATE recovery SET position=max(position,?),state=?,cause='',acknowledged=0 WHERE account_key=? AND box=?", position, RecoveryReady, s.accountKey, box)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		if err != nil {
			return err
		}
		return ErrStateNotFound
	}
	if _, err = tx.ExecContext(ctx, "UPDATE consistency SET position=max(position,?),degraded=0,reason='' WHERE account_key=? AND box=?", position, s.accountKey, box); err != nil {
		return err
	}
	return tx.Commit()
}

// MarkRecoveryPermanent records a condition that cannot be reconciled automatically.
func (s *Store) MarkRecoveryPermanent(ctx context.Context, box, cause string) error {
	if cause == "" {
		return fmt.Errorf("permanent recovery cause is empty")
	}
	return s.transitionRecovery(ctx, box, RecoveryPermanentDegraded, cause, true, false)
}

// AcknowledgeRecovery explicitly accepts one permanent-degraded box.
func (s *Store) AcknowledgeRecovery(ctx context.Context, box string) error {
	result, err := s.db.ExecContext(ctx, "UPDATE recovery SET acknowledged=1 WHERE account_key=? AND box=? AND state=?", s.accountKey, box, RecoveryPermanentDegraded)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		if err != nil {
			return err
		}
		return ErrStateNotFound
	}
	return nil
}

func (s *Store) transitionRecovery(ctx context.Context, box string, state RecoveryState, cause string, replaceCause, acknowledged bool) error {
	if box == "" {
		return fmt.Errorf("recovery box is empty")
	}
	query := "UPDATE recovery SET state=?,acknowledged=? WHERE account_key=? AND box=?"
	args := []any{state, acknowledged, s.accountKey, box}
	if replaceCause {
		query = "UPDATE recovery SET state=?,cause=?,acknowledged=? WHERE account_key=? AND box=?"
		args = []any{state, cause, acknowledged, s.accountKey, box}
	}
	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		if err != nil {
			return err
		}
		return ErrStateNotFound
	}
	return nil
}

// Recovery returns one persisted box status.
func (s *Store) Recovery(ctx context.Context, box string) (RecoveryRecord, bool, error) {
	var record RecoveryRecord
	var acknowledged int
	err := s.db.QueryRowContext(ctx, "SELECT box,position,state,cause,acknowledged FROM recovery WHERE account_key=? AND box=?", s.accountKey, box).Scan(&record.Box, &record.Position, &record.State, &record.Cause, &acknowledged)
	if errors.Is(err, sql.ErrNoRows) {
		return RecoveryRecord{}, false, nil
	}
	record.Acknowledged = acknowledged != 0
	return record, err == nil, err
}

// RecoveryReady reports readiness across every required persisted box.
func (s *Store) RecoveryReady(ctx context.Context) (bool, []RecoveryRecord, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT box,position,state,cause,acknowledged FROM recovery WHERE account_key=? ORDER BY box", s.accountKey)
	if err != nil {
		return false, nil, err
	}
	defer func() { _ = rows.Close() }()
	ready := true
	var records []RecoveryRecord
	for rows.Next() {
		var record RecoveryRecord
		var acknowledged int
		if err := rows.Scan(&record.Box, &record.Position, &record.State, &record.Cause, &acknowledged); err != nil {
			return false, nil, err
		}
		record.Acknowledged = acknowledged != 0
		if record.State != RecoveryReady && (record.State != RecoveryPermanentDegraded || !record.Acknowledged) {
			ready = false
		}
		records = append(records, record)
	}
	return ready, records, rows.Err()
}

// RequiredRecoveries returns every non-ready recovery record in stable box order.
func (s *Store) RequiredRecoveries(ctx context.Context) ([]RecoveryRecord, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT box,position,state,cause,acknowledged FROM recovery WHERE account_key=? AND state<>? ORDER BY box", s.accountKey, RecoveryReady)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var records []RecoveryRecord
	for rows.Next() {
		var record RecoveryRecord
		var acknowledged int
		if err := rows.Scan(&record.Box, &record.Position, &record.State, &record.Cause, &acknowledged); err != nil {
			return nil, err
		}
		record.Acknowledged = acknowledged != 0
		if record.State != RecoveryPermanentDegraded || !record.Acknowledged {
			records = append(records, record)
		}
	}
	return records, rows.Err()
}

// PendingJournal returns at most limit unnormalized packets in receipt order.
func (s *Store) PendingJournal(ctx context.Context, limit int) ([]JournalRow, error) {
	if limit <= 0 || limit > MaxCaptureRows {
		return nil, ErrInputTooLarge
	}
	rows, err := s.db.QueryContext(ctx, "SELECT id,payload,received_at,normalized_at FROM journal WHERE account_key=? AND normalized_at IS NULL ORDER BY id LIMIT ?", s.accountKey, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var result []JournalRow
	for rows.Next() {
		var row JournalRow
		var received int64
		var normalized sql.NullInt64
		if err := rows.Scan(&row.ID, &row.Payload, &received, &normalized); err != nil {
			return nil, err
		}
		row.ReceivedAt = time.Unix(0, received).UTC()
		result = append(result, row)
	}
	return result, rows.Err()
}

// PendingQuarantine returns at most limit unreprocessed rows in stable order.
func (s *Store) PendingQuarantine(ctx context.Context, limit int) ([]QuarantineRow, error) {
	if limit <= 0 || limit > MaxCaptureRows {
		return nil, ErrInputTooLarge
	}
	rows, err := s.db.QueryContext(ctx, "SELECT id,source_key,kind,payload FROM quarantine WHERE account_key=? AND reprocessed_at IS NULL ORDER BY id LIMIT ?", s.accountKey, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var result []QuarantineRow
	for rows.Next() {
		var row QuarantineRow
		if err := rows.Scan(&row.ID, &row.SourceKey, &row.Kind, &row.Payload); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

// CompleteQuarantine records successful mapper-upgrade reprocessing while retaining the source-key tombstone.
func (s *Store) CompleteQuarantine(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, "UPDATE quarantine SET reprocessed_at=? WHERE account_key=? AND id=? AND reprocessed_at IS NULL", time.Now().UTC().UnixNano(), s.accountKey, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrStateNotFound
	}
	return nil
}
