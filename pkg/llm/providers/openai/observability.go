package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

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

	mu          sync.Mutex
	finished    bool
	responseBuf strings.Builder
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
			s.responseBuf.WriteString(chunk.Delta)
			s.mu.Unlock()
		}
		return chunk, nil
	}
	if errors.Is(err, io.EOF) {
		s.finishSuccess()
		return ai.LLMGenerateChunk{}, fmt.Errorf("openai observed stream recv: %w", err)
	}
	s.finishFailure(err)
	return ai.LLMGenerateChunk{}, fmt.Errorf("openai observed stream recv: %w", err)
}

func (s *observedLLMStream) Close() error {
	err := s.base.Close()
	if err != nil {
		s.finishFailure(err)
		return fmt.Errorf("openai observed stream close: %w", err)
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
	responseText := s.responseBuf.String()
	s.mu.Unlock()

	event, err := s.recorder.RecordEvent(s.ctx, panel.Event{
		Category:    panel.EventCategoryLLM,
		Kind:        "llm.call.completed",
		Level:       panel.EventLevelDebug,
		Module:      "llm-provider",
		Component:   s.provider,
		Summary:     "completed llm provider call",
		Description: panel.TruncateDescription(responseText),
		PayloadType: "LLMCallCompletedPayload",
		Payload: panel.LLMCallCompletedPayload{
			Provider:           s.provider,
			Model:              s.model,
			ElapsedMS:          time.Since(s.startedAt).Milliseconds(),
			OutputMessageCount: boolToCount(strings.TrimSpace(responseText) != ""),
		},
	})
	if err != nil {
		return
	}
	recordArtifact(s.ctx, s.recorder, event.ID, panel.ArtifactKindPromptResponse, responseText)
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
		Summary:     "llm provider call failed",
		Description: panel.TruncateDescription(fmt.Sprintf("%s: %s", s.provider, streamErr.Error())),
		PayloadType: "LLMCallFailedPayload",
		Payload: panel.LLMCallFailedPayload{
			Provider:  s.provider,
			Model:     s.model,
			ElapsedMS: time.Since(s.startedAt).Milliseconds(),
			Error:     streamErr.Error(),
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
	event, err := recorder.RecordEvent(ctx, panel.Event{
		OccurredAt:  startedAt.UTC(),
		Category:    panel.EventCategoryLLM,
		Kind:        "llm.call.started",
		Level:       panel.EventLevelDebug,
		Module:      "llm-provider",
		Component:   provider,
		Summary:     "started llm provider call",
		PayloadType: "LLMCallStartedPayload",
		Payload: panel.LLMCallStartedPayload{
			Provider:     provider,
			Model:        req.Model,
			MessageCount: len(req.Messages),
			ToolCount:    len(req.Tools),
		},
	})
	if err != nil {
		return
	}
	requestArtifact, marshalErr := json.Marshal(req)
	if marshalErr != nil {
		return
	}
	recordArtifact(ctx, recorder, event.ID, panel.ArtifactKindPromptFull, string(requestArtifact))
}

func recordEmbeddingStart(
	ctx context.Context,
	recorder panel.Recorder,
	provider string,
	req ai.EmbeddingRequest,
	startedAt time.Time,
) int64 {
	event, err := recorder.RecordEvent(ctx, panel.Event{
		OccurredAt:  startedAt.UTC(),
		Category:    panel.EventCategoryEmbedding,
		Kind:        "embedding.call.started",
		Level:       panel.EventLevelDebug,
		Module:      "embedding-provider",
		Component:   provider,
		Summary:     "started embedding provider call",
		PayloadType: "EmbeddingCallStartedPayload",
		Payload: panel.EmbeddingCallStartedPayload{
			Provider:   provider,
			Model:      req.Model,
			TaskType:   string(req.TaskType),
			InputCount: len(req.Texts),
		},
	})
	if err != nil {
		return 0
	}
	requestArtifact, marshalErr := json.Marshal(req.Texts)
	if marshalErr == nil {
		recordArtifact(ctx, recorder, event.ID, panel.ArtifactKindEmbeddingInput, string(requestArtifact))
	}
	return event.ID
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
		Summary:     "completed embedding provider call",
		PayloadType: "EmbeddingCallCompletedPayload",
		Payload: panel.EmbeddingCallCompletedPayload{
			Provider:   provider,
			Model:      req.Model,
			TaskType:   string(req.TaskType),
			InputCount: len(req.Texts),
			Dimensions: dimensions,
			ElapsedMS:  time.Since(startedAt).Milliseconds(),
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
		Summary:     "embedding provider call failed",
		Description: panel.TruncateDescription(callErr.Error()),
		PayloadType: "EmbeddingCallFailedPayload",
		Payload: panel.EmbeddingCallFailedPayload{
			Provider:  provider,
			Model:     req.Model,
			TaskType:  string(req.TaskType),
			ElapsedMS: time.Since(startedAt).Milliseconds(),
			Error:     callErr.Error(),
		},
	})
	if err != nil {
		return
	}
}

func recordArtifact(
	ctx context.Context,
	recorder panel.Recorder,
	eventID int64,
	kind panel.ArtifactKind,
	content string,
) {
	if strings.TrimSpace(content) == "" {
		return
	}
	_, err := recorder.RecordArtifact(ctx, panel.Artifact{
		ID:        fmt.Sprintf("art_%d_%d", eventID, time.Now().UnixNano()),
		EventID:   eventID,
		Kind:      kind,
		CreatedAt: time.Now().UTC(),
		Content:   content,
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
