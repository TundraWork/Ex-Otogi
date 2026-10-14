package telegram

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/gotd/td/tg"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/telegram/mapper"
	telegramstore "github.com/TundraWork/Ex-Otogi/internal/adapter/telegram/store"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

type dispatchHarness struct {
	store  *telegramstore.Store
	kernel *submitter
	wake   chan struct{}
}

func newDispatchHarness(t *testing.T) *dispatchHarness {
	return &dispatchHarness{store: openStore(t), kernel: newSubmitter(), wake: make(chan struct{}, 1)}
}

func (h *dispatchHarness) insert(t *testing.T, key string, u tg.UpdateClass) {
	t.Helper()
	if _, err := h.store.InsertInbox(context.Background(), key, encodeUpdate(t, u), time.Unix(100, 0)); err != nil {
		t.Fatal(err)
	}
}

// run starts a dispatcher and returns a function that stops it.
func (h *dispatchHarness) run(t *testing.T) func() {
	t.Helper()
	d := &dispatcher{
		store: h.store, kernel: h.kernel, wake: h.wake, poll: 10 * time.Millisecond, log: quiet, now: time.Now, record: func(string, string, any) {},
		mapper:  mapper.New(mapper.Config{Platform: Platform, Account: "main"}),
		pending: map[int64]*rowProgress{},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.run(ctx) }()
	return func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("dispatcher: %v", err)
		}
	}
}

func TestDispatcherSubmitsInOrderWithoutWaiting(t *testing.T) {
	h := newDispatchHarness(t)
	for i := 1; i <= 3; i++ {
		h.insert(t, fmt.Sprint(i), &tg.UpdateNewMessage{Message: newMessage(i, &tg.PeerUser{UserID: 2}, fmt.Sprint(i)), Pts: i, PtsCount: 1})
	}
	stop := h.run(t)
	defer stop()
	for i := 1; i <= 3; i++ { // none completed yet, yet all are submitted
		if e := h.kernel.next(t); e.Message.PlainText() != fmt.Sprint(i) || e.ID == "" {
			t.Fatalf("event %d = %+v", i, e)
		}
	}
	h.kernel.complete(1, otogi.Outcome{})
	eventually(t, "second row done", func() bool { return pendingCount(t, h.store) == 2 })

	h.insert(t, "4", &tg.UpdateNewMessage{Message: newMessage(4, &tg.PeerUser{UserID: 2}, "4"), Pts: 4, PtsCount: 1})
	h.wake <- struct{}{}
	if e := h.kernel.next(t); e.Message.PlainText() != "4" {
		t.Fatalf("woken dispatch = %+v", e)
	}
}

func TestDispatcherResolvesDeletionsAndWaitsForAllEvents(t *testing.T) {
	h := newDispatchHarness(t)
	h.kernel.auto = true
	h.insert(t, "a", &tg.UpdateNewMessage{Message: newMessage(10, &tg.PeerUser{UserID: 2}, "hi"), Pts: 1, PtsCount: 1})
	h.insert(t, "b", &tg.UpdateNewMessage{Message: newMessage(11, &tg.PeerChat{ChatID: 3}, "yo"), Pts: 2, PtsCount: 1})
	stop := h.run(t)
	h.kernel.next(t)
	h.kernel.next(t)
	eventually(t, "messages done", func() bool { return pendingCount(t, h.store) == 0 })
	stop()

	// One deletion update for two known messages and one unknown.
	h.kernel.auto = false
	h.insert(t, "c", &tg.UpdateDeleteMessages{Messages: []int{10, 11, 99}, Pts: 3, PtsCount: 3})
	stop = h.run(t)
	defer stop()
	first, second := h.kernel.next(t), h.kernel.next(t)
	if first.Action != otogi.ActionMessageDeleted || first.Conversation.ID != "user:2" || second.Conversation.ID != "chat:3" {
		t.Fatalf("deletions = %+v / %+v", first.Conversation, second.Conversation)
	}
	h.kernel.complete(2, otogi.Outcome{}) // runs the done callback inline
	if pendingCount(t, h.store) != 1 {
		t.Fatal("row marked done before all its events finished")
	}
	h.kernel.complete(3, otogi.Outcome{})
	eventually(t, "deletion row done", func() bool { return pendingCount(t, h.store) == 0 })
}

func TestDispatcherMarksUndecodableRowsFailed(t *testing.T) {
	h := newDispatchHarness(t)
	if _, err := h.store.InsertInbox(context.Background(), "bad", []byte{1, 2, 3}, time.Now()); err != nil {
		t.Fatal(err)
	}
	h.insert(t, "good", &tg.UpdateNewMessage{Message: newMessage(1, &tg.PeerUser{UserID: 2}, "ok"), Pts: 1, PtsCount: 1})
	stop := h.run(t)
	defer stop()
	if e := h.kernel.next(t); e.Message.PlainText() != "ok" {
		t.Fatalf("event after bad row = %+v", e)
	}
	if n := pendingCount(t, h.store); n != 1 { // the good row, still in flight
		t.Fatalf("pending = %d; the bad row should be failed", n)
	}
}

func TestDispatcherReplaysUnfinishedRowsWithStableIDs(t *testing.T) {
	h := newDispatchHarness(t)
	h.insert(t, "a", &tg.UpdateNewMessage{Message: newMessage(1, &tg.PeerUser{UserID: 2}, "a"), Pts: 1, PtsCount: 1})
	stop := h.run(t)
	first := h.kernel.next(t)
	// The kernel stopped before the event finished.
	h.kernel.complete(0, otogi.Outcome{EventID: first.ID, Err: otogi.ErrUnavailable})
	stop()
	if pendingCount(t, h.store) != 1 {
		t.Fatal("abandoned row was marked done")
	}

	stop = h.run(t) // next start
	defer stop()
	if replay := h.kernel.next(t); replay.ID != first.ID {
		t.Fatalf("replayed event ID %q, want %q", replay.ID, first.ID)
	}
}

func TestDispatcherStopsQuietlyWhenKernelRefuses(t *testing.T) {
	h := newDispatchHarness(t)
	h.kernel.err = fmt.Errorf("draining: %w", otogi.ErrUnavailable)
	h.insert(t, "a", &tg.UpdateNewMessage{Message: newMessage(1, &tg.PeerUser{UserID: 2}, "a"), Pts: 1, PtsCount: 1})
	d := &dispatcher{store: h.store, kernel: h.kernel, wake: h.wake, poll: time.Hour, log: quiet, now: time.Now, record: func(string, string, any) {},
		mapper: mapper.New(mapper.Config{Platform: Platform, Account: "main"}), pending: map[int64]*rowProgress{}}
	if err := d.run(context.Background()); err != nil {
		t.Fatalf("run = %v, want nil", err)
	}
	if pendingCount(t, h.store) != 1 {
		t.Fatal("refused row was not left pending")
	}
}
