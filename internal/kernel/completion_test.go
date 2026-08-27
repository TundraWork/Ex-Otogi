package kernel

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

func TestTrustedAdmissionCompletesAfterAllInterestedModules(t *testing.T) {
	firstDone := make(chan struct{})
	releaseSecond := make(chan struct{})
	first := newTestModule(t, "a", "message", func(context.Context, otogi.Event, otogi.Invocation) otogi.Result {
		close(firstDone)
		return otogi.NewSucceededResult()
	})
	second := newTestModule(t, "b", "message", func(ctx context.Context, _ otogi.Event, _ otogi.Invocation) otogi.Result {
		select {
		case <-releaseSecond:
			return otogi.NewSucceededResult()
		case <-ctx.Done():
			return otogi.NewCanceledResult(ctx.Err())
		}
	})
	k := newTestKernel(t, DefaultConfig(), []otogi.Module{second, first}, &testMessenger{})
	if err := k.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	id, err := otogi.NewEventID("trusted-event")
	if err != nil {
		t.Fatal(err)
	}
	completions, err := k.SubscribeCompletion(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if err := k.SubmitTrusted(context.Background(), id, newTestInput(t, "message", "payload")); err != nil {
		t.Fatal(err)
	}
	<-firstDone
	select {
	case completion := <-completions:
		t.Fatalf("completion before all modules finished: %q", completion.EventID().String())
	default:
	}
	close(releaseSecond)
	select {
	case completion := <-completions:
		if completion.EventID() != id {
			t.Fatalf("completion ID = %q, want %q", completion.EventID().String(), id.String())
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for completion")
	}
	if _, open := <-completions; open {
		t.Fatal("completion subscription remained open")
	}
	if err := k.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestTrustedAdmissionRejectsActiveDuplicateAndAllowsReplayAfterCompletion(t *testing.T) {
	release := make(chan struct{}, 2)
	var calls atomic.Int32
	module := newTestModule(t, "module", "message", func(ctx context.Context, _ otogi.Event, _ otogi.Invocation) otogi.Result {
		calls.Add(1)
		select {
		case <-release:
			return otogi.NewSucceededResult()
		case <-ctx.Done():
			return otogi.NewCanceledResult(ctx.Err())
		}
	})
	k := startedKernel(t, module)
	id, err := otogi.NewEventID("stable-inbox-event")
	if err != nil {
		t.Fatal(err)
	}
	input := newTestInput(t, "message", "payload")
	first, err := k.SubscribeCompletion(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if err := k.SubmitTrusted(context.Background(), id, input); err != nil {
		t.Fatal(err)
	}
	waitForCalls(t, module, 1)
	if err := k.SubmitTrusted(context.Background(), id, input); !errors.Is(err, otogi.ErrInvalidState) {
		t.Fatalf("duplicate active SubmitTrusted() error = %v, want ErrInvalidState", err)
	}
	release <- struct{}{}
	select {
	case <-first:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first completion")
	}

	second, err := k.SubscribeCompletion(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if err := k.SubmitTrusted(context.Background(), id, input); err != nil {
		t.Fatalf("replay SubmitTrusted(): %v", err)
	}
	waitForCalls(t, module, 2)
	release <- struct{}{}
	select {
	case completion := <-second:
		if completion.EventID() != id {
			t.Fatalf("replay completion ID = %q", completion.EventID().String())
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for replay completion")
	}
	if calls.Load() != 2 {
		t.Fatalf("module calls = %d, want at-least-once replay call", calls.Load())
	}
	if err := k.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestTrustedAdmissionReplaysAfterCompletionAcknowledgementLoss(t *testing.T) {
	var calls atomic.Int32
	module := newTestModule(t, "module", "message", func(context.Context, otogi.Event, otogi.Invocation) otogi.Result {
		calls.Add(1)
		return otogi.NewSucceededResult()
	})
	k := startedKernel(t, module)
	id, _ := otogi.NewEventID("lost-ack-event")
	ctx, cancel := context.WithCancel(context.Background())
	if _, err := k.SubscribeCompletion(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := k.SubmitTrusted(context.Background(), id, newTestInput(t, "message", "payload")); err != nil {
		t.Fatal(err)
	}
	cancel()
	waitForCalls(t, module, 1)
	deadline := time.Now().Add(time.Second)
	for {
		completion, err := k.SubscribeCompletion(context.Background(), id)
		if err == nil {
			if err := k.SubmitTrusted(context.Background(), id, newTestInput(t, "message", "payload")); err != nil {
				t.Fatal(err)
			}
			select {
			case <-completion:
			case <-time.After(time.Second):
				t.Fatal("timed out waiting for replay completion")
			}
			break
		}
		if !errors.Is(err, otogi.ErrInvalidState) || time.Now().After(deadline) {
			t.Fatalf("subscribe after lost acknowledgement: %v", err)
		}
		time.Sleep(time.Millisecond)
	}
	if calls.Load() != 2 {
		t.Fatalf("module calls = %d, want replay", calls.Load())
	}
	if err := k.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestCompletionSubscriptionsAreBoundedAndCancellationReleasesCapacity(t *testing.T) {
	config, err := NewConfig(1)
	if err != nil {
		t.Fatal(err)
	}
	k := newTestKernel(t, config, []otogi.Module{newTestModule(t, "module", "message", nil)}, &testMessenger{})
	if err := k.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	firstID, _ := otogi.NewEventID("first")
	secondID, _ := otogi.NewEventID("second")
	ctx, cancel := context.WithCancel(context.Background())
	first, err := k.SubscribeCompletion(ctx, firstID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := k.SubscribeCompletion(context.Background(), secondID); !errors.Is(err, otogi.ErrCapacity) {
		t.Fatalf("subscription beyond bound error = %v, want ErrCapacity", err)
	}
	cancel()
	select {
	case _, open := <-first:
		if open {
			t.Fatal("canceled subscription produced a completion")
		}
	case <-time.After(time.Second):
		t.Fatal("canceled subscription did not close")
	}
	deadline := time.Now().Add(time.Second)
	for {
		second, subscribeErr := k.SubscribeCompletion(context.Background(), secondID)
		if subscribeErr == nil {
			cancelSecond := second
			_ = cancelSecond
			break
		}
		if !errors.Is(subscribeErr, otogi.ErrCapacity) || time.Now().After(deadline) {
			t.Fatalf("SubscribeCompletion after cancellation: %v", subscribeErr)
		}
		time.Sleep(time.Millisecond)
	}
	if err := k.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestTrustedAdmissionCompletesValidUnroutedAction(t *testing.T) {
	k := startedKernel(t, newTestModule(t, "module", "message", nil))
	id, _ := otogi.NewEventID("unrouted")
	completion, err := k.SubscribeCompletion(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if err := k.SubmitTrusted(context.Background(), id, newTestInput(t, "other", "payload")); err != nil {
		t.Fatalf("SubmitTrusted valid unrouted action: %v", err)
	}
	select {
	case got := <-completion:
		if got.EventID() != id {
			t.Fatalf("completion ID = %q", got.EventID().String())
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for unrouted completion")
	}
	if len(k.Deliveries()) != 0 {
		t.Fatalf("unrouted deliveries = %d", len(k.Deliveries()))
	}
	if err := k.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestOrdinarySubmitRejectsActiveTrustedIDCollision(t *testing.T) {
	release := make(chan struct{})
	module := newTestModule(t, "module", "message", func(ctx context.Context, _ otogi.Event, _ otogi.Invocation) otogi.Result {
		select {
		case <-release:
			return otogi.NewSucceededResult()
		case <-ctx.Done():
			return otogi.NewCanceledResult(ctx.Err())
		}
	})
	k := startedKernel(t, module)
	id, err := otogi.NewEventID("evt_000000000000000000000000000000000000000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if err := k.SubmitTrusted(context.Background(), id, newTestInput(t, "message", "trusted")); err != nil {
		t.Fatal(err)
	}
	waitForCalls(t, module, 1)
	if _, err := k.Submit(context.Background(), newTestInput(t, "message", "ordinary")); !errors.Is(err, otogi.ErrInvalidState) {
		t.Fatalf("colliding Submit() error = %v, want ErrInvalidState", err)
	}
	close(release)
	if err := k.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestOrdinarySubmitRemainsKernelOwned(t *testing.T) {
	k := startedKernel(t, newTestModule(t, "module", "message", nil))
	id, err := k.Submit(context.Background(), newTestInput(t, "message", "payload"))
	if err != nil {
		t.Fatal(err)
	}
	if id.String() == "" || id.String() == "trusted-event" {
		t.Fatalf("generated event ID = %q", id.String())
	}
	if err := k.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
}
