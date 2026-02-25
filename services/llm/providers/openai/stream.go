package openai

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"ex-otogi/pkg/otogi"

	"github.com/openai/openai-go/v3/responses"
)

type openAIResponseStream interface {
	Next() bool
	Current() responses.ResponseStreamEventUnion
	Err() error
	Close() error
}

type openAIStream struct {
	mu       sync.Mutex
	stream   openAIResponseStream
	closed   bool
	finished bool
}

func newOpenAIStream(stream openAIResponseStream) *openAIStream {
	return &openAIStream{stream: stream}
}

func (s *openAIStream) Recv(ctx context.Context) (otogi.LLMGenerateChunk, error) {
	if ctx == nil {
		return otogi.LLMGenerateChunk{}, fmt.Errorf("openai stream recv: nil context")
	}

	for {
		if err := ctx.Err(); err != nil {
			_ = s.Close()
			return otogi.LLMGenerateChunk{}, fmt.Errorf("openai stream recv context: %w", err)
		}

		event, err := s.nextEvent(ctx)
		if err != nil {
			return otogi.LLMGenerateChunk{}, err
		}

		chunk, done, mapErr := mapOpenAIStreamEvent(event)
		if mapErr != nil {
			return otogi.LLMGenerateChunk{}, mapErr
		}
		if done {
			s.markFinished()
			return otogi.LLMGenerateChunk{}, io.EOF
		}
		if chunk.Delta == "" {
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

	if !stream.Next() {
		err := stream.Err()
		if err == nil {
			s.finished = true
			s.mu.Unlock()
			return responses.ResponseStreamEventUnion{}, io.EOF
		}
		s.finished = true
		s.mu.Unlock()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return responses.ResponseStreamEventUnion{}, fmt.Errorf("openai stream context: %w", ctxErr)
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return responses.ResponseStreamEventUnion{}, fmt.Errorf("openai stream canceled: %w", err)
		}

		return responses.ResponseStreamEventUnion{}, fmt.Errorf("openai stream next: %w", err)
	}

	event := stream.Current()
	s.mu.Unlock()
	return event, nil
}

func (s *openAIStream) markFinished() {
	s.mu.Lock()
	s.finished = true
	s.mu.Unlock()
}

func mapOpenAIStreamEvent(
	event responses.ResponseStreamEventUnion,
) (otogi.LLMGenerateChunk, bool, error) {
	eventType := strings.TrimSpace(event.Type)
	if eventType == "" {
		return otogi.LLMGenerateChunk{}, false, fmt.Errorf("openai stream parse event: missing type")
	}

	switch eventType {
	case openAIEventOutputTextDelta:
		if !event.JSON.Delta.Valid() {
			return otogi.LLMGenerateChunk{}, false, openAIEventParseError(eventType, "missing delta")
		}
		return otogi.LLMGenerateChunk{Delta: event.Delta}, false, nil
	case openAIEventCompleted:
		if !event.JSON.Response.Valid() {
			return otogi.LLMGenerateChunk{}, false, openAIEventParseError(eventType, "missing response")
		}
		return otogi.LLMGenerateChunk{}, true, nil
	case openAIEventFailed:
		if !event.JSON.Response.Valid() {
			return otogi.LLMGenerateChunk{}, false, openAIEventParseError(eventType, "missing response")
		}
		status := strings.TrimSpace(string(event.Response.Status))
		if status == "" {
			status = "unknown"
		}
		return otogi.LLMGenerateChunk{}, false, fmt.Errorf("openai stream response failed: status=%s", status)
	case openAIEventError:
		if !event.JSON.Message.Valid() {
			return otogi.LLMGenerateChunk{}, false, openAIEventParseError(eventType, "missing message")
		}
		message := strings.TrimSpace(event.Message)
		if message == "" {
			return otogi.LLMGenerateChunk{}, false, openAIEventParseError(eventType, "empty message")
		}
		code := strings.TrimSpace(event.Code)
		if code != "" {
			return otogi.LLMGenerateChunk{}, false, fmt.Errorf("openai stream error %s: %s", code, message)
		}
		return otogi.LLMGenerateChunk{}, false, fmt.Errorf("openai stream error: %s", message)
	default:
		// Keep non-text events forward-compatible.
		return otogi.LLMGenerateChunk{}, false, nil
	}
}

func openAIEventParseError(eventType, reason string) error {
	return fmt.Errorf("openai stream parse event %s: %s", eventType, reason)
}

var _ otogi.LLMStream = (*openAIStream)(nil)
