package memory

import (
	"context"

	panel "ex-otogi/pkg/otogi/management"
)

type recorderStub struct {
	events []panel.Event
}

func newRecorderStub() *recorderStub {
	return &recorderStub{}
}

func (r *recorderStub) RecordEvent(ctx context.Context, event panel.Event) (panel.Event, error) {
	if trace, ok := panel.TraceFromContext(ctx); ok {
		event.TraceID = trace.TraceID
	}
	r.events = append(r.events, event)
	return event, nil
}

func (r *recorderStub) RecordArtifact(context.Context, panel.Artifact) (panel.Artifact, error) {
	return panel.Artifact{}, nil
}

func (r *recorderStub) UpsertSnapshot(context.Context, panel.Snapshot) (panel.Snapshot, error) {
	return panel.Snapshot{}, nil
}
