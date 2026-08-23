package management

import (
	"context"
	"testing"
)

func TestTraceContextRoundTrip(t *testing.T) {
	parentID := int64(42)
	trace := TraceContext{
		TraceID:       "trace-123",
		ParentEventID: &parentID,
	}

	ctx := WithTrace(context.Background(), trace)
	got, ok := TraceFromContext(ctx)
	if !ok {
		t.Fatal("TraceFromContext returned ok=false, want true")
	}
	if got.TraceID != trace.TraceID {
		t.Fatalf("TraceID = %q, want %q", got.TraceID, trace.TraceID)
	}
	if got.ParentEventID == nil || *got.ParentEventID != parentID {
		t.Fatalf("ParentEventID = %v, want %d", got.ParentEventID, parentID)
	}
}

func TestServiceKeysAreStable(t *testing.T) {
	if ServiceRecorder == "" {
		t.Fatal("ServiceRecorder is empty")
	}
	if ServiceQuery == "" {
		t.Fatal("ServiceQuery is empty")
	}
	if ServiceRecorder == ServiceQuery {
		t.Fatal("service keys must be distinct")
	}
}
