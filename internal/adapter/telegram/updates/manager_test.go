package updates

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"

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
	go func() { done <- RunManager(ctx, manager, gate, api, 42, gotdupdates.AuthOptions{}, nil) }()
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
		done <- RunManager(ctx, manager, gate, &fakeAPI{state: &tg.UpdatesState{}}, 42, gotdupdates.AuthOptions{}, nil)
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
	err := RunManager(context.Background(), manager, gate, &fakeAPI{state: &tg.UpdatesState{}}, 42, gotdupdates.AuthOptions{}, nil)
	if !errors.Is(err, ErrStartupOverflow) {
		t.Fatalf("RunManager error = %v", err)
	}
}

func TestManagerReadySignalsOnlyAfterGateReplay(t *testing.T) {
	release := make(chan struct{})
	journal := &memoryJournal{startupWait: release}
	manager, gate, _, _ := NewManager(ManagerConfig{APIStorage: &memState{}, Journal: journal, Downstream: telegram.UpdateHandlerFunc(func(context.Context, tg.UpdatesClass) error { return nil }), GapStore: &gapStore{}, Health: NewHealth()})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- RunManager(ctx, manager, gate, &fakeAPI{state: &tg.UpdatesState{}}, 42, gotdupdates.AuthOptions{}, func() { close(ready) })
	}()
	select {
	case <-ready:
		t.Fatal("ready before gate replay completed")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("ready not signaled")
	}
	cancel()
	<-done
}

