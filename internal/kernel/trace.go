package kernel

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"

	panel "ex-otogi/pkg/otogi/management"
)

func ensureTraceContext(ctx context.Context) (context.Context, panel.TraceContext, error) {
	if trace, ok := panel.TraceFromContext(ctx); ok && trace.TraceID != "" {
		return ctx, trace, nil
	}

	traceID, err := newTraceID()
	if err != nil {
		return nil, panel.TraceContext{}, fmt.Errorf("ensure trace context: %w", err)
	}

	trace := panel.TraceContext{TraceID: traceID}
	return panel.WithTrace(ctx, trace), trace, nil
}

func newTraceID() (string, error) {
	var payload [16]byte
	if _, err := rand.Read(payload[:]); err != nil {
		return "", fmt.Errorf("generate trace id: %w", err)
	}

	return "trc_" + hex.EncodeToString(payload[:]), nil
}
