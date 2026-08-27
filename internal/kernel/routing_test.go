package kernel

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

func TestRoutingInvokesInterestedModulesInIDOrderAndIsolatesFailures(t *testing.T) {
	var mu sync.Mutex
	var order []string
	failed := newTestModule(t, "b", "message", func(context.Context, otogi.Event, otogi.Invocation) otogi.Result {
		mu.Lock()
		order = append(order, "b")
		mu.Unlock()
		return otogi.NewFailedResult(otogi.ErrUnavailable)
	})
	panicked := newTestModule(t, "a", "message", func(context.Context, otogi.Event, otogi.Invocation) otogi.Result {
		mu.Lock()
		order = append(order, "a")
		mu.Unlock()
		panic("secret payload")
	})
	succeeded := newTestModule(t, "c", "message", func(context.Context, otogi.Event, otogi.Invocation) otogi.Result {
		mu.Lock()
		order = append(order, "c")
		mu.Unlock()
		return otogi.NewSucceededResult()
	})
	ignored := newTestModule(t, "d", "other", nil)
	k := newTestKernel(t, DefaultConfig(), []otogi.Module{succeeded, failed, ignored, panicked}, &testMessenger{})
	if err := k.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	id, err := k.Submit(context.Background(), newTestInput(t, "message", "text"))
	if err != nil {
		t.Fatal(err)
	}
	if err := k.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	gotOrder := strings.Join(order, "")
	mu.Unlock()
	if gotOrder != "abc" {
		t.Fatalf("order = %q, want abc", gotOrder)
	}
	outcomes := k.Deliveries()
	if len(outcomes) != 3 {
		t.Fatalf("deliveries = %d, want 3", len(outcomes))
	}
	wantIDs := []string{"a", "b", "c"}
	wantStatuses := []otogi.ResultStatus{otogi.ResultFailed, otogi.ResultFailed, otogi.ResultSucceeded}
	for i, outcome := range outcomes {
		if outcome.EventID() != id || outcome.ModuleID().String() != wantIDs[i] || outcome.Status() != wantStatuses[i] {
			t.Fatalf("delivery %d = (%q,%q,%q)", i, outcome.EventID().String(), outcome.ModuleID().String(), outcome.Status())
		}
	}
	if !errors.Is(outcomes[0].Err(), otogi.ErrModulePanic) {
		t.Fatalf("panic error = %v", outcomes[0].Err())
	}
	if !errors.Is(outcomes[1].Err(), otogi.ErrUnavailable) {
		t.Fatalf("failure error = %v", outcomes[1].Err())
	}
	if outcomes[2].Err() != nil {
		t.Fatalf("success error = %v", outcomes[2].Err())
	}

	reports := k.Panics()
	if len(reports) != 1 || reports[0].EventID() != id || reports[0].ModuleID().String() != "a" {
		t.Fatalf("panic reports = %#v", reports)
	}
	if len(reports[0].Stack()) == 0 || len(reports[0].Stack()) > 32*1024 {
		t.Fatalf("stack length = %d", len(reports[0].Stack()))
	}
	if strings.Contains(string(reports[0].Stack()), "secret payload") {
		t.Fatal("panic stack retained panic value")
	}
	stack := reports[0].Stack()
	stack[0] ^= 0xff
	if k.Panics()[0].Stack()[0] == stack[0] {
		t.Fatal("Panics returned aliased stack")
	}
}

func TestInvocationScopesMessageAndRejectsDuplicateSlots(t *testing.T) {
	messenger := &recordingMessenger{}
	module := newTestModule(t, "sender", "message", func(ctx context.Context, event otogi.Event, invocation otogi.Invocation) otogi.Result {
		text, _ := otogi.NewText("reply")
		if err := invocation.SendText(ctx, "reply-slot", text); err != nil {
			return otogi.NewFailedResult(err)
		}
		if err := invocation.SendText(ctx, "reply-slot", text); !errors.Is(err, otogi.ErrInvalidState) {
			return otogi.NewFailedResult(fmt.Errorf("duplicate slot error: %v", err))
		}
		return otogi.NewSucceededResult()
	})
	k := newTestKernel(t, DefaultConfig(), []otogi.Module{module}, messenger)
	if err := k.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	id, err := k.Submit(context.Background(), newTestInput(t, "message", "request"))
	if err != nil {
		t.Fatal(err)
	}
	if err := k.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	messages := messenger.snapshot()
	if len(messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(messages))
	}
	message := messages[0]
	if message.Tenant().String() != "tenant" || message.Platform().String() != "platform" || message.Account().String() != "account" || message.Conversation().String() != "conversation" || message.OriginatingEvent() != id || message.OriginatingModule().String() != "sender" || message.Text().String() != "reply" {
		t.Fatalf("message scope incorrect")
	}
	if got, want := message.IdempotencyKey().String(), expectedMessagingKey(id.String(), "sender", "reply-slot"); got != want {
		t.Fatalf("key = %q, want %q", got, want)
	}
	if got := k.Deliveries()[0].Status(); got != otogi.ResultSucceeded {
		t.Fatalf("status = %q", got)
	}
}

func TestBoundedHistoriesEvictOldestAndReturnCopies(t *testing.T) {
	module := newTestModule(t, "panic", "message", func(context.Context, otogi.Event, otogi.Invocation) otogi.Result { panic("redacted") })
	k := newTestKernel(t, DefaultConfig(), []otogi.Module{module}, &testMessenger{})
	if err := k.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	var ids []otogi.EventID
	for i := 0; i < 65; i++ {
		id, err := k.Submit(context.Background(), newTestInput(t, "message", fmt.Sprintf("text-%d", i)))
		if err != nil {
			waitForCalls(t, module, i)
			id, err = k.Submit(context.Background(), newTestInput(t, "message", fmt.Sprintf("text-%d", i)))
		}
		if err != nil {
			t.Fatalf("Submit %d: %v", i, err)
		}
		ids = append(ids, id)
	}
	if err := k.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	panics, deliveries := k.Panics(), k.Deliveries()
	if len(panics) != 64 || len(deliveries) != 64 {
		t.Fatalf("history sizes = %d, %d", len(panics), len(deliveries))
	}
	if panics[0].EventID() != ids[1] || deliveries[0].EventID() != ids[1] || panics[63].EventID() != ids[64] {
		t.Fatal("histories did not evict oldest")
	}
	panics[0] = PanicReport{}
	deliveries[0] = Delivery{}
	if k.Panics()[0].EventID() != ids[1] || k.Deliveries()[0].EventID() != ids[1] {
		t.Fatal("history slices are aliased")
	}
}

func expectedMessagingKey(event, module, slot string) string {
	h := sha256.New()
	var size [8]byte
	for _, part := range []string{event, module, "core.messaging", slot} {
		binary.BigEndian.PutUint64(size[:], uint64(len(part)))
		_, _ = h.Write(size[:])
		_, _ = h.Write([]byte(part))
	}
	return hex.EncodeToString(h.Sum(nil))
}

type recordingMessenger struct {
	mu       sync.Mutex
	messages []otogi.OutboundMessage
}

func (m *recordingMessenger) Send(ctx context.Context, message otogi.OutboundMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = append(m.messages, message)
	return nil
}
func (m *recordingMessenger) snapshot() []otogi.OutboundMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]otogi.OutboundMessage(nil), m.messages...)
}
