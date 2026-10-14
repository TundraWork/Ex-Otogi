package telegram

import (
	"context"
	"sync"
	"testing"
	"time"

	telegramstore "github.com/TundraWork/Ex-Otogi/internal/adapter/telegram/store"
	"github.com/TundraWork/Ex-Otogi/internal/kernel"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// effectModule replies once per event; it waits on hold (when set) after
// sending, so a test can control when the kernel reports completion.
type effectModule struct{ hold chan struct{} }

func (*effectModule) Descriptor() otogi.ModuleDescriptor {
	return otogi.ModuleDescriptor{ID: "effect", Subscribes: []otogi.EventAction{otogi.ActionMessageEdited}}
}

func (m *effectModule) Handle(ctx context.Context, _ otogi.Event, inv otogi.Invocation) error {
	if err := inv.Send(ctx, "reply", otogi.TextMessage("reply")); err != nil {
		return err
	}
	if m.hold != nil {
		<-m.hold
	}
	return nil
}

type effectMessenger struct {
	mu      sync.Mutex
	effects []otogi.OutboundMessage
	notify  chan struct{}
}

func (m *effectMessenger) Send(_ context.Context, message otogi.OutboundMessage) error {
	m.mu.Lock()
	m.effects = append(m.effects, message)
	m.mu.Unlock()
	m.notify <- struct{}{}
	return nil
}

// cancelAfterSubmit cancels the dispatch attempt right after admission,
// simulating an acknowledgement lost to shutdown.
type cancelAfterSubmit struct {
	kernel *kernel.Kernel
	cancel context.CancelFunc
}

func (a *cancelAfterSubmit) Submit(ctx context.Context, event otogi.Event, done func(otogi.Outcome)) error {
	err := a.kernel.Submit(ctx, event, done)
	if err == nil {
		a.cancel()
	}
	return err
}

func TestDispatcherKernelReplayKeepsEventAndEffectIDsStable(t *testing.T) {
	messenger := &effectMessenger{notify: make(chan struct{}, 2)}
	module := &effectModule{hold: make(chan struct{})}
	k, err := kernel.New(kernel.Config{}, []otogi.Module{module}, nil, messenger, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := k.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = k.Stop(context.Background()) }()
	decode := func(context.Context, []byte) (otogi.Event, error) { return dispatchInput(t, "chat:1"), nil }

	row := dispatchRow(t, "018f47a6-f5f7-7cac-9000-333333333333", "chat:1", []byte("rich"))
	store := &dispatchStore{rows: []telegramstore.InboxRow{row}}
	firstCtx, cancel := context.WithCancel(context.Background())
	d := NewInboxDispatcher(store, &cancelAfterSubmit{kernel: k, cancel: cancel}, decode, dispatchConfig(time.Unix(600, 0).UTC()))
	if worked, err := d.dispatchOne(firstCtx); !worked || err != nil {
		t.Fatalf("first dispatch=%v,%v", worked, err)
	}
	if len(store.complete) != 0 || len(store.released) != 1 {
		t.Fatalf("lost acknowledgement retired row: complete=%v released=%v", store.complete, store.released)
	}
	<-messenger.notify
	close(module.hold)

	store.rows = []telegramstore.InboxRow{row}
	d = NewInboxDispatcher(store, k, decode, dispatchConfig(time.Unix(700, 0).UTC()))
	if worked, err := d.dispatchOne(context.Background()); !worked || err != nil {
		t.Fatalf("replay=%v,%v", worked, err)
	}
	<-messenger.notify
	if len(store.complete) != 1 {
		t.Fatalf("replayed row not completed: %v", store.complete)
	}
	messenger.mu.Lock()
	defer messenger.mu.Unlock()
	first, second := messenger.effects[0], messenger.effects[1]
	if first.Event != second.Event || first.Event != deterministicInboxEventID(row.ID) {
		t.Fatalf("event IDs differ: %s %s", first.Event, second.Event)
	}
	if first.Key != second.Key {
		t.Fatalf("effect keys differ: %s %s", first.Key, second.Key)
	}
}
