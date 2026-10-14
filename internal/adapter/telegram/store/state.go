package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/gotd/td/telegram/updates"
)

// The methods below implement gotd's updates.StateStorage, ChannelAccessHasher,
// and UserAccessHasher. The database holds one account, so userID is ignored;
// Bind guarantees the account.

var (
	_ updates.StateStorage        = (*Store)(nil)
	_ updates.ChannelAccessHasher = (*Store)(nil)
	_ updates.UserAccessHasher    = (*Store)(nil)
)

// GetState returns the saved common update state.
func (s *Store) GetState(ctx context.Context, _ int64) (updates.State, bool, error) {
	var st updates.State
	err := s.db.QueryRowContext(ctx, "SELECT pts, qts, date, seq FROM update_state").Scan(&st.Pts, &st.Qts, &st.Date, &st.Seq)
	if errors.Is(err, sql.ErrNoRows) {
		return updates.State{}, false, nil
	}
	if err != nil {
		return updates.State{}, false, fmt.Errorf("get update state: %w", err)
	}
	return st, true, nil
}

// SetState replaces the common update state.
func (s *Store) SetState(ctx context.Context, _ int64, st updates.State) error {
	return s.exec(ctx, "set update state", `INSERT INTO update_state (singleton, pts, qts, date, seq) VALUES (1, ?, ?, ?, ?)
		ON CONFLICT (singleton) DO UPDATE SET pts = excluded.pts, qts = excluded.qts, date = excluded.date, seq = excluded.seq`,
		st.Pts, st.Qts, st.Date, st.Seq)
}

// SetPts updates the saved pts.
func (s *Store) SetPts(ctx context.Context, _ int64, pts int) error {
	return s.setField(ctx, "pts", pts)
}

// SetQts updates the saved qts.
func (s *Store) SetQts(ctx context.Context, _ int64, qts int) error {
	return s.setField(ctx, "qts", qts)
}

// SetDate updates the saved date.
func (s *Store) SetDate(ctx context.Context, _ int64, date int) error {
	return s.setField(ctx, "date", date)
}

// SetSeq updates the saved seq.
func (s *Store) SetSeq(ctx context.Context, _ int64, seq int) error {
	return s.setField(ctx, "seq", seq)
}

// SetDateSeq updates the saved date and seq together.
func (s *Store) SetDateSeq(ctx context.Context, _ int64, date, seq int) error {
	return s.updateState(ctx, "date = ?, seq = ?", date, seq)
}

func (s *Store) setField(ctx context.Context, column string, value int) error {
	return s.updateState(ctx, column+" = ?", value)
}

// updateState applies assignments to the state row, creating it first if needed.
func (s *Store) updateState(ctx context.Context, assignments string, args ...any) error {
	if err := s.exec(ctx, "create update state", "INSERT INTO update_state (singleton, pts, qts, date, seq) VALUES (1, 0, 0, 0, 0) ON CONFLICT DO NOTHING"); err != nil {
		return err
	}
	return s.exec(ctx, "update state", "UPDATE update_state SET "+assignments, args...)
}

// GetChannelPts returns the saved pts of a channel.
func (s *Store) GetChannelPts(ctx context.Context, _, channelID int64) (int, bool, error) {
	var pts int
	err := s.db.QueryRowContext(ctx, "SELECT pts FROM channel_state WHERE channel_id = ?", channelID).Scan(&pts)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("get channel pts: %w", err)
	}
	return pts, true, nil
}

// SetChannelPts saves the pts of a channel.
func (s *Store) SetChannelPts(ctx context.Context, _, channelID int64, pts int) error {
	return s.exec(ctx, "set channel pts", `INSERT INTO channel_state (channel_id, pts) VALUES (?, ?)
		ON CONFLICT (channel_id) DO UPDATE SET pts = excluded.pts`, channelID, pts)
}

// ForEachChannels calls f for every channel with saved state.
func (s *Store) ForEachChannels(ctx context.Context, _ int64, f func(context.Context, int64, int) error) error {
	rows, err := s.db.QueryContext(ctx, "SELECT channel_id, pts FROM channel_state ORDER BY channel_id")
	if err != nil {
		return fmt.Errorf("list channels: %w", err)
	}
	type channel struct {
		id  int64
		pts int
	}
	var channels []channel
	for rows.Next() {
		var c channel
		if err := rows.Scan(&c.id, &c.pts); err != nil {
			_ = rows.Close()
			return fmt.Errorf("list channels: %w", err)
		}
		channels = append(channels, c)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return fmt.Errorf("list channels: %w", err)
	}
	// Call f after closing rows: the single connection must be free for f's own queries.
	for _, c := range channels {
		if err := f(ctx, c.id, c.pts); err != nil {
			return err
		}
	}
	return nil
}

// SetChannelAccessHash saves a channel access hash in the shared peer table.
func (s *Store) SetChannelAccessHash(ctx context.Context, _, channelID, hash int64) error {
	return s.putPeer(ctx, channelPrefix, channelID, hash)
}

// GetChannelAccessHash returns a channel access hash from the shared peer table.
func (s *Store) GetChannelAccessHash(ctx context.Context, _, channelID int64) (int64, bool, error) {
	return s.getPeer(ctx, channelPrefix, channelID)
}

// SetUserAccessHash saves a user access hash in the shared peer table.
func (s *Store) SetUserAccessHash(ctx context.Context, _, userID, hash int64) error {
	return s.putPeer(ctx, usersPrefix, userID, hash)
}

// GetUserAccessHash returns a user access hash from the shared peer table.
func (s *Store) GetUserAccessHash(ctx context.Context, _, userID int64) (int64, bool, error) {
	return s.getPeer(ctx, usersPrefix, userID)
}

func (s *Store) exec(ctx context.Context, op, query string, args ...any) error {
	if _, err := s.db.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	return nil
}
