package updates

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/gotd/td/telegram"
	gotdupdates "github.com/gotd/td/telegram/updates"
	"github.com/gotd/td/tg"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

type gapStore struct {
	records []string
	err     error
}

func (s *gapStore) RecordGap(_ context.Context, box string, position int64, reason string) error {
	s.records = append(s.records, fmt.Sprintf("%s:%d:%s", box, position, reason))
	return s.err
}

func TestGapCallbacksPersistEveryGotdUnrecoverableCondition(t *testing.T) {
	store := &gapStore{}
	health := NewHealth()
	callbacks := GapCallbacks(store, health)
	callbacks.OnLoadUserStateFailed()
	callbacks.OnLoadChannelStateFailed(10)
	callbacks.OnTooLong()
	callbacks.OnChannelTooLong(11)
	callbacks.OnChannelInaccessible(12)
	want := "[common:0:load user state failed channel:10:0:load channel state failed common:0:difference too long channel:11:0:channel difference too long channel:12:0:channel inaccessible]"
	if got := fmt.Sprint(store.records); got != want {
		t.Fatalf("records = %s", got)
	}
	ready, reasons := health.Ready()
	if ready || len(reasons) != 5 {
		t.Fatalf("health ready=%v reasons=%v", ready, reasons)
	}
}

func TestGapCallbacksRetainStorageFailureInHealth(t *testing.T) {
	store := &gapStore{err: errors.New("disk full")}
	health := NewHealth()
	GapCallbacks(store, health).OnTooLong()
	ready, reasons := health.Ready()
	if ready || len(reasons) != 1 || reasons[0].Err == nil {
		t.Fatalf("health ready=%v reasons=%+v", ready, reasons)
	}
}

type fakeAPI struct {
	state      *tg.UpdatesState
	stateCalls int
}

func (f *fakeAPI) UpdatesGetState(context.Context) (*tg.UpdatesState, error) {
	f.stateCalls++
	return f.state, nil
}
func (*fakeAPI) UpdatesGetDifference(context.Context, *tg.UpdatesGetDifferenceRequest) (tg.UpdatesDifferenceClass, error) {
	return &tg.UpdatesDifferenceEmpty{}, nil
}
func (*fakeAPI) UpdatesGetChannelDifference(context.Context, *tg.UpdatesGetChannelDifferenceRequest) (tg.UpdatesChannelDifferenceClass, error) {
	return &tg.UpdatesChannelDifferenceEmpty{}, nil
}

type memState struct {
	state gotdupdates.State
	found bool
}

func (m *memState) GetState(context.Context, int64) (gotdupdates.State, bool, error) {
	return m.state, m.found, nil
}
func (m *memState) SetState(_ context.Context, _ int64, s gotdupdates.State) error {
	m.state, m.found = s, true
	return nil
}
func (*memState) SetPts(context.Context, int64, int) error          { return nil }
func (*memState) SetQts(context.Context, int64, int) error          { return nil }
func (*memState) SetDate(context.Context, int64, int) error         { return nil }
func (*memState) SetSeq(context.Context, int64, int) error          { return nil }
func (*memState) SetDateSeq(context.Context, int64, int, int) error { return nil }
func (*memState) GetChannelPts(context.Context, int64, int64) (int, bool, error) {
	return 0, false, nil
}
func (*memState) SetChannelPts(context.Context, int64, int64, int) error { return nil }
func (*memState) ForEachChannels(context.Context, int64, func(context.Context, int64, int) error) error {
	return nil
}

