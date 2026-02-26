package gemini

import (
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"sync"

	"ex-otogi/pkg/otogi"

	"google.golang.org/genai"
)

type geminiStream struct {
	mu sync.Mutex

	next func() (*genai.GenerateContentResponse, error, bool)
	stop func()

	closed          bool
	finished        bool
	includeThoughts bool
	pending         []otogi.LLMGenerateChunk
}

func newGeminiStream(
	seq iter.Seq2[*genai.GenerateContentResponse, error],
	includeThoughts bool,
) *geminiStream {
	next, stop := iter.Pull2(seq)
	return &geminiStream{
		next:            next,
		stop:            stop,
		includeThoughts: includeThoughts,
	}
}

func (s *geminiStream) Recv(ctx context.Context) (otogi.LLMGenerateChunk, error) {
	if ctx == nil {
		return otogi.LLMGenerateChunk{}, fmt.Errorf("gemini stream recv: nil context")
	}

	for {
		if err := ctx.Err(); err != nil {
			_ = s.Close()
			return otogi.LLMGenerateChunk{}, fmt.Errorf("gemini stream recv context: %w", err)
		}
		if chunk, ok := s.dequeuePending(); ok {
			if chunk.Delta == "" {
				continue
			}
			return chunk, nil
		}

		response, err := s.nextResponse(ctx)
		if err != nil {
			return otogi.LLMGenerateChunk{}, err
		}

		chunks, mapErr := mapGenerateContentResponse(response, s.includeThoughts)
		if mapErr != nil {
			return otogi.LLMGenerateChunk{}, mapErr
		}
		if len(chunks) == 0 {
			continue
		}
		if len(chunks) > 1 {
			s.enqueuePending(chunks[1:])
		}

		if chunks[0].Delta == "" {
			continue
		}
		return chunks[0], nil
	}
}

func (s *geminiStream) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.finished = true
	stop := s.stop
	s.stop = nil
	s.next = nil
	s.mu.Unlock()

	if stop != nil {
		stop()
	}

	return nil
}

func (s *geminiStream) nextResponse(ctx context.Context) (*genai.GenerateContentResponse, error) {
	s.mu.Lock()
	if s.closed || s.finished {
		s.mu.Unlock()
		return nil, io.EOF
	}
	next := s.next
	if next == nil {
		s.finished = true
		s.mu.Unlock()
		return nil, io.EOF
	}
	s.mu.Unlock()

	response, recvErr, ok := next()
	if !ok {
		s.markFinished()
		return nil, io.EOF
	}
	if recvErr != nil {
		s.markFinished()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("gemini stream context: %w", ctxErr)
		}
		if errors.Is(recvErr, context.Canceled) || errors.Is(recvErr, context.DeadlineExceeded) {
			return nil, fmt.Errorf("gemini stream canceled: %w", recvErr)
		}
		return nil, fmt.Errorf("gemini stream next: %w", recvErr)
	}

	return response, nil
}

func (s *geminiStream) markFinished() {
	s.mu.Lock()
	s.finished = true
	s.mu.Unlock()
}

func (s *geminiStream) dequeuePending() (otogi.LLMGenerateChunk, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.pending) == 0 {
		return otogi.LLMGenerateChunk{}, false
	}

	chunk := s.pending[0]
	s.pending = s.pending[1:]
	return chunk, true
}

func (s *geminiStream) enqueuePending(chunks []otogi.LLMGenerateChunk) {
	if len(chunks) == 0 {
		return
	}

	s.mu.Lock()
	s.pending = append(s.pending, chunks...)
	s.mu.Unlock()
}

func mapGenerateContentResponse(
	response *genai.GenerateContentResponse,
	includeThoughts bool,
) ([]otogi.LLMGenerateChunk, error) {
	if response == nil {
		return nil, fmt.Errorf("gemini stream parse response: nil response")
	}
	if len(response.Candidates) == 0 || response.Candidates[0] == nil {
		return nil, nil
	}
	content := response.Candidates[0].Content
	if content == nil {
		return nil, nil
	}

	chunks := make([]otogi.LLMGenerateChunk, 0, len(content.Parts))
	for _, part := range content.Parts {
		if part == nil || part.Text == "" {
			continue
		}
		if part.Thought {
			if !includeThoughts {
				continue
			}
			chunks = append(chunks, otogi.LLMGenerateChunk{
				Kind:  otogi.LLMGenerateChunkKindThinkingSummary,
				Delta: part.Text,
			})
			continue
		}
		chunks = append(chunks, otogi.LLMGenerateChunk{
			Kind:  otogi.LLMGenerateChunkKindOutputText,
			Delta: part.Text,
		})
	}

	return chunks, nil
}

var _ otogi.LLMStream = (*geminiStream)(nil)
