package gemini

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"ex-otogi/pkg/llm"
	"ex-otogi/pkg/otogi/ai"
	panel "ex-otogi/pkg/otogi/management"
)

type observedLLMStream struct {
	base      ai.LLMStream
	ctx       context.Context
	recorder  panel.Recorder
	provider  string
	model     string
	startedAt time.Time

	mu             sync.Mutex
	finished       bool
	outputObserved bool
}

func newObservedLLMStream(
	ctx context.Context,
	base ai.LLMStream,
	recorder panel.Recorder,
	provider string,
	model string,
	startedAt time.Time,
) ai.LLMStream {
	return &observedLLMStream{
		base:      base,
		ctx:       ctx,
		recorder:  recorder,
		provider:  provider,
		model:     model,
		startedAt: startedAt,
	}
}

func (s *observedLLMStream) Recv(ctx context.Context) (ai.LLMGenerateChunk, error) {
	chunk, err := s.base.Recv(ctx)
	if err == nil {
		if chunk.Kind.Normalize() == ai.LLMGenerateChunkKindOutputText || chunk.Kind.Normalize() == ai.LLMGenerateChunkKindThinkingSummary {
			s.mu.Lock()
			s.outputObserved = s.outputObserved || strings.TrimSpace(chunk.Delta) != ""
			s.mu.Unlock()
		}
		return chunk, nil
	}
	if errors.Is(err, io.EOF) {
		s.finishSuccess()
		return ai.LLMGenerateChunk{}, fmt.Errorf("gemini observed stream recv: %w", err)
	}
	s.finishFailure(err)
	return ai.LLMGenerateChunk{}, fmt.Errorf("gemini observed stream recv: %w", err)
}

func (s *observedLLMStream) Close() error {
	err := s.base.Close()
	if err != nil {
		s.finishFailure(err)
		return fmt.Errorf("gemini observed stream close: %w", err)
	}
	return nil
}

func (s *observedLLMStream) finishSuccess() {
	s.mu.Lock()
	if s.finished {
		s.mu.Unlock()
		return
	}
	s.finished = true
	outputObserved := s.outputObserved
	s.mu.Unlock()

	elapsedMS := time.Since(s.startedAt).Milliseconds()
	_, err := s.recorder.RecordEvent(s.ctx, panel.Event{
		Category:    panel.EventCategoryLLM,
		Kind:        "llm.call.completed",
		Level:       panel.EventLevelInfo,
		Module:      "llm-provider",
		Component:   s.provider,
		Subject:     "completed llm provider call",
		Description: panel.TruncateDescription(fmt.Sprintf("%s/%s completed in %dms", s.provider, s.model, elapsedMS)),
		Payload: panel.LLMCallCompletedPayload{
			Provider:           s.provider,
			Model:              s.model,
			ElapsedMS:          elapsedMS,
			OutputMessageCount: boolToCount(outputObserved),
			Attempt:            llm.AttemptFromContext(s.ctx),
		},
	})
	if err != nil {
		return
	}
}

func (s *observedLLMStream) finishFailure(streamErr error) {
	s.mu.Lock()
	if s.finished {
		s.mu.Unlock()
		return
	}
	s.finished = true
	s.mu.Unlock()

	_, err := s.recorder.RecordEvent(s.ctx, panel.Event{
		Category:    panel.EventCategoryLLM,
		Kind:        "llm.call.failed",
		Level:       panel.EventLevelError,
		Module:      "llm-provider",
		Component:   s.provider,
		Subject:     "llm provider call failed",
		Description: panel.TruncateDescription(fmt.Sprintf("%s/%s: %s", s.provider, s.model, streamErr.Error())),
		Payload: panel.LLMCallFailedPayload{
			Provider:  s.provider,
			Model:     s.model,
			ElapsedMS: time.Since(s.startedAt).Milliseconds(),
			Attempt:   llm.AttemptFromContext(s.ctx), FailureClass: llm.ProviderFailureClass(streamErr),
			Error: streamErr.Error(),
		},
	})
	if err != nil {
		return
	}
}

func recordLLMStart(
	ctx context.Context,
	recorder panel.Recorder,
	provider string,
	req ai.LLMGenerateRequest,
	startedAt time.Time,
) {
	_, err := recorder.RecordEvent(ctx, panel.Event{
		OccurredAt:  startedAt.UTC(),
		Category:    panel.EventCategoryLLM,
		Kind:        "llm.call.started",
		Level:       panel.EventLevelDebug,
		Module:      "llm-provider",
		Component:   provider,
		Subject:     "started llm provider call",
		Description: llmRequestDescription(req),
		Payload: panel.LLMCallStartedPayload{
			Provider:     provider,
			Model:        req.Model,
			MessageCount: len(req.Messages),
			ToolCount:    len(req.Tools),
			Attempt:      llm.AttemptFromContext(ctx),
		},
	})
	_ = err
}