func TestManagerGateFailureDoesNotSignalReady(t *testing.T) {
	journal := &memoryJournal{}
	manager, gate, _, _ := NewManager(ManagerConfig{APIStorage: &memState{}, Journal: journal, Downstream: telegram.UpdateHandlerFunc(func(context.Context, tg.UpdatesClass) error { return nil }), GapStore: &gapStore{}, Health: NewHealth()})
	for i := 0; i <= StartupJournalLimit; i++ {
		journal.next++
		if journal.rows == nil {
			journal.rows = map[int64][]byte{}
		}
		journal.rows[journal.next] = encodePacket(t, packet(i))
		journal.pending = append(journal.pending, journal.next)
	}
	ready := false
	err := RunManager(context.Background(), manager, gate, &fakeAPI{state: &tg.UpdatesState{}}, 42, gotdupdates.AuthOptions{}, func() { ready = true })
	if !errors.Is(err, ErrStartupOverflow) || ready {
		t.Fatalf("error=%v ready=%v", err, ready)
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

func TestHealthPublishesFirstDegradationOnceWithoutBlocking(t *testing.T) {
	health := NewHealth()
	if err := health.arm(); err != nil {
		t.Fatal(err)
	}
	first := errors.New("sqlite full")
	health.degrade(DegradedReason{Box: "common", Position: 4, Reason: "capture normalized update", Err: first})
	for i := 0; i < 100; i++ {
		health.degrade(DegradedReason{Box: "common", Position: int64(i + 5), Reason: "later failure"})
	}
	select {
	case err := <-health.Failed():
		if !errors.Is(err, first) || !strings.Contains(err.Error(), "capture normalized update") {
			t.Fatalf("failure=%v", err)
		}
	default:
		t.Fatal("degradation was not published")
	}
	select {
	case err := <-health.Failed():
		t.Fatalf("second failure=%v", err)
	default:
	}
}

func TestManagerRunReturnsPostReadyHealthFailure(t *testing.T) {
	health := NewHealth()
	manager, gate, _, _ := NewManager(ManagerConfig{APIStorage: &memState{found: true}, Journal: &memoryJournal{}, Downstream: telegram.UpdateHandlerFunc(func(context.Context, tg.UpdatesClass) error { return nil }), GapStore: &gapStore{}, Health: health})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- RunManager(ctx, manager, gate, &fakeAPI{state: &tg.UpdatesState{}}, 42, gotdupdates.AuthOptions{}, func() { close(ready) })
	}()
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("manager not ready")
	}
	cause := errors.New("capture failed")
	health.degrade(DegradedReason{Box: "common", Position: 9, Reason: "capture normalized update", Err: cause})
	select {
	case err := <-done:
		if !errors.Is(err, cause) {
			t.Fatalf("manager error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("manager did not stop after degradation")
	}
}

type startupCaptureFailureStore struct {
	captureStore
	recordErr error
}

func (s *startupCaptureFailureStore) RecordGap(context.Context, string, int64, string) error {
	return s.recordErr
}

func TestManagerRunPublishesPreArmCaptureFailureBeforeReady(t *testing.T) {
	cause := errors.New("capture failed")
	recordErr := errors.New("record gap failed")
	journal := &memoryJournal{}
	health := NewHealth()
	store := &startupCaptureFailureStore{captureStore: captureStore{err: cause}, recordErr: recordErr}
	downstream := NewDownstream(store, testMapper(t), func(otogi.Event) ([]byte, error) { return []byte("payload"), nil }, health)
	if err := downstream.Handle(context.Background(), mappedBatch()); !errors.Is(err, cause) {
		t.Fatalf("startup capture error = %v", err)
	}
	manager, gate, _, _ := NewManager(ManagerConfig{APIStorage: &memState{found: true}, Journal: journal, Downstream: downstream, GapStore: journal, Health: health})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ready := make(chan struct{}, 1)
	err := RunManager(ctx, manager, gate, &fakeAPI{state: &tg.UpdatesState{}}, 42, gotdupdates.AuthOptions{}, func() { ready <- struct{}{} })
	if !errors.Is(err, cause) || !errors.Is(err, recordErr) {
		t.Fatalf("manager error = %v", err)
	}
	select {
	case <-ready:
		t.Fatal("ready signaled after startup capture failure")
	default:
	}
}

func TestHealthArmWaitsForInFlightStartupCaptureFailure(t *testing.T) {
	health := NewHealth()
	health.boundary.Lock()
	started := make(chan struct{})
	armed := make(chan error, 1)
	go func() {
		close(started)
		armed <- health.arm()
	}()
	<-started
	select {
	case err := <-armed:
		t.Fatalf("arm completed before capture boundary: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	cause := errors.New("capture failed")
	recordErr := errors.New("record gap failed")
	health.degradeCaptureWithinBoundary(DegradedReason{Box: "common", Position: 4, Reason: "capture normalized update", Cause: cause, Err: recordErr})
	health.boundary.Unlock()
	select {
	case err := <-armed:
		if !errors.Is(err, cause) || !errors.Is(err, recordErr) {
			t.Fatalf("arm error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("arm did not complete after capture boundary")
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
	gaps := &gapStore{}
	health := NewHealth()
	manager, gate, _, _ := NewManager(ManagerConfig{APIStorage: &memState{found: true}, Journal: journal, Downstream: telegram.UpdateHandlerFunc(func(context.Context, tg.UpdatesClass) error { return nil }), GapStore: gaps, Health: health})
	if err := gate.Handle(context.Background(), &tg.UpdatesTooLong{}); err != nil {
		t.Fatal(err)
	}
	api := &emptyDifferenceAPI{processed: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- RunManager(ctx, manager, gate, api, 42, gotdupdates.AuthOptions{}, nil) }()
	select {
	case <-api.processed:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	ids, err := journal.StartupJournalIDs(context.Background(), StartupJournalLimit)
	ready, reasons := health.Ready()
	if err != nil || len(ids) != 0 || journal.normalized != 1 || len(gaps.records) != 1 || ready || len(reasons) != 1 {
		t.Fatalf("pending=%v normalized=%d gaps=%v ready=%v reasons=%v err=%v", ids, journal.normalized, gaps.records, ready, reasons, err)
	}
	restart := NewJournalGate(journal, gaps, NewHealth(), telegram.UpdateHandlerFunc(func(context.Context, tg.UpdatesClass) error { t.Fatal("normalized journal replayed"); return nil }))
	if err := restart.Open(context.Background()); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestNoOutputGapPersistenceFailureDegradesHealthAndLeavesJournalPending(t *testing.T) {
	journal := &memoryJournal{}
	gaps := &gapStore{err: errors.New("disk full")}
	health := NewHealth()
	gate := newJournalGate(journal, gaps, health, telegram.UpdateHandlerFunc(func(context.Context, tg.UpdatesClass) error { return nil }), newJournalCorrelation())
	gate.open = true
	if err := gate.Handle(context.Background(), &tg.UpdatesTooLong{}); !errors.Is(err, gaps.err) {
		t.Fatalf("error=%v", err)
	}
	ids, err := journal.StartupJournalIDs(context.Background(), StartupJournalLimit)
	ready, reasons := health.Ready()
	if err != nil || len(ids) != 1 || journal.normalized != 0 || ready || len(reasons) != 1 || !errors.Is(reasons[0].Err, gaps.err) {
		t.Fatalf("pending=%v normalized=%d ready=%v reasons=%+v err=%v", ids, journal.normalized, ready, reasons, err)
	}
}
