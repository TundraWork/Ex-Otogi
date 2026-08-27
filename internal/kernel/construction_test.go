package kernel

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

func TestConfigValidatesQueueCapacity(t *testing.T) {
	for _, capacity := range []int{0, -1, 1025} {
		if _, err := NewConfig(capacity); !errors.Is(err, otogi.ErrInvalid) {
			t.Fatalf("NewConfig(%d) error = %v, want ErrInvalid", capacity, err)
		}
	}
	for _, capacity := range []int{1, 1024} {
		config, err := NewConfig(capacity)
		if err != nil {
			t.Fatalf("NewConfig(%d): %v", capacity, err)
		}
		if got := config.QueueCapacity(); got != capacity {
			t.Fatalf("QueueCapacity() = %d, want %d", got, capacity)
		}
	}
	if got := DefaultConfig().QueueCapacity(); got != 16 {
		t.Fatalf("default QueueCapacity() = %d, want 16", got)
	}
}

func TestNewRejectsInvalidDependenciesAndDescriptors(t *testing.T) {
	config := DefaultConfig()
	module := newTestModule(t, "module", "message", nil)
	generator := newTestGenerator(t)
	messenger := &testMessenger{}

	tests := []struct {
		name      string
		modules   []otogi.Module
		messenger otogi.Messenger
		generator *otogi.EventIDGenerator
	}{
		{name: "no modules", modules: nil, messenger: messenger, generator: generator},
		{name: "nil module", modules: []otogi.Module{nil}, messenger: messenger, generator: generator},
		{name: "nil messenger", modules: []otogi.Module{module}, generator: generator},
		{name: "nil generator", modules: []otogi.Module{module}, messenger: messenger},
		{name: "zero descriptor", modules: []otogi.Module{&testModule{}}, messenger: messenger, generator: generator},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := New(config, test.modules, test.messenger, test.generator); !errors.Is(err, otogi.ErrInvalid) {
				t.Fatalf("New() error = %v, want ErrInvalid", err)
			}
		})
	}

	duplicate := newTestModule(t, "module", "other", nil)
	if _, err := New(config, []otogi.Module{module, duplicate}, messenger, generator); !errors.Is(err, otogi.ErrInvalid) {
		t.Fatalf("New() duplicate error = %v, want ErrInvalid", err)
	}
}

func TestStartAndBoundedAdmission(t *testing.T) {
	config, err := NewConfig(1)
	if err != nil {
		t.Fatal(err)
	}
	block := make(chan struct{})
	module := newTestModule(t, "module", "message", func(ctx context.Context, _ otogi.Event, _ otogi.Invocation) otogi.Result {
		select {
		case <-block:
			return otogi.NewSucceededResult()
		case <-ctx.Done():
			return otogi.NewCanceledResult(ctx.Err())
		}
	})
	k := newTestKernel(t, config, []otogi.Module{module}, &testMessenger{})
	if k.Ready() {
		t.Fatal("fresh kernel is ready")
	}
	input := newTestInput(t, "message", "first")
	if _, err := k.Submit(context.Background(), input); !errors.Is(err, otogi.ErrInvalidState) {
		t.Fatalf("Submit before Start error = %v, want ErrInvalidState", err)
	}
	if err := k.Start(context.Background()); err != nil {
		t.Fatalf("Start(): %v", err)
	}
	if !k.Ready() {
		t.Fatal("started kernel is not ready")
	}

	firstID, err := k.Submit(context.Background(), input)
	if err != nil {
		t.Fatalf("first Submit(): %v", err)
	}
	waitForCalls(t, module, 1)
	secondID, err := k.Submit(context.Background(), newTestInput(t, "message", "second"))
	if err != nil {
		t.Fatalf("second Submit(): %v", err)
	}
	if firstID == secondID || firstID.String() == "" || secondID.String() == "" {
		t.Fatalf("runtime IDs = %q, %q, want distinct nonempty", firstID.String(), secondID.String())
	}
	if _, err := k.Submit(context.Background(), newTestInput(t, "message", "third")); !errors.Is(err, otogi.ErrCapacity) {
		t.Fatalf("third Submit() error = %v, want ErrCapacity", err)
	}
	close(block)
	if err := k.Drain(context.Background()); err != nil {
		t.Fatalf("Drain(): %v", err)
	}
}

func TestSubmitValidatesCancellationAndAcceptsUnroutedAction(t *testing.T) {
	module := newTestModule(t, "module", "message", nil)
	k := newTestKernel(t, DefaultConfig(), []otogi.Module{module}, &testMessenger{})
	if err := k.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := k.Submit(ctx, newTestInput(t, "message", "text")); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Submit() error = %v, want context.Canceled", err)
	}
	if _, err := k.Submit(context.Background(), newTestInput(t, "unknown", "text")); err != nil {
		t.Fatalf("unrouted Submit() error = %v", err)
	}
	if err := k.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(k.Deliveries()) != 0 {
		t.Fatalf("unrouted deliveries = %d", len(k.Deliveries()))
	}
}

