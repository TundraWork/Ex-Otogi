package management

import "context"

type traceContextKey struct{}

// TraceContext carries trace metadata propagated through one request chain.
type TraceContext struct {
	// TraceID groups related management events across one processing flow.
	TraceID string
	// ParentEventID links one event to an already-recorded parent event when
	// causal ordering is known.
	ParentEventID *int64
}

// WithTrace returns a child context carrying the provided trace metadata.
func WithTrace(ctx context.Context, trace TraceContext) context.Context {
	return context.WithValue(ctx, traceContextKey{}, trace)
}

// TraceFromContext extracts trace metadata previously attached with WithTrace.
func TraceFromContext(ctx context.Context) (TraceContext, bool) {
	if ctx == nil {
		return TraceContext{}, false
	}

	trace, ok := ctx.Value(traceContextKey{}).(TraceContext)
	return trace, ok
}
