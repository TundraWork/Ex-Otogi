package kernel

import (
	"context"
	"testing"
	"time"

	"ex-otogi/pkg/otogi/core"
	panel "ex-otogi/pkg/otogi/management"
	"ex-otogi/pkg/otogi/platform"
)

func TestEventBusPublishPropagatesTraceContext(t *testing.T) {
	t.Parallel()

	bus := NewEventBus(8, 1, time.Second, nil, nil)
	t.Cleanup(func() {
		_ = bus.Close(context.Background())
	})

	traceIDs := make(chan string, 2)
	_, err := bus.Subscribe(context.Background(), core.InterestSet{
		Kinds: []platform.EventKind{platform.EventKindArticleCreated},
	}, core.SubscriptionSpec{
		Name: "trace-propagation",
	}, func(ctx context.Context, _ *platform.Event) error {
		trace, ok := panel.TraceFromContext(ctx)
		if !ok {
			t.Error("TraceFromContext returned ok=false")
			return nil
		}
		traceIDs <- trace.TraceID
		return nil
	})
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}

	tracedCtx, trace, err := ensureTraceContext(context.Background())
	if err != nil {
		t.Fatalf("ensureTraceContext failed: %v", err)
	}
	if err := bus.Publish(tracedCtx, newTestEvent("e1", platform.EventKindArticleCreated)); err != nil {
		t.Fatalf("Publish failed: %v", err)
	}
	if err := bus.Publish(tracedCtx, newTestEvent("e2", platform.EventKindArticleCreated)); err != nil {
		t.Fatalf("Publish failed: %v", err)
	}

	for range 2 {
		select {
		case got := <-traceIDs:
			if got != trace.TraceID {
				t.Fatalf("trace ID = %q, want %q", got, trace.TraceID)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for trace propagation")
		}
	}
}
