package llmchat

import (
	"context"
	"errors"
	"io"

	"ex-otogi/pkg/otogi/ai"
)

type embeddingProviderStub struct {
	response    ai.EmbeddingResponse
	err         error
	lastRequest ai.EmbeddingRequest
}

func (s *embeddingProviderStub) Embed(_ context.Context, req ai.EmbeddingRequest) (ai.EmbeddingResponse, error) {
	s.lastRequest = req
	if s.err != nil {
		return ai.EmbeddingResponse{}, s.err
	}

	return s.response, nil
}

type llmMemoryServiceStub struct {
	storeResponse  ai.LLMMemoryRecord
	storeErr       error
	searchResponse []ai.LLMMemoryMatch
	searchErr      error
	listResponse   []ai.LLMMemoryRecord
	listErr        error
	updateResponse ai.LLMMemoryRecord
	updateErr      error
	deleteErr      error
	lastSearch     ai.LLMMemoryQuery
	searchCalls    []ai.LLMMemoryQuery
	lastUpdate     ai.LLMMemoryUpdate
	updateCalls    []ai.LLMMemoryUpdate
}

func (s *llmMemoryServiceStub) Store(_ context.Context, entry ai.LLMMemoryEntry) (ai.LLMMemoryRecord, error) {
	if s.storeErr != nil {
		return ai.LLMMemoryRecord{}, s.storeErr
	}
	if s.storeResponse.ID != "" {
		return s.storeResponse, nil
	}

	return ai.LLMMemoryRecord{
		ID:        "stored-memory",
		Scope:     entry.Scope,
		Content:   entry.Content,
		Category:  entry.Category,
		Embedding: append([]float32(nil), entry.Embedding...),
		Metadata:  cloneStringMap(entry.Metadata),
	}, nil
}

func (s *llmMemoryServiceStub) Search(_ context.Context, query ai.LLMMemoryQuery) ([]ai.LLMMemoryMatch, error) {
	s.lastSearch = query
	s.searchCalls = append(s.searchCalls, query)
	if s.searchErr != nil {
		return nil, s.searchErr
	}

	return append([]ai.LLMMemoryMatch(nil), s.searchResponse...), nil
}

func (s *llmMemoryServiceStub) Update(_ context.Context, update ai.LLMMemoryUpdate) (ai.LLMMemoryRecord, error) {
	s.lastUpdate = update
	s.updateCalls = append(s.updateCalls, update)
	if s.updateErr != nil {
		return ai.LLMMemoryRecord{}, s.updateErr
	}
	if s.updateResponse.ID != "" {
		return s.updateResponse, nil
	}

	return ai.LLMMemoryRecord{
		ID:        update.ID,
		Content:   update.Content,
		Category:  update.Category,
		Embedding: append([]float32(nil), update.Embedding...),
		Profile:   update.Profile,
		Metadata:  cloneStringMap(update.Metadata),
	}, nil
}

func (s *llmMemoryServiceStub) Delete(context.Context, string) error {
	return s.deleteErr
}

func (s *llmMemoryServiceStub) ListByScope(
	_ context.Context,
	scope ai.LLMMemoryScope,
	limit int,
) ([]ai.LLMMemoryRecord, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}

	records := make([]ai.LLMMemoryRecord, 0, len(s.listResponse))
	for _, record := range s.listResponse {
		if scope != (ai.LLMMemoryScope{}) && record.Scope != scope {
			continue
		}
		records = append(records, record)
	}
	if limit > 0 && len(records) > limit {
		records = records[:limit]
	}

	return records, nil
}

type llmProviderStub struct {
	stream   ai.LLMStream
	streams  []ai.LLMStream
	err      error
	lastReq  ai.LLMGenerateRequest
	requests []ai.LLMGenerateRequest
}

func (s *llmProviderStub) GenerateStream(_ context.Context, req ai.LLMGenerateRequest) (ai.LLMStream, error) {
	s.lastReq = req
	s.requests = append(s.requests, req)
	if s.err != nil {
		return nil, s.err
	}
	if len(s.streams) > 0 {
		stream := s.streams[0]
		s.streams = s.streams[1:]
		return stream, nil
	}
	if s.stream == nil {
		return nil, errors.New("nil stream")
	}

	return s.stream, nil
}

type llmStreamStub struct {
	chunks   []ai.LLMGenerateChunk
	index    int
	recvErr  error
	closeErr error
}

func (s *llmStreamStub) Recv(context.Context) (ai.LLMGenerateChunk, error) {
	if s.recvErr != nil {
		return ai.LLMGenerateChunk{}, s.recvErr
	}
	if s.index >= len(s.chunks) {
		return ai.LLMGenerateChunk{}, io.EOF
	}
	chunk := s.chunks[s.index]
	s.index++
	return chunk, nil
}

func (s *llmStreamStub) Close() error {
	return s.closeErr
}

type llmProviderRegistryStub struct {
	providers map[string]ai.LLMProvider
	err       error
}

func (s *llmProviderRegistryStub) Resolve(provider string) (ai.LLMProvider, error) {
	if s.err != nil {
		return nil, s.err
	}
	resolved, ok := s.providers[provider]
	if !ok {
		return nil, errors.New("provider not found")
	}

	return resolved, nil
}
