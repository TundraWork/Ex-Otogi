package management

import "context"

// ServiceRecorder is the canonical service registry key for the management
// recorder write path.
const ServiceRecorder = "otogi.management.recorder"

// ServiceQuery is the canonical service registry key for management query
// access.
const ServiceQuery = "otogi.management.query"

// Recorder captures structured observability data for the management plane.
//
// Implementations should be safe for concurrent use. Callers may treat
// recording as best-effort and should continue business execution when the
// recorder is unavailable unless the surrounding feature explicitly requires
// observability.
type Recorder interface {
	// RecordEvent appends one structured event to the management timeline.
	//
	// Implementations assign the final global event ID and may enrich trace
	// metadata from ctx before storing the event.
	RecordEvent(ctx context.Context, event Event) (Event, error)
	// RecordArtifact stores one debugging artifact associated with a retained
	// event.
	RecordArtifact(ctx context.Context, artifact Artifact) (Artifact, error)
	// UpsertSnapshot replaces one current-state snapshot identified by namespace
	// and key.
	UpsertSnapshot(ctx context.Context, snapshot Snapshot) (Snapshot, error)
}

// Query exposes read-only access to retained management data.
//
// Implementations should return immutable DTO copies so caller mutation cannot
// affect retained process state.
type Query interface {
	// ListEvents returns a newest-first latest, newer, or older page and applies
	// optional filters from the request. Unfiltered queries omit debug events.
	ListEvents(ctx context.Context, request EventQuery) (EventPage, error)
	// GetEvent returns one retained event by global ID.
	GetEvent(ctx context.Context, id int64) (Event, error)
	// GetTrace returns the currently retained event timeline for one trace ID.
	GetTrace(ctx context.Context, request TraceQuery) (TraceView, error)
	// GetArtifact returns one retained artifact by ID.
	GetArtifact(ctx context.Context, id string) (Artifact, error)
	// ListSnapshots returns current snapshot records matching the optional
	// filters in the request.
	ListSnapshots(ctx context.Context, request SnapshotQuery) (SnapshotList, error)
	// GetOverview returns one lightweight summary of the currently retained
	// observability window.
	GetOverview(ctx context.Context) (Overview, error)
}
