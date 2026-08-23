package managementhttp

import (
	"context"
	"errors"

	panel "ex-otogi/pkg/otogi/management"

	"github.com/danielgtaylor/huma/v2"
)

type eventsInput struct {
	AfterID        int64               `query:"after_id"`
	BeforeID       int64               `query:"before_id"`
	Limit          int                 `query:"limit"`
	Category       panel.EventCategory `query:"category"`
	Kind           string              `query:"kind"`
	TraceID        string              `query:"trace_id"`
	ConversationID string              `query:"conversation_id"`
	Module         string              `query:"module"`
	Level          panel.EventLevel    `query:"level"`
}

type eventByIDInput struct {
	ID int64 `path:"id"`
}

type traceInput struct {
	TraceID string `path:"trace_id"`
	Limit   int    `query:"limit"`
}

type artifactInput struct {
	ID string `path:"id"`
}

type snapshotsInput struct {
	Namespace string `query:"namespace"`
	Key       string `query:"key"`
	Module    string `query:"module"`
}

type pageOutput struct {
	Body panel.EventPage
}

type eventOutput struct {
	Body panel.Event
}

type traceOutput struct {
	Body panel.TraceView
}

type artifactOutput struct {
	Body panel.Artifact
}

type snapshotsOutput struct {
	Body panel.SnapshotList
}

type overviewOutput struct {
	Body panel.Overview
}

// registerRoutes registers all management API routes on the given Huma API.
// When called with a nil query, handlers are never invoked — only their
// input/output type signatures are used for OpenAPI schema generation.
// All query dereferences are inside handler closures, so nil query is safe
// as long as no handler is called.
func registerRoutes(api huma.API, query panel.Query) {
	huma.Get(api, "/panel/events", func(ctx context.Context, input *eventsInput) (*pageOutput, error) {
		page, err := query.ListEvents(ctx, panel.EventQuery{
			AfterID:        input.AfterID,
			BeforeID:       input.BeforeID,
			Limit:          input.Limit,
			Category:       input.Category,
			Kind:           input.Kind,
			TraceID:        input.TraceID,
			ConversationID: input.ConversationID,
			Module:         input.Module,
			Level:          input.Level,
		})
		if err != nil {
			return nil, huma.Error400BadRequest(err.Error(), err)
		}
		return &pageOutput{Body: page}, nil
	})

	huma.Get(api, "/panel/events/{id}", func(ctx context.Context, input *eventByIDInput) (*eventOutput, error) {
		event, err := query.GetEvent(ctx, input.ID)
		if err != nil {
			if errors.Is(err, panel.ErrEventNotFound) {
				return nil, huma.Error404NotFound("event not found", err)
			}
			return nil, huma.Error500InternalServerError("event query failed", err)
		}
		return &eventOutput{Body: event}, nil
	})

	huma.Get(api, "/panel/traces/{trace_id}", func(ctx context.Context, input *traceInput) (*traceOutput, error) {
		traceView, err := query.GetTrace(ctx, panel.TraceQuery{
			TraceID: input.TraceID,
			Limit:   input.Limit,
		})
		if err != nil {
			return nil, huma.Error400BadRequest(err.Error(), err)
		}
		return &traceOutput{Body: traceView}, nil
	})

	huma.Get(api, "/panel/artifacts/{id}", func(ctx context.Context, input *artifactInput) (*artifactOutput, error) {
		artifact, err := query.GetArtifact(ctx, input.ID)
		if err != nil {
			if errors.Is(err, panel.ErrArtifactNotFound) {
				return nil, huma.Error404NotFound("artifact not found", err)
			}
			return nil, huma.Error500InternalServerError("artifact query failed", err)
		}
		return &artifactOutput{Body: artifact}, nil
	})

	huma.Get(api, "/panel/snapshots", func(ctx context.Context, input *snapshotsInput) (*snapshotsOutput, error) {
		snapshots, err := query.ListSnapshots(ctx, panel.SnapshotQuery{
			Namespace: input.Namespace,
			Key:       input.Key,
			Module:    input.Module,
		})
		if err != nil {
			return nil, huma.Error400BadRequest(err.Error(), err)
		}
		return &snapshotsOutput{Body: snapshots}, nil
	})

	huma.Get(api, "/panel/overview", func(ctx context.Context, _ *struct{}) (*overviewOutput, error) {
		overview, err := query.GetOverview(ctx)
		if err != nil {
			return nil, huma.Error500InternalServerError("overview query failed", err)
		}
		return &overviewOutput{Body: overview}, nil
	})
}
