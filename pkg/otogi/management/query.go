package management

// EventQuery defines filters and pagination for timeline polling.
type EventQuery struct {
	// AfterID requests events with IDs strictly greater than this cursor.
	AfterID int64
	// Limit caps the number of returned events when positive.
	Limit int
	// Category restricts matches to one coarse event category.
	Category EventCategory
	// Kind restricts matches to one event kind.
	Kind string
	// TraceID restricts matches to one trace.
	TraceID string
	// ConversationID restricts matches to one conversation.
	ConversationID string
	// Module restricts matches to one module.
	Module string
	// Level restricts matches to one event level.
	Level EventLevel
}

// TraceQuery defines one retained trace lookup request.
type TraceQuery struct {
	// TraceID identifies the trace to fetch.
	TraceID string
	// Limit caps the number of returned events when positive.
	Limit int
}

// SnapshotQuery defines filters for current-state snapshot queries.
type SnapshotQuery struct {
	// Namespace restricts matches to one snapshot namespace.
	Namespace string
	// Key restricts matches to one snapshot key.
	Key string
	// Module restricts matches to one module owner.
	Module string
}
