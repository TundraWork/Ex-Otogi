package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/gotd/td/telegram/updates"
)

// GetState returns common gotd update state.
func (s *Store) GetState(ctx context.Context, userID int64) (updates.State, bool, error) {
	var v updates.State
	e := s.db.QueryRowContext(ctx, "SELECT pts,qts,date,seq FROM update_state WHERE account_key=? AND user_id=?", s.accountKey, userID).Scan(&v.Pts, &v.Qts, &v.Date, &v.Seq)
	if errors.Is(e, sql.ErrNoRows) {
		return v, false, nil
	}
	return v, e == nil, e
}

// SetState upserts common gotd update state.
func (s *Store) SetState(ctx context.Context, userID int64, v updates.State) error {
	_, e := s.db.ExecContext(ctx, "INSERT INTO update_state(account_key,user_id,pts,qts,date,seq) VALUES(?,?,?,?,?,?) ON CONFLICT(account_key,user_id) DO UPDATE SET pts=excluded.pts,qts=excluded.qts,date=excluded.date,seq=excluded.seq", s.accountKey, userID, v.Pts, v.Qts, v.Date, v.Seq)
	return e
}
func (s *Store) updateState(ctx context.Context, userID int64, set string, args ...any) error {
	q := "UPDATE update_state SET " + set + " WHERE account_key=? AND user_id=?"
	args = append(args, s.accountKey, userID)
	r, e := s.db.ExecContext(ctx, q, args...)
	if e != nil {
		return e
	}
	n, e := r.RowsAffected()
	if e == nil && n == 0 {
		return ErrStateNotFound
	}
	return e
}

// SetPts updates pts for existing common state.
func (s *Store) SetPts(ctx context.Context, u int64, v int) error {
	return s.updateState(ctx, u, "pts=?", v)
}

// SetQts updates qts for existing common state.
func (s *Store) SetQts(ctx context.Context, u int64, v int) error {
	return s.updateState(ctx, u, "qts=?", v)
}

// SetDate updates date for existing common state.
func (s *Store) SetDate(ctx context.Context, u int64, v int) error {
	return s.updateState(ctx, u, "date=?", v)
}

// SetSeq updates seq for existing common state.
func (s *Store) SetSeq(ctx context.Context, u int64, v int) error {
	return s.updateState(ctx, u, "seq=?", v)
}

// SetDateSeq atomically updates date and seq for existing common state.
func (s *Store) SetDateSeq(ctx context.Context, u int64, d, q int) error {
	return s.updateState(ctx, u, "date=?,seq=?", d, q)
}

// GetChannelPts returns channel pts.
func (s *Store) GetChannelPts(ctx context.Context, u, c int64) (int, bool, error) {
	var v int
	e := s.db.QueryRowContext(ctx, "SELECT pts FROM channel_state WHERE account_key=? AND user_id=? AND channel_id=?", s.accountKey, u, c).Scan(&v)
	if errors.Is(e, sql.ErrNoRows) {
		return 0, false, nil
	}
	return v, e == nil, e
}

// SetChannelPts upserts channel pts.
func (s *Store) SetChannelPts(ctx context.Context, u, c int64, v int) error {
	_, e := s.db.ExecContext(ctx, "INSERT INTO channel_state(account_key,user_id,channel_id,pts) VALUES(?,?,?,?) ON CONFLICT(account_key,user_id,channel_id) DO UPDATE SET pts=excluded.pts", s.accountKey, u, c, v)
	return e
}

// ForEachChannels visits account-scoped channel states in ID order.
func (s *Store) ForEachChannels(ctx context.Context, u int64, f func(context.Context, int64, int) error) error {
	rows, e := s.db.QueryContext(ctx, "SELECT channel_id,pts FROM channel_state WHERE account_key=? AND user_id=? ORDER BY channel_id", s.accountKey, u)
	if e != nil {
		return e
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		var pts int
		if e = rows.Scan(&id, &pts); e != nil {
			return e
		}
		if e = f(ctx, id, pts); e != nil {
			return e
		}
	}
	return rows.Err()
}

// SetChannelAccessHash upserts an account-scoped channel access hash without creating update state.
func (s *Store) SetChannelAccessHash(ctx context.Context, u, c, h int64) error {
	_, e := s.db.ExecContext(ctx, "INSERT INTO channel_access_hash(account_key,user_id,channel_id,access_hash) VALUES(?,?,?,?) ON CONFLICT(account_key,user_id,channel_id) DO UPDATE SET access_hash=excluded.access_hash", s.accountKey, u, c, h)
	return e
}

// GetChannelAccessHash returns an account-scoped channel access hash.
func (s *Store) GetChannelAccessHash(ctx context.Context, u, c int64) (int64, bool, error) {
	var h sql.NullInt64
	e := s.db.QueryRowContext(ctx, "SELECT access_hash FROM channel_access_hash WHERE account_key=? AND user_id=? AND channel_id=?", s.accountKey, u, c).Scan(&h)
	if errors.Is(e, sql.ErrNoRows) || e == nil && !h.Valid {
		return 0, false, nil
	}
	if e != nil {
		return 0, false, e
	}
	return h.Int64, true, nil
}

// SetUserAccessHash upserts an account-scoped user access hash.
func (s *Store) SetUserAccessHash(ctx context.Context, u, t, h int64) error {
	_, e := s.db.ExecContext(ctx, "INSERT INTO user_hash(account_key,user_id,target_id,access_hash) VALUES(?,?,?,?) ON CONFLICT(account_key,user_id,target_id) DO UPDATE SET access_hash=excluded.access_hash", s.accountKey, u, t, h)
	return e
}

// GetUserAccessHash returns an account-scoped user access hash.
func (s *Store) GetUserAccessHash(ctx context.Context, u, t int64) (int64, bool, error) {
	var h int64
	e := s.db.QueryRowContext(ctx, "SELECT access_hash FROM user_hash WHERE account_key=? AND user_id=? AND target_id=?", s.accountKey, u, t).Scan(&h)
	if errors.Is(e, sql.ErrNoRows) {
		return 0, false, nil
	}
	return h, e == nil, e
}
