package updates

import (
	"context"
	"fmt"

	gotdupdates "github.com/gotd/td/telegram/updates"
)

type monitoredStateStorage struct {
	next   gotdupdates.StateStorage
	gaps   GapStore
	health *Health
}

type consistencyReader interface {
	Consistency(context.Context, string) (int64, bool, string, error)
}

func (s monitoredStateStorage) consistency() (consistencyReader, bool) {
	if reader, ok := s.next.(consistencyReader); ok {
		return reader, true
	}
	reader, ok := s.gaps.(consistencyReader)
	return reader, ok
}

func (s monitoredStateStorage) fail(ctx context.Context, box, operation string, position int64, err error) error {
	if err == nil {
		return nil
	}
	recordErr := s.gaps.RecordGap(ctx, box, position, operation+": "+err.Error())
	s.health.degrade(DegradedReason{Box: box, Position: position, Reason: operation, Err: recordErr})
	return err
}
func (s monitoredStateStorage) GetState(ctx context.Context, userID int64) (gotdupdates.State, bool, error) {
	state, found, err := s.next.GetState(ctx, userID)
	if err != nil || !found {
		return state, found, err
	}
	if reader, ok := s.consistency(); ok {
		marker, degraded, reason, markerErr := reader.Consistency(ctx, "common")
		if markerErr != nil {
			return state, found, s.fail(ctx, "common", "load consistency marker", int64(state.Pts), markerErr)
		}
		if degraded {
			s.health.degrade(DegradedReason{Box: "common", Position: marker, Reason: reason})
		} else if int64(state.Pts) > marker {
			mismatch := fmt.Errorf("cursor pts %d exceeds normalized position %d", state.Pts, marker)
			_ = s.fail(ctx, "common", "cursor ahead of consistency marker", int64(state.Pts), mismatch)
		}
	}
	return state, found, nil
}
func (s monitoredStateStorage) SetState(ctx context.Context, userID int64, value gotdupdates.State) error {
	return s.fail(ctx, "common", "set state", int64(value.Pts), s.next.SetState(ctx, userID, value))
}
func (s monitoredStateStorage) SetPts(ctx context.Context, userID int64, value int) error {
	return s.fail(ctx, "common", "set pts", int64(value), s.next.SetPts(ctx, userID, value))
}
func (s monitoredStateStorage) SetQts(ctx context.Context, userID int64, value int) error {
	return s.fail(ctx, "common", "set qts", int64(value), s.next.SetQts(ctx, userID, value))
}
func (s monitoredStateStorage) SetDate(ctx context.Context, userID int64, value int) error {
	return s.fail(ctx, "common", "set date", int64(value), s.next.SetDate(ctx, userID, value))
}
func (s monitoredStateStorage) SetSeq(ctx context.Context, userID int64, value int) error {
	return s.fail(ctx, "common", "set seq", int64(value), s.next.SetSeq(ctx, userID, value))
}
func (s monitoredStateStorage) SetDateSeq(ctx context.Context, userID int64, date, seq int) error {
	return s.fail(ctx, "common", "set date and seq", int64(seq), s.next.SetDateSeq(ctx, userID, date, seq))
}
func (s monitoredStateStorage) GetChannelPts(ctx context.Context, userID, channelID int64) (int, bool, error) {
	pts, found, err := s.next.GetChannelPts(ctx, userID, channelID)
	if err != nil || !found {
		return pts, found, err
	}
	box := fmt.Sprintf("channel:%d", channelID)
	if reader, ok := s.consistency(); ok {
		marker, degraded, reason, markerErr := reader.Consistency(ctx, box)
		if markerErr != nil {
			return pts, found, s.fail(ctx, box, "load consistency marker", int64(pts), markerErr)
		}
		if degraded {
			s.health.degrade(DegradedReason{Box: box, Position: marker, Reason: reason})
		} else if int64(pts) > marker {
			mismatch := fmt.Errorf("cursor pts %d exceeds normalized position %d", pts, marker)
			_ = s.fail(ctx, box, "cursor ahead of consistency marker", int64(pts), mismatch)
		}
	}
	return pts, found, nil
}
func (s monitoredStateStorage) SetChannelPts(ctx context.Context, userID, channelID int64, value int) error {
	return s.fail(ctx, fmt.Sprintf("channel:%d", channelID), "set channel pts", int64(value), s.next.SetChannelPts(ctx, userID, channelID, value))
}
func (s monitoredStateStorage) ForEachChannels(ctx context.Context, userID int64, f func(context.Context, int64, int) error) error {
	return s.next.ForEachChannels(ctx, userID, f)
}
