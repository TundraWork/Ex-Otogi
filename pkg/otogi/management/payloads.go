package management

// PlatformEventReceivedPayload describes one inbound platform event accepted by
// the runtime.
type PlatformEventReceivedPayload struct {
	// EventKind is the inbound platform event kind.
	EventKind string
	// SourceID identifies the source runtime or driver instance.
	SourceID string
	// MessageID identifies the source platform message when present.
	MessageID string
}

// PlatformEventPublishedPayload describes one business event published through
// the kernel bus.
type PlatformEventPublishedPayload struct {
	// EventKind is the published internal event kind.
	EventKind string
	// SubscriberCount reports how many handlers matched the event.
	SubscriberCount int
}

// MemoryRetrieveStartedPayload describes semantic retrieval startup details.
type MemoryRetrieveStartedPayload struct {
	// Provider is the embedding or retrieval provider name when applicable.
	Provider string
	// QueryCount is the number of retrieval queries prepared for the request.
	QueryCount int
}

// MemoryRetrievePlanPayload describes a retrieval plan emitted before search.
type MemoryRetrievePlanPayload struct {
	// Queries lists the planned search queries.
	Queries []string
	// TimeFilter records the optional time filter strategy.
	TimeFilter string
	// Depth records the retrieval depth setting.
	Depth string
	// PlannerUsed reports whether a dedicated planner produced this plan.
	PlannerUsed bool
}

// MemoryRetrieveSearchedPayload describes one retrieval search execution.
type MemoryRetrieveSearchedPayload struct {
	// CandidateCount is the number of candidates examined by the search.
	CandidateCount int
	// ReturnedCount is the number of ranked results produced.
	ReturnedCount int
}

// MemoryRetrieveCompletedPayload describes retrieval completion details.
type MemoryRetrieveCompletedPayload struct {
	// ResultCount is the number of memories returned to the caller.
	ResultCount int
	// ElapsedMS is the end-to-end retrieval duration in milliseconds.
	ElapsedMS int64
}

// MemoryExtractStartedPayload describes one memory extraction request.
type MemoryExtractStartedPayload struct {
	// SourceKind identifies the source material being analyzed.
	SourceKind string
	// SegmentCount is the number of input segments considered.
	SegmentCount int
}

// MemoryExtractCompletedPayload describes the outcome of one extraction pass.
type MemoryExtractCompletedPayload struct {
	// ExtractedCount is the number of candidate memories produced.
	ExtractedCount int
	// Consolidated reports whether post-processing consolidated the candidates.
	Consolidated bool
}

// MemoryStoreUpsertedPayload describes one memory store write.
type MemoryStoreUpsertedPayload struct {
	// Store identifies the memory store implementation.
	Store string
	// UpsertedCount is the number of records written.
	UpsertedCount int
}

// MemoryStoreOperationPayload describes one generic memory store operation.
type MemoryStoreOperationPayload struct {
	// Store identifies the memory store implementation.
	Store string
	// Operation is the stable store operation name.
	Operation string
	// AffectedCount reports how many records were affected.
	AffectedCount int
}

// EmbeddingCallStartedPayload describes one embedding provider call start.
type EmbeddingCallStartedPayload struct {
	// Provider identifies the embedding provider.
	Provider string
	// Model identifies the embedding model.
	Model string
	// TaskType records the logical embedding task.
	TaskType string
	// InputCount is the number of input texts submitted.
	InputCount int
}

// EmbeddingCallCompletedPayload describes one successful embedding call.
type EmbeddingCallCompletedPayload struct {
	// Provider identifies the embedding provider.
	Provider string
	// Model identifies the embedding model.
	Model string
	// TaskType records the logical embedding task.
	TaskType string
	// InputCount is the number of input texts submitted.
	InputCount int
	// Dimensions is the output vector dimension when known.
	Dimensions int
	// ElapsedMS is the provider call duration in milliseconds.
	ElapsedMS int64
}

// EmbeddingCallFailedPayload describes one failed embedding call.
type EmbeddingCallFailedPayload struct {
	// Provider identifies the embedding provider.
	Provider string
	// Model identifies the embedding model.
	Model string
	// TaskType records the logical embedding task.
	TaskType string
	// ElapsedMS is the provider call duration in milliseconds.
	ElapsedMS int64
	// Error is the wrapped provider failure message.
	Error string
}

// LLMCallStartedPayload describes one LLM generation request start.
type LLMCallStartedPayload struct {
	// Provider identifies the LLM provider.
	Provider string
	// Model identifies the requested model.
	Model string
	// MessageCount is the number of input messages.
	MessageCount int
	// ToolCount is the number of tool definitions attached to the request.
	ToolCount int
}

// LLMCallCompletedPayload describes one successful LLM generation request.
type LLMCallCompletedPayload struct {
	// Provider identifies the LLM provider.
	Provider string
	// Model identifies the requested model.
	Model string
	// ElapsedMS is the provider call duration in milliseconds.
	ElapsedMS int64
	// OutputMessageCount is the number of output messages or choices returned.
	OutputMessageCount int
}

// LLMCallFailedPayload describes one failed LLM generation request.
type LLMCallFailedPayload struct {
	// Provider identifies the LLM provider.
	Provider string
	// Model identifies the requested model.
	Model string
	// ElapsedMS is the provider call duration in milliseconds.
	ElapsedMS int64
	// Error is the wrapped provider failure message.
	Error string
}

// LLMToolDetectedPayload describes tools selected by the model.
type LLMToolDetectedPayload struct {
	// ToolNames lists the detected tool calls in request order.
	ToolNames []string
	// Count is the number of detected tool calls.
	Count int
}

// LLMToolExecutedPayload describes one executed tool call.
type LLMToolExecutedPayload struct {
	// ToolName identifies the executed tool.
	ToolName string
	// Success reports whether the tool completed successfully.
	Success bool
	// ElapsedMS is the tool execution duration in milliseconds.
	ElapsedMS int64
	// Error is the tool failure message when Success is false.
	Error string
}

// RuntimeAsyncErrorPayload describes an asynchronous runtime failure.
type RuntimeAsyncErrorPayload struct {
	// Operation identifies the async task or goroutine boundary.
	Operation string
	// Error is the wrapped runtime failure message.
	Error string
}
