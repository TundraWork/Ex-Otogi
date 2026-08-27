package kernel

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func TestLifecycleTransitionsAndRetainsTerminalCause(t *testing.T) {
	t.Run("clean drain stops and stop is idempotent", func(t *testing.T) {
		k := startedKernel(t, newTestModule(t, "module", "message", nil))
		if err := k.Drain(context.Background()); err != nil {
			t.Fatalf("Drain(): %v", err)
		}
		if k.Ready() {
			t.Fatal("drained kernel is ready")
		}
		if err := k.Stop(context.Background()); err != nil {
			t.Fatalf("Stop(): %v", err)
		}
		if err := k.Wait(context.Background()); err != nil {
			t.Fatalf("Wait(): %v", err)
		}
	})

	t.Run("drain timeout fails and retains cause", func(t *testing.T) {
		release := make(chan struct{})
		started := make(chan struct{})
		module := blockingModule(t, started, release)
		k := startedKernel(t, module)
		if _, err := k.Submit(context.Background(), newTestInput(t, "message", "text")); err != nil {
			t.Fatal(err)
		}
		<-started
		ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
		defer cancel()
		if err := k.Drain(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Drain() = %v", err)
		}
		if k.Ready() {
			t.Fatal("failed drain remains ready")
		}
		close(release)
		if err := k.Wait(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Wait() = %v", err)
		}
		if err := k.Wait(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("second Wait() = %v", err)
		}
		if err := k.Stop(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Stop() = %v", err)
		}
	})

	t.Run("stop timeout is stopped and wait retains cause", func(t *testing.T) {
		release := make(chan struct{})
		started := make(chan struct{})
		k := startedKernel(t, blockingModule(t, started, release))
		if _, err := k.Submit(context.Background(), newTestInput(t, "message", "text")); err != nil {
			t.Fatal(err)
		}
		<-started
		ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
		defer cancel()
		if err := k.Stop(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Stop() = %v", err)
		}
		if k.Ready() {
			t.Fatal("timed out stop remains ready")
		}
		if _, err := k.Submit(context.Background(), newTestInput(t, "message", "later")); !errors.Is(err, otogi.ErrInvalidState) {
			t.Fatalf("Submit() = %v", err)
		}
		close(release)
		if err := k.Wait(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Wait() = %v", err)
		}
		if err := k.Stop(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("second Stop() = %v", err)
		}
	})

	t.Run("stop from new", func(t *testing.T) {
		k := newTestKernel(t, DefaultConfig(), []otogi.Module{newTestModule(t, "module", "message", nil)}, &testMessenger{})
		if err := k.Stop(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := k.Stop(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := k.Wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
}

func TestConcurrentSubmitAndStop(t *testing.T) {
	k := startedKernel(t, newTestModule(t, "module", "message", nil))
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_, _ = k.Submit(context.Background(), newTestInput(t, "message", "text"))
			}
		}()
	}
	wg.Add(1)
	go func() { defer wg.Done(); _ = k.Stop(context.Background()) }()
	wg.Wait()
	if err := k.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestStopRacingStartIsValid(t *testing.T) {
	for i := 0; i < 100; i++ {
		k := newTestKernel(t, DefaultConfig(), []otogi.Module{newTestModule(t, "module", "message", nil)}, &testMessenger{})
		startDone := make(chan error, 1)
		go func() { startDone <- k.Start(context.Background()) }()
		stopErr := k.Stop(context.Background())
		startErr := <-startDone
		if stopErr != nil {
			t.Fatalf("Stop() = %v", stopErr)
		}
		if startErr != nil && !errors.Is(startErr, otogi.ErrInvalidState) {
			t.Fatalf("Start() = %v", startErr)
		}
		if err := k.Wait(context.Background()); err != nil {
			t.Fatalf("Wait() = %v", err)
		}
	}
}

func startedKernel(t *testing.T, module otogi.Module) *Kernel {
	t.Helper()
	k := newTestKernel(t, DefaultConfig(), []otogi.Module{module}, &testMessenger{})
	if err := k.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	return k
}

func blockingModule(t *testing.T, started chan<- struct{}, release <-chan struct{}) *testModule {
	t.Helper()
	return newTestModule(t, "module", "message", func(context.Context, otogi.Event, otogi.Invocation) otogi.Result {
		close(started)
		<-release
		return otogi.NewSucceededResult()
	})
}
