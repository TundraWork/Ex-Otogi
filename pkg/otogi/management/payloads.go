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

// MemoryWindowEnqueuedPayload describes one article being queued into an
// extraction window.
type MemoryWindowEnqueuedPayload struct {
	// ArticleID identifies the newly queued article.
	ArticleID string
	// WindowArticleCount is the number of articles currently buffered.
	WindowArticleCount int
	// WindowRuneCount is the total buffered article text size in runes.
	WindowRuneCount int
}

// MemoryExtractStartedPayload describes one memory extraction request.
type MemoryExtractStartedPayload struct {
	// SourceKind identifies the source material being analyzed.
	SourceKind string
	// SegmentCount is the number of input segments considered.
	SegmentCount int
	// InputRunes is the serialized prompt input size in runes.
	InputRunes int
	// ExistingMemoryCount is the number of retrieved existing memories provided
	// to the extractor for comparison.
	ExistingMemoryCount int
}

// MemoryExtractCompletedPayload describes the outcome of one extraction pass.
type MemoryExtractCompletedPayload struct {
	// ExtractedCount is the number of candidate memories produced.
	ExtractedCount int
	// AppliedCount is the number of candidate actions successfully applied.
	AppliedCount int
	// FailedCount is the number of candidate actions that failed to apply.
	FailedCount int
	// Consolidated reports whether post-processing consolidated the candidates.
	Consolidated bool
}

// MemoryWindowFlushedPayload describes one window flush event.
type MemoryWindowFlushedPayload struct {
	// Reason describes why the window was flushed (quiet, max_count, max_runes, max_age, scope_drain, shutdown).
	Reason string
	// ArticleCount is the number of articles in the flushed window.
	ArticleCount int
	// RuneCount is the total buffered article text size in runes.
	RuneCount int
	// BufferedMS is the elapsed time from the first buffered receive until the
	// window started processing.
	BufferedMS int64
}

// MemoryWindowSkippedPayload describes one flushed window that was skipped
// before extraction.
type MemoryWindowSkippedPayload struct {
	// Reason identifies why the flushed window was skipped.
	Reason string
	// ArticleCount is the number of articles in the skipped window.
	ArticleCount int
}

// MemoryWindowProcessingFailedPayload describes one background window
// processing failure.
type MemoryWindowProcessingFailedPayload struct {
	// Reason describes why the window was flushed.
	Reason string
	// ArticleCount is the number of articles in the failed window.
	ArticleCount int
	// Error is the wrapped processing failure.
	Error string
}

// MemoryExtractParseFailedPayload describes one extractor response parse
// failure.
type MemoryExtractParseFailedPayload struct {
	// ResponseRunes is the extractor response size in runes.
	ResponseRunes int
	// Error is the response parse failure.
	Error string
}

// MemoryExtractApplyFailedPayload describes one extracted candidate that could
// not be applied to the memory store.
type MemoryExtractApplyFailedPayload struct {
	// Action identifies the attempted extractor action.
	Action string
	// TargetID identifies the target record when present.
	TargetID string
	// Category is the extracted memory category.
	Category string
	// Importance is the extracted importance score.
	Importance int
	// Error is the application failure.
	Error string
}

// MemoryConsolidationPrunedPayload describes one consolidation pruning pass.
type MemoryConsolidationPrunedPayload struct {
	// TotalRecords is the number of records before pruning.
	TotalRecords int
	// ExpiredCount is the number of records removed due to ValidUntil expiry.
	ExpiredCount int
	// DecayPrunedCount is the number of records removed due to low decay score.
	DecayPrunedCount int
	// KeptCount is the number of records retained after pruning.
	KeptCount int
}

// MemoryConsolidationCappedPayload describes overflow pruning at the cap.
type MemoryConsolidationCappedPayload struct {
	// TotalRecords is the number of records before capping.
	TotalRecords int
	// MaxAllowed is the configured maximum.
	MaxAllowed int
	// RemovedCount is the number of records removed to enforce the cap.
	RemovedCount int
}

// MemoryConsolidationCycleCompletedPayload describes one consolidation cycle
// pass across currently active scopes.
type MemoryConsolidationCycleCompletedPayload struct {
	// ScopeCount is the number of active scopes inspected this cycle.
	ScopeCount int
	// ElapsedMS is the total cycle duration in milliseconds.
	ElapsedMS int64
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
