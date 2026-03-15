package openai

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"ex-otogi/pkg/otogi/ai"

	"github.com/openai/openai-go/v3/responses"
)

type openAIResponseStream interface {
	Next() bool
	Current() responses.ResponseStreamEventUnion
	Err() error
	Close() error
}

type openAIStream struct {
	mu        sync.Mutex
	stream    openAIResponseStream
	closed    bool
	finished  bool
	toolCalls map[string]openAIToolCallMeta
}

type openAIToolCallMeta struct {
	callID string
	name   string
}

func newOpenAIStream(stream openAIResponseStream) *openAIStream {
	return &openAIStream{
		stream:    stream,
		toolCalls: make(map[string]openAIToolCallMeta),
	}
}

func (s *openAIStream) Recv(ctx context.Context) (ai.LLMGenerateChunk, error) {
	if ctx == nil {
		return ai.LLMGenerateChunk{}, fmt.Errorf("openai stream recv: nil context")
	}

	for {
		if err := ctx.Err(); err != nil {
			_ = s.Close()
			return ai.LLMGenerateChunk{}, fmt.Errorf("openai stream recv context: %w", err)
		}

		event, err := s.nextEvent(ctx)
		if err != nil {
			return ai.LLMGenerateChunk{}, err
		}

		chunk, done, mapErr := mapOpenAIStreamEvent(event)
		if mapErr != nil {
			return ai.LLMGenerateChunk{}, mapErr
		}
		if done {
			s.markFinished()
			return ai.LLMGenerateChunk{}, io.EOF
		}
		chunk = s.resolveToolCallChunk(event, chunk)
		if chunk.Kind.Normalize() != ai.LLMGenerateChunkKindToolCall && chunk.Delta == "" {
			continue
		}

		return chunk, nil
	}
}

func (s *openAIStream) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.finished = true
	stream := s.stream
	s.stream = nil
	s.mu.Unlock()

	if stream == nil {
		return nil
	}
	if err := stream.Close(); err != nil {
		return fmt.Errorf("openai stream close: %w", err)
	}

	return nil
}

func (s *openAIStream) nextEvent(ctx context.Context) (responses.ResponseStreamEventUnion, error) {
	s.mu.Lock()
	if s.closed || s.finished {
		s.mu.Unlock()
		return responses.ResponseStreamEventUnion{}, io.EOF
	}
	stream := s.stream
	if stream == nil {
		s.finished = true
		s.mu.Unlock()
		return responses.ResponseStreamEventUnion{}, io.EOF
	}
	s.mu.Unlock()

	if !stream.Next() {
		err := stream.Err()
		s.mu.Lock()
		s.finished = true
		s.mu.Unlock()
		if err == nil {
			return responses.ResponseStreamEventUnion{}, io.EOF
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return responses.ResponseStreamEventUnion{}, fmt.Errorf("openai stream context: %w", ctxErr)
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return responses.ResponseStreamEventUnion{}, fmt.Errorf("openai stream canceled: %w", err)
		}

		return responses.ResponseStreamEventUnion{}, fmt.Errorf("openai stream next: %w", err)
	}

	event := stream.Current()

	s.mu.Lock()
	if s.closed || s.finished || s.stream == nil {
		s.mu.Unlock()
		return responses.ResponseStreamEventUnion{}, io.EOF
	}
	s.mu.Unlock()

	return event, nil
}

func (s *openAIStream) markFinished() {
	s.mu.Lock()
	s.finished = true
	s.mu.Unlock()
}

func (s *openAIStream) resolveToolCallChunk(
	event responses.ResponseStreamEventUnion,
	chunk ai.LLMGenerateChunk,
) ai.LLMGenerateChunk {
	if chunk.Kind.Normalize() != ai.LLMGenerateChunkKindToolCall {
		return chunk
	}

	switch strings.TrimSpace(event.Type) {
	case openAIEventOutputItemAdded:
		itemID := strings.TrimSpace(event.Item.ID)
		callID := strings.TrimSpace(event.Item.CallID)
		name := strings.TrimSpace(event.Item.Name)
		if itemID != "" && callID != "" {
			s.mu.Lock()
			s.toolCalls[itemID] = openAIToolCallMeta{callID: callID, name: name}
			s.mu.Unlock()
		}
	case openAIEventFunctionCallArgumentsDelta, openAIEventFunctionCallArgumentsDone:
		s.mu.Lock()
		meta, exists := s.toolCalls[strings.TrimSpace(event.ItemID)]
		s.mu.Unlock()
		if exists {
			if chunk.ToolCallID == "" {
				chunk.ToolCallID = meta.callID
			}
			if chunk.ToolCallName == "" {
				chunk.ToolCallName = meta.name
			}
		}
	}

	return chunk
}

