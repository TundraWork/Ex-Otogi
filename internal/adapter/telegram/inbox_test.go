package telegram

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	gotdtelegram "github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/updates"
	"github.com/gotd/td/tg"
)

// recordingState is an in-memory updates.StateStorage that logs position writes.
type recordingState struct {
	mu  sync.Mutex
	log *[]string
}

func (s *recordingState) record(entry string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	*s.log = append(*s.log, entry)
}
func (s *recordingState) GetState(context.Context, int64) (updates.State, bool, error) {
	return updates.State{}, false, nil
}
func (s *recordingState) SetState(_ context.Context, _ int64, st updates.State) error {
	s.record("set_state")
	return nil
}
func (s *recordingState) SetPts(_ context.Context, _ int64, pts int) error {
	s.record("set_pts")
	return nil
}
func (s *recordingState) SetQts(context.Context, int64, int) error  { return nil }
func (s *recordingState) SetDate(context.Context, int64, int) error { return nil }
func (s *recordingState) SetSeq(context.Context, int64, int) error  { return nil }
func (s *recordingState) SetDateSeq(context.Context, int64, int, int) error {
	return nil
}
func (s *recordingState) GetChannelPts(context.Context, int64, int64) (int, bool, error) {
	return 0, false, nil
}
func (s *recordingState) SetChannelPts(context.Context, int64, int64, int) error { return nil }
func (s *recordingState) ForEachChannels(context.Context, int64, func(context.Context, int64, int) error) error {
	return nil
}

// The inbox's durability rests on gotd calling the handler before saving pts.
// This pins that behavior of the real update manager, so a gotd upgrade that
// changes it fails here.
func TestGotdSavesPositionOnlyAfterHandlerReturns(t *testing.T) {
	var log []string
	state := &recordingState{log: &log}
	handler := gotdtelegram.UpdateHandlerFunc(func(context.Context, tg.UpdatesClass) error {
		state.record("handle")
		return nil
	})
	manager := updates.New(updates.Config{Handler: handler, Storage: state})
	api := tg.NewClient(rpc(func(_ context.Context, request bin.Encoder) (bin.Encoder, error) {
		if _, ok := request.(*tg.UpdatesGetStateRequest); ok {
			return &tg.UpdatesState{Pts: 1, Date: 1}, nil
		}
		return nil, unexpected(request)
	}))
	ctx, cancel := context.WithCancel(context.Background())
	started, stopped := make(chan struct{}), make(chan error, 1)
	go func() {
		stopped <- manager.Run(ctx, api, 42, updates.AuthOptions{OnStart: func(context.Context) { close(started) }})
	}()
	defer func() {
		cancel()
		<-stopped
	}()
	<-started

	update := &tg.UpdateNewMessage{Message: newMessage(1, &tg.PeerUser{UserID: 2}, "x"), Pts: 2, PtsCount: 1}
	// Real batches carry the users they reference; without them gotd treats
	// the update as incomplete and falls back to getDifference.
	users := []tg.UserClass{&tg.User{ID: 2, AccessHash: 20}, &tg.User{ID: 5, AccessHash: 50}}
	if err := manager.Handle(ctx, &tg.Updates{Updates: []tg.UpdateClass{update}, Users: users, Date: 1}); err != nil {
		t.Fatal(err)
	}
	snapshot := func() []string {
		state.mu.Lock()
		defer state.mu.Unlock()
		return slices.Clone(log)
	}
	eventually(t, "pts to be saved", func() bool { return slices.Contains(snapshot(), "set_pts") })
	got := snapshot()
	if slices.Index(got, "handle") < 0 || slices.Index(got, "handle") > slices.Index(got, "set_pts") {
		t.Fatalf("gotd saved pts before calling the handler: %v", got)
	}
}

type failingInbox struct{}

func (failingInbox) InsertInbox(context.Context, string, []byte, time.Time) (bool, error) {
	return false, errors.New("disk full")
}

func TestInboxHandlerPersistsRoutedUpdatesOnce(t *testing.T) {
	store := openStore(t)
	wake := make(chan struct{}, 1)
	h := &inboxHandler{store: store, guard: &positionGuard{StateStorage: store}, wake: wake, log: quiet, now: time.Now, fail: func(err error) { t.Errorf("unexpected failure: %v", err) }}
	batch := &tg.Updates{Updates: []tg.UpdateClass{
		&tg.UpdateNewMessage{Message: newMessage(1, &tg.PeerUser{UserID: 2}, "a"), Pts: 5, PtsCount: 1},
		&tg.UpdateUserTyping{UserID: 2, Action: &tg.SendMessageTypingAction{}},
		&tg.UpdateDeleteChannelMessages{ChannelID: 9, Messages: []int{1}, Pts: 3, PtsCount: 1},
	}}
	for range 2 { // gotd re-delivery after a crash
		if err := h.Handle(context.Background(), batch); err != nil {
			t.Fatal(err)
		}
	}
	if n := pendingCount(t, store); n != 2 {
		t.Fatalf("pending rows = %d, want 2 (routed, deduplicated)", n)
	}
	select {
	case <-wake:
	default:
		t.Fatal("dispatcher was not woken")
	}
}

func TestInboxWriteFailureStopsPositionWrites(t *testing.T) {
	store := openStore(t)
	guard := &positionGuard{StateStorage: store}
	var failure error
	h := &inboxHandler{store: failingInbox{}, guard: guard, wake: make(chan struct{}, 1), log: quiet, now: time.Now, fail: func(err error) { failure = err }}
	update := &tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateNewMessage{Message: newMessage(1, &tg.PeerUser{UserID: 2}, "a"), Pts: 5, PtsCount: 1}}}

	if err := h.Handle(context.Background(), update); err == nil || failure == nil {
		t.Fatalf("Handle = %v, failure = %v; want both set", err, failure)
	}
	ctx := context.Background()
	for name, write := range map[string]func() error{
		"SetPts":        func() error { return guard.SetPts(ctx, 1, 6) },
		"SetState":      func() error { return guard.SetState(ctx, 1, updates.State{Pts: 6}) },
		"SetChannelPts": func() error { return guard.SetChannelPts(ctx, 1, 9, 6) },
		"SetDateSeq":    func() error { return guard.SetDateSeq(ctx, 1, 1, 1) },
	} {
		if err := write(); !errors.Is(err, errInboxFailed) {
			t.Errorf("%s after failure = %v, want errInboxFailed", name, err)
		}
	}
	if _, found, _ := store.GetState(ctx, 1); found {
		t.Fatal("position was saved after the inbox failed")
	}
	if err := h.Handle(ctx, update); !errors.Is(err, errInboxFailed) {
		t.Fatalf("later Handle = %v, want errInboxFailed", err)
	}
}