func recordEmbeddingStart(
	ctx context.Context,
	recorder panel.Recorder,
	provider string,
	req ai.EmbeddingRequest,
	startedAt time.Time,
) {
	_, err := recorder.RecordEvent(ctx, panel.Event{
		OccurredAt:  startedAt.UTC(),
		Category:    panel.EventCategoryEmbedding,
		Kind:        "embedding.call.started",
		Level:       panel.EventLevelDebug,
		Module:      "embedding-provider",
		Component:   provider,
		Subject:     "started embedding provider call",
		Description: embeddingRequestDescription(req),
		Payload: panel.EmbeddingCallStartedPayload{
			Provider:   provider,
			Model:      req.Model,
			TaskType:   string(req.TaskType),
			InputCount: len(req.Texts),
			Attempt:    llm.AttemptFromContext(ctx),
		},
	})
	if err != nil {
		return
	}
}

func recordEmbeddingCompleted(
	ctx context.Context,
	recorder panel.Recorder,
	provider string,
	req ai.EmbeddingRequest,
	resp ai.EmbeddingResponse,
	startedAt time.Time,
) {
	dimensions := 0
	if len(resp.Vectors) > 0 {
		dimensions = len(resp.Vectors[0])
	}
	_, err := recorder.RecordEvent(ctx, panel.Event{
		Category:    panel.EventCategoryEmbedding,
		Kind:        "embedding.call.completed",
		Level:       panel.EventLevelDebug,
		Module:      "embedding-provider",
		Component:   provider,
		Subject:     "completed embedding provider call",
		Description: embeddingCompletionDescription(req, dimensions, time.Since(startedAt).Milliseconds()),
		Payload: panel.EmbeddingCallCompletedPayload{
			Provider:   provider,
			Model:      req.Model,
			TaskType:   string(req.TaskType),
			InputCount: len(req.Texts),
			Dimensions: dimensions,
			ElapsedMS:  time.Since(startedAt).Milliseconds(),
			Attempt:    llm.AttemptFromContext(ctx),
		},
	})
	if err != nil {
		return
	}
}

func recordEmbeddingFailed(
	ctx context.Context,
	recorder panel.Recorder,
	provider string,
	req ai.EmbeddingRequest,
	startedAt time.Time,
	callErr error,
) {
	_, err := recorder.RecordEvent(ctx, panel.Event{
		Category:    panel.EventCategoryEmbedding,
		Kind:        "embedding.call.failed",
		Level:       panel.EventLevelError,
		Module:      "embedding-provider",
		Component:   provider,
		Subject:     "embedding provider call failed",
		Description: embeddingFailureDescription(req, callErr),
		Payload: panel.EmbeddingCallFailedPayload{
			Provider:  provider,
			Model:     req.Model,
			TaskType:  string(req.TaskType),
			ElapsedMS: time.Since(startedAt).Milliseconds(),
			Attempt:   llm.AttemptFromContext(ctx), FailureClass: llm.ProviderFailureClass(callErr),
			Error: callErr.Error(),
		},
	})
	if err != nil {
		return
	}
}

func boolToCount(value bool) int {
	if value {
		return 1
	}
	return 0
}

func llmRequestDescription(req ai.LLMGenerateRequest) string {
	return panel.TruncateDescription(fmt.Sprintf("%s: %d messages, %d tools", req.Model, len(req.Messages), len(req.Tools)))
}

func embeddingRequestDescription(req ai.EmbeddingRequest) string {
	return panel.TruncateDescription(embeddingInputSummary(req))
}

func embeddingCompletionDescription(req ai.EmbeddingRequest, dimensions int, elapsedMS int64) string {
	return panel.TruncateDescription(fmt.Sprintf("%s -> %d dims in %dms", embeddingInputSummary(req), dimensions, elapsedMS))
}

func embeddingFailureDescription(req ai.EmbeddingRequest, callErr error) string {
	if callErr == nil {
		return embeddingRequestDescription(req)
	}

	summary := embeddingInputSummary(req)
	if summary == "" {
		return panel.TruncateDescription(callErr.Error())
	}

	return panel.TruncateDescription(summary + ": " + callErr.Error())
}

func embeddingInputSummary(req ai.EmbeddingRequest) string {
	taskType := strings.TrimSpace(string(req.TaskType))
	switch {
	case taskType == "" && len(req.Texts) == 1:
		return "1 input"
	case taskType == "":
		return fmt.Sprintf("%d inputs", len(req.Texts))
	case len(req.Texts) == 1:
		return "1 " + taskType + " input"
	default:
		return fmt.Sprintf("%d %s inputs", len(req.Texts), taskType)
	}
}
