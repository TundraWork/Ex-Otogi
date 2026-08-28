package updates

import (
	"context"
	"errors"
	"testing"

	gotdupdates "github.com/gotd/td/telegram/updates"
)

type failingState struct {
	memState
	err error
}

func (s *failingState) SetPts(context.Context, int64, int) error { return s.err }

func TestStateStorageFailureDurablyDegradesReadiness(t *testing.T) {
	cause := errors.New("sqlite full")
	gaps := &gapStore{}
	health := NewHealth()
	storage := monitoredStateStorage{next: &failingState{err: cause}, gaps: gaps, health: health}
	var contract gotdupdates.StateStorage = storage
	if err := contract.SetPts(context.Background(), 42, 9); !errors.Is(err, cause) {
		t.Fatalf("SetPts error = %v", err)
	}
	if ready, reasons := health.Ready(); ready || len(reasons) != 1 {
		t.Fatalf("health = %v %+v", ready, reasons)
	}
	if len(gaps.records) != 1 {
		t.Fatalf("gaps = %v", gaps.records)
	}
}

type consistencyState struct {
	memState
	position int64
}

func (s *consistencyState) Consistency(context.Context, string) (int64, bool, string, error) {
	return s.position, false, "", nil
}

func TestStateStorageLoadAheadOfConsistencyMarkerDurablyDegradesReadiness(t *testing.T) {
	gaps := &gapStore{}
	health := NewHealth()
	next := &consistencyState{memState: memState{state: gotdupdates.State{Pts: 9}, found: true}, position: 7}
	storage := monitoredStateStorage{next: next, gaps: gaps, health: health}
	state, found, err := storage.GetState(context.Background(), 42)
	if err == nil || found || state.Pts != 9 {
		t.Fatalf("state=%+v found=%v err=%v", state, found, err)
	}
	if ready, reasons := health.Ready(); ready || len(reasons) != 1 || reasons[0].Reason != "cursor ahead of consistency marker" {
		t.Fatalf("health=%v reasons=%+v", ready, reasons)
	}
	if len(gaps.records) != 1 {
		t.Fatalf("gaps=%v", gaps.records)
	}
}

type degradedConsistencyState struct{ memState }

func (*degradedConsistencyState) Consistency(context.Context, string) (int64, bool, string, error) {
	return 9, true, "difference too long", nil
}

func TestStateStorageRestoresPersistedDegradedReadiness(t *testing.T) {
	gaps := &gapStore{}
	health := NewHealth()
	next := &degradedConsistencyState{memState: memState{state: gotdupdates.State{Pts: 9}, found: true}}
	storage := monitoredStateStorage{next: next, gaps: gaps, health: health}
	if _, _, err := storage.GetState(context.Background(), 42); err != nil {
		t.Fatal(err)
	}
	if ready, reasons := health.Ready(); ready || len(reasons) != 1 || reasons[0].Reason != "difference too long" {
		t.Fatalf("health=%v reasons=%+v", ready, reasons)
	}
}

type channelState struct {
	memState
	position  int64
	degraded  bool
	reason    string
	forwarded bool
}

func (s *channelState) ForEachChannels(ctx context.Context, _ int64, f func(context.Context, int64, int) error) error {
	return f(ctx, 77, 9)
}

func (s *channelState) Consistency(context.Context, string) (int64, bool, string, error) {
	return s.position, s.degraded, s.reason, nil
}

func TestStateStorageForEachChannelsRejectsCursorAheadOfMarker(t *testing.T) {
	next := &channelState{position: 7}
	gaps := &gapStore{}
	health := NewHealth()
	storage := monitoredStateStorage{next: next, gaps: gaps, health: health}
	called := false
	err := storage.ForEachChannels(context.Background(), 42, func(context.Context, int64, int) error { called = true; return nil })
	if err == nil || called {
		t.Fatalf("err=%v called=%v", err, called)
	}
	if ready, _ := health.Ready(); ready || len(gaps.records) != 1 {
		t.Fatalf("ready=%v gaps=%v", ready, gaps.records)
	}
}

func TestStateStorageForEachChannelsRestoresDegradedStateBeforeForwarding(t *testing.T) {
	next := &channelState{position: 9, degraded: true, reason: "channel inaccessible"}
	health := NewHealth()
	storage := monitoredStateStorage{next: next, gaps: &gapStore{}, health: health}
	called := false
	if err := storage.ForEachChannels(context.Background(), 42, func(context.Context, int64, int) error { called = true; return nil }); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("validated channel was not forwarded")
	}
	if ready, reasons := health.Ready(); ready || len(reasons) != 1 || reasons[0].Reason != "channel inaccessible" {
		t.Fatalf("ready=%v reasons=%+v", ready, reasons)
	}
}
