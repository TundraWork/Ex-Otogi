package llmchat

import (
	"context"

	"ex-otogi/pkg/otogi/ai"
)

type semanticRetrieverStub struct {
	content    string
	matchCount int
	err        error
	available  bool
	lastReq    ai.SemanticRetrievalRequest
}

func (s *semanticRetrieverStub) Retrieve(_ context.Context, req ai.SemanticRetrievalRequest) (ai.SemanticRetrievalResult, error) {
	s.lastReq = req
	if s.err != nil {
		return ai.SemanticRetrievalResult{}, s.err
	}
	return ai.SemanticRetrievalResult{Content: s.content, MatchCount: s.matchCount}, nil
}

func (s *semanticRetrieverStub) Available(string) bool { return s.available }