func TestManagerRunOpensGateOnlyAfterStartupStateLoad(t *testing.T) {
	journal := &memoryJournal{}
	recorder := &recordingUpdates{}
	storage := &memState{}
	api := &fakeAPI{state: &tg.UpdatesState{Pts: 1, Qts: 2, Date: 3, Seq: 4}}
	manager, gate, _, _ := NewManager(ManagerConfig{APIStorage: storage, Journal: journal, Downstream: telegram.UpdateHandlerFunc(recorder.Handle), GapStore: &gapStore{}, Health: NewHealth()})
	if err := gate.Handle(context.Background(), packet(7)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- RunManager(ctx, manager, gate, api, 42, gotdupdates.AuthOptions{}) }()
	deadline := time.After(time.Second)
	for len(recorder.snapshot()) == 0 {
		select {
		case <-deadline:
			t.Fatal("gate did not replay")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if got := recorder.snapshot(); api.stateCalls != 1 || fmt.Sprint(got) != "[7]" {
		t.Fatalf("state calls=%d updates=%v", api.stateCalls, got)
	}
	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestManagerRunReplaysSixtyFourRowsWithoutDeadlock(t *testing.T) {
	journal := &memoryJournal{}
	recorder := &recordingUpdates{}
	manager, gate, _, _ := NewManager(ManagerConfig{APIStorage: &memState{}, Journal: journal, Downstream: telegram.UpdateHandlerFunc(recorder.Handle), GapStore: &gapStore{}, Health: NewHealth()})
	for i := 1; i <= StartupJournalLimit; i++ {
		if err := gate.Handle(context.Background(), packet(i)); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- RunManager(ctx, manager, gate, &fakeAPI{state: &tg.UpdatesState{}}, 42, gotdupdates.AuthOptions{})
	}()
	deadline := time.After(2 * time.Second)
	for len(recorder.snapshot()) < StartupJournalLimit {
		select {
		case <-deadline:
			t.Fatalf("replayed %d rows", len(recorder.snapshot()))
		default:
			time.Sleep(time.Millisecond)
		}
	}
	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestManagerRunPropagatesAsynchronousGateReplayFailure(t *testing.T) {
	journal := &memoryJournal{}
	manager, gate, _, _ := NewManager(ManagerConfig{APIStorage: &memState{}, Journal: journal, Downstream: telegram.UpdateHandlerFunc(func(context.Context, tg.UpdatesClass) error { return nil }), GapStore: &gapStore{}, Health: NewHealth()})
	for i := 1; i <= StartupJournalLimit+1; i++ {
		journal.next++
		journal.rows = func() map[int64][]byte {
			if journal.rows == nil {
				journal.rows = make(map[int64][]byte)
			}
			return journal.rows
		}()
		journal.rows[journal.next] = encodePacket(t, packet(i))
		journal.pending = append(journal.pending, journal.next)
	}
	err := RunManager(context.Background(), manager, gate, &fakeAPI{state: &tg.UpdatesState{}}, 42, gotdupdates.AuthOptions{})
	if !errors.Is(err, ErrStartupOverflow) {
		t.Fatalf("RunManager error = %v", err)
	}
}

func TestConfigureOptionsUsesSharedGateAndRequiredMiddlewareOrder(t *testing.T) {
	journal := &memoryJournal{}
	manager, gate, _, _ := NewManager(ManagerConfig{APIStorage: &memState{}, Journal: journal, Downstream: telegram.UpdateHandlerFunc(func(context.Context, tg.UpdatesClass) error { return nil }), GapStore: &gapStore{}, Health: NewHealth()})
	var options telegram.Options
	ConfigureOptions(&options, gate, manager)
	if options.UpdateHandler != gate {
		t.Fatalf("UpdateHandler = %T", options.UpdateHandler)
	}
	if len(options.Middlewares) != 2 {
		t.Fatalf("middleware count = %d", len(options.Middlewares))
	}
}

func TestNewManagerUsesCallerSharedHealth(t *testing.T) {
	health := NewHealth()
	_, _, callbacks, returned := NewManager(ManagerConfig{APIStorage: &memState{}, Journal: &memoryJournal{}, Downstream: telegram.UpdateHandlerFunc(func(context.Context, tg.UpdatesClass) error { return nil }), GapStore: &gapStore{}, Health: health})
	if returned != health {
		t.Fatal("manager replaced shared health")
	}
	callbacks.OnTooLong()
	if ready, reasons := health.Ready(); ready || len(reasons) != 1 {
		t.Fatalf("shared health ready=%v reasons=%+v", ready, reasons)
	}
}

type emptyDifferenceAPI struct {
	processed chan struct{}
	calls     int
}

func (*emptyDifferenceAPI) UpdatesGetState(context.Context) (*tg.UpdatesState, error) {
	return &tg.UpdatesState{}, nil
}
func (a *emptyDifferenceAPI) UpdatesGetDifference(context.Context, *tg.UpdatesGetDifferenceRequest) (tg.UpdatesDifferenceClass, error) {
	a.calls++
	if a.calls == 2 {
		close(a.processed)
	}
	return &tg.UpdatesDifferenceEmpty{}, nil
}
func (*emptyDifferenceAPI) UpdatesGetChannelDifference(context.Context, *tg.UpdatesGetChannelDifferenceRequest) (tg.UpdatesChannelDifferenceClass, error) {
	return &tg.UpdatesChannelDifferenceEmpty{}, nil
}

func TestManagerRunCompletesTooLongJournalOnlyAfterDurableResyncMarker(t *testing.T) {
	journal := &memoryJournal{}
	health := NewHealth()
	manager, gate, _, _ := NewManager(ManagerConfig{APIStorage: &memState{found: true}, Journal: journal, Downstream: telegram.UpdateHandlerFunc(func(context.Context, tg.UpdatesClass) error { return nil }), GapStore: journal, Health: health})
	if err := gate.Handle(context.Background(), &tg.UpdatesTooLong{}); err != nil {
		t.Fatal(err)
	}
	api := &emptyDifferenceAPI{processed: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- RunManager(ctx, manager, gate, api, 42, gotdupdates.AuthOptions{}) }()
	select {
	case <-api.processed:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	ids, err := journal.StartupJournalIDs(context.Background(), StartupJournalLimit)
	if err != nil || len(ids) != 0 || journal.normalized != 1 || len(journal.gaps) != 1 {
		t.Fatalf("pending=%v normalized=%d gaps=%v err=%v", ids, journal.normalized, journal.gaps, err)
	}
	restart := NewJournalGate(journal, telegram.UpdateHandlerFunc(func(context.Context, tg.UpdatesClass) error { t.Fatal("normalized journal replayed"); return nil }))
	if err := restart.Open(context.Background()); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestNoOutputGapPersistenceFailureLeavesJournalPending(t *testing.T) {
	journal := &memoryJournal{gapErr: errors.New("disk full")}
	gate := newJournalGate(journal, telegram.UpdateHandlerFunc(func(context.Context, tg.UpdatesClass) error { return nil }), newJournalCorrelation())
	gate.open = true
	if err := gate.Handle(context.Background(), &tg.UpdatesTooLong{}); !errors.Is(err, journal.gapErr) {
		t.Fatalf("error=%v", err)
	}
	ids, err := journal.StartupJournalIDs(context.Background(), StartupJournalLimit)
	if err != nil || len(ids) != 1 || journal.normalized != 0 {
		t.Fatalf("pending=%v normalized=%d err=%v", ids, journal.normalized, err)
	}
}