func mapOpenAIStreamEvent(
	event responses.ResponseStreamEventUnion,
) (ai.LLMGenerateChunk, bool, error) {
	eventType := strings.TrimSpace(event.Type)
	if eventType == "" {
		return ai.LLMGenerateChunk{}, false, fmt.Errorf("openai stream parse event: missing type")
	}

	switch eventType {
	case openAIEventOutputTextDelta:
		if !event.JSON.Delta.Valid() {
			return ai.LLMGenerateChunk{}, false, openAIEventParseError(eventType, "missing delta")
		}
		return ai.LLMGenerateChunk{
			Kind:  ai.LLMGenerateChunkKindOutputText,
			Delta: event.Delta,
		}, false, nil
	case openAIEventReasoningSummaryTextDelta:
		if !event.JSON.Delta.Valid() {
			return ai.LLMGenerateChunk{}, false, openAIEventParseError(eventType, "missing delta")
		}
		return ai.LLMGenerateChunk{
			Kind:  ai.LLMGenerateChunkKindThinkingSummary,
			Delta: event.Delta,
		}, false, nil
	case openAIEventReasoningTextDelta:
		// Ignore raw reasoning content. llmchat only surfaces short summaries.
		return ai.LLMGenerateChunk{}, false, nil
	case openAIEventOutputItemAdded:
		if !event.JSON.Item.Valid() {
			return ai.LLMGenerateChunk{}, false, openAIEventParseError(eventType, "missing item")
		}
		if strings.TrimSpace(event.Item.Type) != "function_call" {
			return ai.LLMGenerateChunk{}, false, nil
		}
		if !event.Item.JSON.CallID.Valid() {
			return ai.LLMGenerateChunk{}, false, openAIEventParseError(eventType, "missing call_id")
		}
		if !event.Item.JSON.Name.Valid() {
			return ai.LLMGenerateChunk{}, false, openAIEventParseError(eventType, "missing name")
		}
		return ai.LLMGenerateChunk{
			Kind:         ai.LLMGenerateChunkKindToolCall,
			ToolCallID:   event.Item.CallID,
			ToolCallName: event.Item.Name,
		}, false, nil
	case openAIEventFunctionCallArgumentsDelta:
		if !event.JSON.Delta.Valid() {
			return ai.LLMGenerateChunk{}, false, openAIEventParseError(eventType, "missing delta")
		}
		if !event.JSON.ItemID.Valid() {
			return ai.LLMGenerateChunk{}, false, openAIEventParseError(eventType, "missing item_id")
		}
		return ai.LLMGenerateChunk{
			Kind:              ai.LLMGenerateChunkKindToolCall,
			ToolCallArguments: event.Delta,
		}, false, nil
	case openAIEventFunctionCallArgumentsDone:
		return ai.LLMGenerateChunk{}, false, nil
	case openAIEventCompleted:
		if !event.JSON.Response.Valid() {
			return ai.LLMGenerateChunk{}, false, openAIEventParseError(eventType, "missing response")
		}
		return ai.LLMGenerateChunk{}, true, nil
	case openAIEventFailed:
		if !event.JSON.Response.Valid() {
			return ai.LLMGenerateChunk{}, false, openAIEventParseError(eventType, "missing response")
		}
		status := strings.TrimSpace(string(event.Response.Status))
		if status == "" {
			status = "unknown"
		}
		return ai.LLMGenerateChunk{}, false, fmt.Errorf("openai stream response failed: status=%s", status)
	case openAIEventError:
		if !event.JSON.Message.Valid() {
			return ai.LLMGenerateChunk{}, false, openAIEventParseError(eventType, "missing message")
		}
		message := strings.TrimSpace(event.Message)
		if message == "" {
			return ai.LLMGenerateChunk{}, false, openAIEventParseError(eventType, "empty message")
		}
		code := strings.TrimSpace(event.Code)
		if code != "" {
			return ai.LLMGenerateChunk{}, false, fmt.Errorf("openai stream error %s: %s", code, message)
		}
		return ai.LLMGenerateChunk{}, false, fmt.Errorf("openai stream error: %s", message)
	default:
		// Keep non-text events forward-compatible.
		return ai.LLMGenerateChunk{}, false, nil
	}
}

func openAIEventParseError(eventType, reason string) error {
	return fmt.Errorf("openai stream parse event %s: %s", eventType, reason)
}

var _ ai.LLMStream = (*openAIStream)(nil)
