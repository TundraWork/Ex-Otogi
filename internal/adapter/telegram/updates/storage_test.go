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
	if err != nil || !found || state.Pts != 9 {
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