func TestCanceledStartFailsWithoutBecomingReady(t *testing.T) {
	k := newTestKernel(t, DefaultConfig(), []otogi.Module{newTestModule(t, "module", "message", nil)}, &testMessenger{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := k.Start(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Start() error = %v, want context.Canceled", err)
	}
	if k.Ready() {
		t.Fatal("canceled startup became ready")
	}
	if err := k.Wait(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait() error = %v, want context.Canceled", err)
	}
}

func newTestKernel(t *testing.T, config Config, modules []otogi.Module, messenger otogi.Messenger) *Kernel {
	t.Helper()
	k, err := New(config, modules, messenger, newTestGenerator(t))
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	return k
}

func newTestGenerator(t *testing.T) *otogi.EventIDGenerator {
	t.Helper()
	generator, err := otogi.NewEventIDGenerator(bytes.NewReader(make([]byte, 16)))
	if err != nil {
		t.Fatalf("NewEventIDGenerator(): %v", err)
	}
	return generator
}

func newTestInput(t *testing.T, actionValue, textValue string) otogi.EventInput {
	t.Helper()
	platform, _ := otogi.NewPlatformID("platform")
	account, _ := otogi.NewAccountID("account")
	tenant, _ := otogi.NewTenantID("tenant")
	conversation, _ := otogi.NewConversationID("conversation")
	var action otogi.EventAction
	if actionValue == "message" {
		action = otogi.ActionMessageCreated
	} else {
		action, _ = otogi.NewEventAction("https://example.test/" + actionValue)
	}
	partID, _ := otogi.NewPartID("text")
	part, _ := otogi.NewTextPart(partID, textValue, nil, nil, nil)
	message, err := otogi.NewMessage([]otogi.PartID{partID}, []otogi.Part{part}, nil, nil)
	if err != nil {
		t.Fatalf("NewMessage(): %v", err)
	}
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	input, err := otogi.NewEventInput(otogi.EventInputParams{Platform: platform, Account: account, Tenant: tenant, Conversation: conversation, Action: action, Direction: otogi.DirectionIncoming, Completeness: otogi.CompletenessComplete, OccurredAt: now, IngestedAt: now, Message: message})
	if err != nil {
		t.Fatalf("NewEventInput(): %v", err)
	}
	return input
}

func testRichMessage(t *testing.T, text string) otogi.Message {
	t.Helper()
	id, err := otogi.NewPartID("text")
	if err != nil {
		t.Fatal(err)
	}
	part, err := otogi.NewTextPart(id, text, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	message, err := otogi.NewMessage([]otogi.PartID{id}, []otogi.Part{part}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return message
}

type testModule struct {
	descriptor otogi.ModuleDescriptor
	handle     func(context.Context, otogi.Event, otogi.Invocation) otogi.Result
	mu         sync.Mutex
	calls      int
}

func newTestModule(t *testing.T, idValue, actionValue string, handle func(context.Context, otogi.Event, otogi.Invocation) otogi.Result) *testModule {
	t.Helper()
	id, _ := otogi.NewModuleID(idValue)
	var action otogi.EventAction
	switch actionValue {
	case "message":
		action = otogi.ActionMessageCreated
	case "edited":
		action = otogi.ActionMessageEdited
	default:
		action, _ = otogi.NewEventAction("https://example.test/" + actionValue)
	}
	descriptor, err := otogi.NewModuleDescriptor(id, []otogi.EventAction{action})
	if err != nil {
		t.Fatalf("NewModuleDescriptor(): %v", err)
	}
	return &testModule{descriptor: descriptor, handle: handle}
}

func (m *testModule) Descriptor() otogi.ModuleDescriptor { return m.descriptor }

func (m *testModule) Handle(ctx context.Context, event otogi.Event, invocation otogi.Invocation) otogi.Result {
	m.mu.Lock()
	m.calls++
	m.mu.Unlock()
	if m.handle != nil {
		return m.handle(ctx, event, invocation)
	}
	return otogi.NewSucceededResult()
}

func waitForCalls(t *testing.T, module *testModule, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		module.mu.Lock()
		calls := module.calls
		module.mu.Unlock()
		if calls >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("module calls did not reach %d", want)
}

type testMessenger struct{}

func (*testMessenger) Send(context.Context, otogi.OutboundMessage) error { return nil }
