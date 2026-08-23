package management

// ChatRequestPayload describes one end-to-end chat request lifecycle event.
type ChatRequestPayload struct {
	// Agent identifies the selected chat agent.
	Agent string
	// Provider identifies the configured provider profile.
	Provider string
	// Model identifies the requested model.
	Model string
	// Outcome is accepted, completed, failed, or canceled.
	Outcome string
	// State is the current request lifecycle state.
	State string
	// PreviousState is populated for state transition events.
	PreviousState string
	// ElapsedMS is the request duration for terminal events.
	ElapsedMS int64
	// FailureClass is a stable coarse failure category when the request failed.
	FailureClass string
	// Error is the wrapped internal failure detail when the request failed.
	Error string
	// MaxAttempts is the effective application-level provider attempt budget.
	MaxAttempts int
}

// MemoryRetrieveCompletedPayload describes retrieval completion details.
type MemoryRetrieveCompletedPayload struct {
	// Outcome is completed, degraded, or failed.
	Outcome string
	// QueryCount is the number of semantic queries executed.
	QueryCount int
	// CandidateCount is the number of candidates considered before selection.
	CandidateCount int
	// ResultCount is the number of memories returned to the caller.
	ResultCount int
	// PlannerUsed reports whether an LLM retrieval planner was enabled.
	PlannerUsed bool
	// Degradation explains why heuristic planning replaced the LLM planner.
	Degradation string
	// ElapsedMS is the end-to-end retrieval duration in milliseconds.
	ElapsedMS int64
	// Error is the wrapped retrieval failure when Outcome is failed.
	Error string
}

// MemoryExtractCompletedPayload describes the outcome of one extraction pass.
type MemoryExtractCompletedPayload struct {
	// Outcome is completed or failed.
	Outcome string
	// Reason describes why the source window was flushed.
	Reason string
	// ArticleCount is the number of buffered articles processed.
	ArticleCount int
	// InputRunes is the serialized conversation size processed by formation.
	InputRunes int
	// BufferedMS is the time from the first buffered article until processing.
	BufferedMS int64
	// ExtractedCount is the number of candidate memories produced.
	ExtractedCount int
	// AppliedCount is the number of candidate actions successfully applied.
	AppliedCount int
	// FailedCount is the number of candidate actions that failed to apply.
	FailedCount int
	// Consolidated reports whether post-processing consolidated the candidates.
	Consolidated bool
	// ElapsedMS is the end-to-end formation duration in milliseconds.
	ElapsedMS int64
	// Error is the wrapped formation failure when Outcome is failed.
	Error string
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
	// Attempt is the one-based application-level attempt number.
	Attempt int
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
	// Attempt is the one-based application-level attempt number.
	Attempt int
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
	// Attempt is the one-based application-level attempt number.
	Attempt int
	// FailureClass is the stable coarse provider failure category.
	FailureClass string
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
	// Attempt is the one-based application-level attempt number.
	Attempt int
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
	// Attempt is the one-based application-level attempt number.
	Attempt int
}

// LLMCallFailedPayload describes one failed LLM generation request.
type LLMCallFailedPayload struct {
	// Provider identifies the LLM provider.
	Provider string
	// Model identifies the requested model.
	Model string
	// ElapsedMS is the provider call duration in milliseconds.
	ElapsedMS int64
	// Attempt is the one-based application-level attempt number.
	Attempt int
	// FailureClass is the stable coarse provider failure category.
	FailureClass string
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
