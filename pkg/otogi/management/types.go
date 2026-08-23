package management

import "time"

// EventCategory groups one event into a coarse frontend-visible timeline area.
type EventCategory string

// Stable first-phase event categories.
const (
	EventCategoryRuntime       EventCategory = "runtime"
	EventCategoryBusinessEvent EventCategory = "business_event"
	EventCategoryMemory        EventCategory = "memory"
	EventCategoryEmbedding     EventCategory = "embedding"
	EventCategoryLLM           EventCategory = "llm"
	EventCategoryTool          EventCategory = "tool"
	EventCategoryDriver        EventCategory = "driver"
	EventCategoryHTTPAPI       EventCategory = "http_api"
)

// EventLevel describes the severity or verbosity of one recorded event.
type EventLevel string

// Stable first-phase event levels.
const (
	EventLevelDebug EventLevel = "debug"
	EventLevelInfo  EventLevel = "info"
	EventLevelWarn  EventLevel = "warn"
	EventLevelError EventLevel = "error"
)

// ArtifactKind classifies one stored debugging artifact.
type ArtifactKind string

// Stable first-phase artifact kinds.
const (
	ArtifactKindPromptSystem       ArtifactKind = "prompt.system"
	ArtifactKindPromptUser         ArtifactKind = "prompt.user"
	ArtifactKindPromptFull         ArtifactKind = "prompt.full"
	ArtifactKindPromptTool         ArtifactKind = "prompt.tool"
	ArtifactKindPromptResponse     ArtifactKind = "response.full"
	ArtifactKindRetrievalPlan      ArtifactKind = "retrieval.plan"
	ArtifactKindRetrievalResults   ArtifactKind = "retrieval.results"
	ArtifactKindEmbeddingInput     ArtifactKind = "embedding.input"
	ArtifactKindEmbeddingResponse  ArtifactKind = "embedding.response"
	ArtifactKindStructuredDebug    ArtifactKind = "debug.structured"
	ArtifactKindStructuredSnapshot ArtifactKind = "snapshot.structured"
)

// Event is the unified timeline envelope exposed to management consumers.
type Event struct {
	// ID is the globally monotonic event identifier assigned by the runtime.
	ID int64
	// OccurredAt records when the event happened.
	OccurredAt time.Time
	// TraceID groups events belonging to the same processing flow.
	TraceID string
	// ParentEventID links one event to its causal parent when known.
	ParentEventID *int64
	// Category is the coarse management grouping for the event.
	Category EventCategory
	// Kind is the stable machine-readable event identifier.
	Kind string
	// Level indicates severity or verbosity.
	Level EventLevel
	// Module identifies the emitting module or runtime area.
	Module string
	// Component identifies the lower-level implementation component.
	Component string
	// TenantID identifies the tenant scope when multi-tenant routing is in use.
	TenantID string
	// Platform identifies the source platform when relevant.
	Platform string
	// ConversationID identifies the conversation correlated with the event.
	ConversationID string
	// ActorID identifies the actor correlated with the event.
	ActorID string
	// Subject is the short human-readable event description.
	Subject string
	// Description carries a brief human-readable excerpt derived from the event
	// payload. Maximum 140 Unicode characters. Empty when no relevant content
	// is available.
	Description string
	// Payload carries kind-specific JSON-compatible data. Kind is the semantic
	// discriminator; Go type names are not part of the observability contract.
	Payload any
	// ArtifactIDs links the event to retained debugging artifacts.
	ArtifactIDs []string
}

// Artifact stores one retained debugging blob linked to an event.
type Artifact struct {
	// ID is the stable artifact identifier.
	ID string
	// EventID identifies the event this artifact belongs to.
	EventID int64
	// Kind classifies the artifact content.
	Kind ArtifactKind
	// CreatedAt records when the artifact was captured.
	CreatedAt time.Time
	// Content stores the original untruncated debugging content.
	Content string
}

// Snapshot represents one replace-in-place current-state record.
type Snapshot struct {
	// Namespace groups related snapshot records.
	Namespace string
	// Key uniquely identifies one snapshot within its namespace.
	Key string
	// Module identifies the module or subsystem owning the snapshot.
	Module string
	// UpdatedAt records the last replacement time for the snapshot.
	UpdatedAt time.Time
	// Summary provides one short human-readable description.
	Summary string
	// Payload stores namespace-specific JSON-compatible data.
	Payload any
}

// EventPage is one newest-first page of the management event timeline.
type EventPage struct {
	// Items contains retained events in descending event ID order.
	Items []Event
	// OldestID is the oldest event ID returned by this page.
	OldestID int64
	// NewestID is the newest event ID returned, or the unchanged AfterID cursor
	// when a newer query returns no matches.
	NewestID int64
	// WindowStartID is the oldest currently retained event ID.
	WindowStartID int64
	// WindowEndID is the newest currently retained event ID.
	WindowEndID int64
	// CursorResetRequired indicates that the caller's cursor predates retention.
	CursorResetRequired bool
	// HasOlder indicates that additional matching events exist below OldestID.
	HasOlder bool
	// HasNewer indicates that additional matching events exist above NewestID.
	HasNewer bool
}

// TraceView returns one retained event timeline for a specific trace.
type TraceView struct {
	// TraceID identifies the trace being queried.
	TraceID string
	// Items contains retained trace events in ascending event ID order.
	Items []Event
}

// SnapshotList returns matching current-state snapshot records.
type SnapshotList struct {
	// Items contains all snapshots matching the request filters.
	Items []Snapshot
}

// Overview summarizes the currently retained management window.
type Overview struct {
	// WindowStartID is the oldest retained event ID.
	WindowStartID int64
	// WindowEndID is the newest retained event ID.
	WindowEndID int64
	// TotalEvents is the count of retained events.
	TotalEvents int
	// EventCountsByCategory aggregates retained event counts by category.
	EventCountsByCategory map[EventCategory]int
	// RecentErrorCount counts retained events at error level.
	RecentErrorCount int
	// ActiveInflightByComponent reports current inflight counts keyed by
	// component or provider name.
	ActiveInflightByComponent map[string]int
	// RecentTraceCount is the number of distinct retained trace IDs.
	RecentTraceCount int
}
