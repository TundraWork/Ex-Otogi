package kernel

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

func TestSendCancellationIsIsolatedWithinInvocation(t *testing.T) {
	messenger := &isolatingMessenger{entered: make(chan string, 2), releaseB: make(chan struct{})}
	module := newTestModule(t, "sender", "message", func(ctx context.Context, _ otogi.Event, invocation otogi.Invocation) otogi.Result {
		text, _ := otogi.NewText("reply")
		aCtx, cancelA := context.WithCancel(ctx)
		aDone := make(chan error, 1)
		go func() { aDone <- invocation.SendText(aCtx, "a", text) }()
		if slot := <-messenger.entered; slot != "a" {
			return otogi.NewFailedResult(errors.New("unexpected first slot"))
		}
		cancelA()
		if err := <-aDone; !errors.Is(err, context.Canceled) {
			return otogi.NewFailedResult(err)
		}

		bDone := make(chan error, 1)
		go func() { bDone <- invocation.SendText(ctx, "b", text) }()
		if slot := <-messenger.entered; slot != "b" {
			return otogi.NewFailedResult(errors.New("unexpected second slot"))
		}
		close(messenger.releaseB)
		if err := <-bDone; err != nil {
			return otogi.NewFailedResult(err)
		}
		return otogi.NewSucceededResult()
	})
	k := newTestKernel(t, DefaultConfig(), []otogi.Module{module}, messenger)
	if err := k.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := k.Submit(context.Background(), newTestInput(t, "message", "request")); err != nil {
		t.Fatal(err)
	}
	if err := k.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := k.Deliveries()[0].Status(); got != otogi.ResultSucceeded {
		t.Fatalf("delivery status = %q", got)
	}
}

func TestStopCauseWinsOverOlderDrainCancellation(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	k := startedKernel(t, blockingModule(t, started, release))
	if _, err := k.Submit(context.Background(), newTestInput(t, "message", "text")); err != nil {
		t.Fatal(err)
	}
	<-started
	drainCtx, cancelDrain := context.WithCancel(context.Background())
	drainDone := make(chan error, 1)
	go func() { drainDone <- k.Drain(drainCtx) }()
	waitUntilNotReady(t, k)
	stopCtx, cancelStop := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancelStop()
	stopErr := k.Stop(stopCtx)
	if !errors.Is(stopErr, context.DeadlineExceeded) {
		t.Fatalf("Stop() = %v", stopErr)
	}
	cancelDrain()
	if err := <-drainDone; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Drain() = %v, want retained stop cause", err)
	}
	close(release)
	if err := k.Wait(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Wait() = %v", err)
	}
}

func TestStopInterruptingDrainRetainsCancellation(t *testing.T) {
	started := make(chan struct{})
	module := newTestModule(t, "module", "message", func(ctx context.Context, _ otogi.Event, _ otogi.Invocation) otogi.Result {
		close(started)
		<-ctx.Done()
		return otogi.NewFailedResult(ctx.Err())
	})
	k := startedKernel(t, module)
	if _, err := k.Submit(context.Background(), newTestInput(t, "message", "active")); err != nil {
		t.Fatal(err)
	}
	<-started
	if _, err := k.Submit(context.Background(), newTestInput(t, "message", "queued")); err != nil {
		t.Fatal(err)
	}
	drainDone := make(chan error, 1)
	go func() { drainDone <- k.Drain(context.Background()) }()
	waitUntilNotReady(t, k)
	if err := k.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() = %v", err)
	}
	if err := <-drainDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("Drain() = %v, want cancellation", err)
	}
	if err := k.Stop(context.Background()); err != nil {
		t.Fatalf("second Stop() = %v", err)
	}
}

func TestConcurrentStopJoinsWorkerAfterEarlierStopTimeout(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	k := startedKernel(t, blockingModule(t, started, release))
	if _, err := k.Submit(context.Background(), newTestInput(t, "message", "text")); err != nil {
		t.Fatal(err)
	}
	<-started
	firstCtx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if err := k.Stop(firstCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("first Stop() = %v", err)
	}
	secondDone := make(chan error, 1)
	go func() { secondDone <- k.Stop(context.Background()) }()
	select {
	case err := <-secondDone:
		t.Fatalf("second Stop returned before worker termination: %v", err)
	case <-time.After(10 * time.Millisecond):
	}
	close(release)
	if err := <-secondDone; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second Stop() = %v", err)
	}
	if err := k.Wait(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Wait() = %v", err)
	}
	if err := k.Stop(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("third Stop() = %v", err)
	}
}

func TestInvalidModuleResultBecomesFailedAndRoutingContinues(t *testing.T) {
	invalid := newTestModule(t, "a", "message", func(context.Context, otogi.Event, otogi.Invocation) otogi.Result { return otogi.Result{} })
	later := newTestModule(t, "b", "message", nil)
	k := newTestKernel(t, DefaultConfig(), []otogi.Module{later, invalid}, &testMessenger{})
	if err := k.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := k.Submit(context.Background(), newTestInput(t, "message", "text")); err != nil {
		t.Fatal(err)
	}
	if err := k.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	deliveries := k.Deliveries()
	if len(deliveries) != 2 {
		t.Fatalf("deliveries = %d", len(deliveries))
	}
	if deliveries[0].Status() != otogi.ResultFailed || !errors.Is(deliveries[0].Err(), otogi.ErrInvalid) {
		t.Fatalf("invalid delivery = (%q, %v)", deliveries[0].Status(), deliveries[0].Err())
	}
	if deliveries[1].Status() != otogi.ResultSucceeded {
		t.Fatalf("later delivery = %q", deliveries[1].Status())
	}
}

type isolatingMessenger struct {
	entered  chan string
	releaseB chan struct{}
	mu       sync.Mutex
	calls    int
}

func (m *isolatingMessenger) Send(ctx context.Context, _ otogi.OutboundMessage) error {
	m.mu.Lock()
	m.calls++
	call := m.calls
	m.mu.Unlock()
	slot := "a"
	if call == 2 {
		slot = "b"
	}
	m.entered <- slot
	if slot == "b" {
		select {
		case <-m.releaseB:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	<-ctx.Done()
	return ctx.Err()
}

func waitUntilNotReady(t *testing.T, k *Kernel) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for k.Ready() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if k.Ready() {
		t.Fatal("kernel remained ready")
	}
}
