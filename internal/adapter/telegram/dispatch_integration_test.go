package telegram

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"

	telegramstore "github.com/TundraWork/Ex-Otogi/internal/adapter/telegram/store"
	"github.com/TundraWork/Ex-Otogi/internal/kernel"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

type effectModule struct {
	descriptor otogi.ModuleDescriptor
}

func newEffectModule(t *testing.T) *effectModule {
	t.Helper()
	id, _ := otogi.NewModuleID("effect")
	descriptor, err := otogi.NewModuleDescriptor(id, []otogi.EventAction{otogi.ActionMessageEdited})
	if err != nil {
		t.Fatal(err)
	}
	return &effectModule{descriptor: descriptor}
}
func (m *effectModule) Descriptor() otogi.ModuleDescriptor { return m.descriptor }
func (m *effectModule) Handle(ctx context.Context, _ otogi.Event, invocation otogi.Invocation) otogi.Result {
	partID, _ := otogi.NewPartID("reply")
	part, _ := otogi.NewTextPart(partID, "reply", nil, nil, nil)
	message, _ := otogi.NewMessage([]otogi.PartID{partID}, []otogi.Part{part}, nil, nil)
	if err := invocation.Send(ctx, "reply", message); err != nil {
		return otogi.NewFailedResult(err)
	}
	return otogi.NewSucceededResult()
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
	select {
	case m.notify <- struct{}{}:
	default:
	}
	return nil
}

type cancelAfterSubmit struct {
	kernel *kernel.Kernel
	cancel context.CancelFunc
	once   sync.Once
}

func (a *cancelAfterSubmit) SubscribeCompletion(ctx context.Context, id otogi.EventID) (<-chan otogi.EventID, error) {
	return a.kernel.SubscribeCompletion(ctx, id)
}
func (a *cancelAfterSubmit) SubmitTrusted(ctx context.Context, id otogi.EventID, input otogi.EventInput) error {
	err := a.kernel.SubmitTrusted(ctx, id, input)
	if err == nil {
		a.once.Do(a.cancel)
	}
	return err
}

func TestDispatcherKernelReplayKeepsEventAndEffectIDsStable(t *testing.T) {
	messenger := &effectMessenger{notify: make(chan struct{}, 2)}
	generator, err := otogi.NewEventIDGenerator(bytes.NewReader(make([]byte, 16)))
	if err != nil {
		t.Fatal(err)
	}
	k, err := kernel.New(kernel.DefaultConfig(), []otogi.Module{newEffectModule(t)}, messenger, generator)
	if err != nil {
		t.Fatal(err)
	}
	if err := k.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = k.Drain(context.Background()) }()

	row := dispatchRow(t, "018f47a6-f5f7-7cac-9000-333333333333", "chat:1", []byte("rich"))
	store := &dispatchStore{rows: []telegramstore.InboxRow{row}}
	firstCtx, cancel := context.WithCancel(context.Background())
	bridge := &cancelAfterSubmit{kernel: k, cancel: cancel}
	d := NewInboxDispatcher(store, bridge, func(context.Context, []byte) (otogi.EventInput, error) { return dispatchInput(t, "chat:1"), nil }, dispatchConfig(time.Unix(600, 0).UTC()))
	if worked, err := d.dispatchOne(firstCtx); !worked || err != nil {
		t.Fatalf("first dispatch=%v,%v", worked, err)
	}
	select {
	case <-messenger.notify:
	case <-time.After(time.Second):
		t.Fatal("first module effect did not finish")
	}
	if len(store.complete) != 0 || len(store.released) != 1 {
		t.Fatalf("lost acknowledgement retired row: complete=%v released=%v", store.complete, store.released)
	}

	store.rows = []telegramstore.InboxRow{row}
	d = NewInboxDispatcher(store, k, func(context.Context, []byte) (otogi.EventInput, error) { return dispatchInput(t, "chat:1"), nil }, dispatchConfig(time.Unix(700, 0).UTC()))
	if worked, err := d.dispatchOne(context.Background()); !worked || err != nil {
		t.Fatalf("replay=%v,%v", worked, err)
	}
	select {
	case <-messenger.notify:
	case <-time.After(time.Second):
		t.Fatal("replayed module effect did not finish")
	}
	messenger.mu.Lock()
	defer messenger.mu.Unlock()
	if len(messenger.effects) != 2 {
		t.Fatalf("effects=%d", len(messenger.effects))
	}
	first, second := messenger.effects[0], messenger.effects[1]
	if first.OriginatingEvent() != second.OriginatingEvent() || first.OriginatingEvent() != deterministicInboxEventID(row.ID) {
		t.Fatalf("event IDs differ: %s %s", first.OriginatingEvent(), second.OriginatingEvent())
	}
	if first.IdempotencyKey() != second.IdempotencyKey() {
		t.Fatalf("effect IDs differ: %s %s", first.IdempotencyKey(), second.IdempotencyKey())
	}
}
