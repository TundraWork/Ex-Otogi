package llmchat

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"ex-otogi/pkg/otogi/ai"
)

func TestRememberToolExecute(t *testing.T) {
	scope := ai.LLMMemoryScope{Platform: "telegram", ConversationID: "chat-1"}

	tests := []struct {
		name       string
		args       string
		embedResp  ai.EmbeddingResponse
		embedErr   error
		storeResp  ai.LLMMemoryRecord
		storeErr   error
		wantResult string
		wantErr    string
	}{
		{
			name:       "stores embedded memory entry",
			args:       `{"content":"Alice likes tea","category":"preference"}`,
			embedResp:  ai.EmbeddingResponse{Vectors: [][]float32{{0.5, 0.5}}},
			storeResp:  ai.LLMMemoryRecord{ID: "mem-1"},
			wantResult: "Remembered: Alice likes tea (id: mem-1)",
		},
		{
			name:    "rejects invalid args",
			args:    `{"content":"","category":"preference"}`,
			wantErr: "missing content",
		},
		{
			name:     "propagates embedding error",
			args:     `{"content":"Alice likes tea","category":"preference"}`,
			embedErr: errors.New("embed failed"),
			wantErr:  "embed failed",
		},
		{
			name:      "propagates store error",
			args:      `{"content":"Alice likes tea","category":"preference"}`,
			embedResp: ai.EmbeddingResponse{Vectors: [][]float32{{0.5, 0.5}}},
			storeErr:  errors.New("store failed"),
			wantErr:   "store failed",
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			embeddingProvider := &embeddingProviderStub{
				response: testCase.embedResp,
				err:      testCase.embedErr,
			}
			memoryService := &llmMemoryServiceStub{
				storeResponse: testCase.storeResp,
				storeErr:      testCase.storeErr,
			}

			result, err := newRememberTool(scope, embeddingProvider, memoryService).Execute(
				context.Background(),
				json.RawMessage(testCase.args),
			)
			if testCase.wantErr != "" {
				if err == nil {
					t.Fatal("Execute error = nil, want error")
				}
				if !containsAll(err.Error(), testCase.wantErr) {
					t.Fatalf("error = %q, want substring %q", err, testCase.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Execute failed: %v", err)
			}
			if result != testCase.wantResult {
				t.Fatalf("result = %q, want %q", result, testCase.wantResult)
			}
			if len(embeddingProvider.lastRequest.Texts) != 1 || embeddingProvider.lastRequest.Texts[0] != "Alice likes tea" {
				t.Fatalf("embed request texts = %v, want Alice likes tea", embeddingProvider.lastRequest.Texts)
			}
			if embeddingProvider.lastRequest.TaskType != ai.EmbeddingTaskTypeDocument {
				t.Fatalf("embed task type = %q, want %q", embeddingProvider.lastRequest.TaskType, ai.EmbeddingTaskTypeDocument)
			}
			if memoryService.lastStore.Scope != scope {
				t.Fatalf("store scope = %+v, want %+v", memoryService.lastStore.Scope, scope)
			}
			if memoryService.lastStore.Content != "Alice likes tea" {
				t.Fatalf("store content = %q, want Alice likes tea", memoryService.lastStore.Content)
			}
			if memoryService.lastStore.Category != "preference" {
				t.Fatalf("store category = %q, want preference", memoryService.lastStore.Category)
			}
		})
	}
}

func TestRecallToolExecute(t *testing.T) {
	scope := ai.LLMMemoryScope{Platform: "telegram", ConversationID: "chat-1"}
	createdAt := time.Date(2026, time.March, 10, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name          string
		args          string
		embedResp     ai.EmbeddingResponse
		embedErr      error
		searchResp    []ai.LLMMemoryMatch
		searchErr     error
		wantJSONCount int
		wantResult    string
		wantErr       string
		wantLimit     int
	}{
		{
			name:      "returns json search results and caps limit",
			args:      `{"query":"tea preferences","limit":20}`,
			embedResp: ai.EmbeddingResponse{Vectors: [][]float32{{0.7, 0.3}}},
			searchResp: []ai.LLMMemoryMatch{
				{
					Record: ai.LLMMemoryRecord{
						ID:        "mem-1",
						Content:   "Alice likes tea",
						Category:  "preference",
						CreatedAt: createdAt,
					},
					Similarity: 0.91,
				},
			},
			wantJSONCount: 1,
			wantLimit:     maxRecallToolLimit,
		},
		{
			name:       "returns no-results message",
			args:       `{"query":"missing"}`,
			embedResp:  ai.EmbeddingResponse{Vectors: [][]float32{{0.7, 0.3}}},
			wantResult: "No relevant memories found.",
			wantLimit:  defaultRecallToolLimit,
		},
		{
			name:    "rejects invalid args",
			args:    `{"query":"","limit":3}`,
			wantErr: "missing query",
		},
		{
			name:      "propagates search error",
			args:      `{"query":"tea preferences"}`,
			embedResp: ai.EmbeddingResponse{Vectors: [][]float32{{0.7, 0.3}}},
			searchErr: errors.New("search failed"),
			wantErr:   "search failed",
			wantLimit: defaultRecallToolLimit,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			embeddingProvider := &embeddingProviderStub{
				response: testCase.embedResp,
				err:      testCase.embedErr,
			}
			memoryService := &llmMemoryServiceStub{
				searchResponse: testCase.searchResp,
				searchErr:      testCase.searchErr,
			}

			result, err := newRecallTool(scope, embeddingProvider, memoryService).Execute(
				context.Background(),
				json.RawMessage(testCase.args),
			)
			if testCase.wantErr != "" {
				if err == nil {
					t.Fatal("Execute error = nil, want error")
				}
				if !containsAll(err.Error(), testCase.wantErr) {
					t.Fatalf("error = %q, want substring %q", err, testCase.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Execute failed: %v", err)
			}
			if testCase.wantResult != "" {
				if result != testCase.wantResult {
					t.Fatalf("result = %q, want %q", result, testCase.wantResult)
				}
			} else {
				var decoded []map[string]any
				if err := json.Unmarshal([]byte(result), &decoded); err != nil {
					t.Fatalf("result is not valid json array: %v (result=%s)", err, result)
				}
				if len(decoded) != testCase.wantJSONCount {
					t.Fatalf("json result count = %d, want %d", len(decoded), testCase.wantJSONCount)
				}
				if decoded[0]["id"] != "mem-1" {
					t.Fatalf("json result id = %v, want mem-1", decoded[0]["id"])
				}
				if decoded[0]["category"] != "preference" {
					t.Fatalf("json result category = %v, want preference", decoded[0]["category"])
				}
				if decoded[0]["created_at"] != createdAt.Format(time.RFC3339) {
					t.Fatalf("json result created_at = %v, want %s", decoded[0]["created_at"], createdAt.Format(time.RFC3339))
				}
			}
			if embeddingProvider.lastRequest.TaskType != ai.EmbeddingTaskTypeQuery {
				t.Fatalf("embed task type = %q, want %q", embeddingProvider.lastRequest.TaskType, ai.EmbeddingTaskTypeQuery)
			}
			if memoryService.lastSearch.Scope != scope {
				t.Fatalf("search scope = %+v, want %+v", memoryService.lastSearch.Scope, scope)
			}
			if memoryService.lastSearch.Limit != testCase.wantLimit {
				t.Fatalf("search limit = %d, want %d", memoryService.lastSearch.Limit, testCase.wantLimit)
			}
			if memoryService.lastSearch.MinSimilarity != 0.3 {
				t.Fatalf("search min similarity = %f, want 0.3", memoryService.lastSearch.MinSimilarity)
			}
		})
	}
}

func TestForgetToolExecute(t *testing.T) {
	tests := []struct {
		name       string
		args       string
		deleteErr  error
		wantResult string
		wantErr    string
	}{
		{
			name:       "deletes memory by id",
			args:       `{"memory_id":"mem-1"}`,
			wantResult: "Forgotten memory mem-1.",
		},
		{
			name:    "rejects invalid args",
			args:    `{"memory_id":""}`,
			wantErr: "missing memory_id",
		},
		{
			name:      "propagates delete error",
			args:      `{"memory_id":"mem-1"}`,
			deleteErr: errors.New("delete failed"),
			wantErr:   "delete failed",
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			memoryService := &llmMemoryServiceStub{deleteErr: testCase.deleteErr}

			result, err := newForgetTool(memoryService).Execute(
				context.Background(),
				json.RawMessage(testCase.args),
			)
			if testCase.wantErr != "" {
				if err == nil {
					t.Fatal("Execute error = nil, want error")
				}
				if !containsAll(err.Error(), testCase.wantErr) {
					t.Fatalf("error = %q, want substring %q", err, testCase.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Execute failed: %v", err)
			}
			if result != testCase.wantResult {
				t.Fatalf("result = %q, want %q", result, testCase.wantResult)
			}
			if memoryService.lastDeleteID != "mem-1" {
				t.Fatalf("delete id = %q, want mem-1", memoryService.lastDeleteID)
			}
		})
	}
}

func TestBuildSemanticMemoryToolRegistry(t *testing.T) {
	scope := ai.LLMMemoryScope{Platform: "telegram", ConversationID: "chat-1"}

	if registry := buildSemanticMemoryToolRegistry(scope, nil, &llmMemoryServiceStub{}); registry != nil {
		t.Fatal("registry with nil embedding provider = non-nil, want nil")
	}
	if registry := buildSemanticMemoryToolRegistry(scope, &embeddingProviderStub{}, nil); registry != nil {
		t.Fatal("registry with nil memory service = non-nil, want nil")
	}

	registry := buildSemanticMemoryToolRegistry(scope, &embeddingProviderStub{}, &llmMemoryServiceStub{})
	if registry == nil || !registry.HasTools() {
		t.Fatal("registry = nil or empty, want semantic memory tools")
	}
	if len(registry.Definitions()) != 3 {
		t.Fatalf("tool definitions len = %d, want 3", len(registry.Definitions()))
	}
}

type embeddingProviderStub struct {
	lastRequest ai.EmbeddingRequest
	response    ai.EmbeddingResponse
	err         error
}

func (s *embeddingProviderStub) Embed(_ context.Context, req ai.EmbeddingRequest) (ai.EmbeddingResponse, error) {
	s.lastRequest = req
	if s.err != nil {
		return ai.EmbeddingResponse{}, s.err
	}

	vectors := make([][]float32, 0, len(s.response.Vectors))
	for _, vector := range s.response.Vectors {
		vectors = append(vectors, append([]float32(nil), vector...))
	}

	return ai.EmbeddingResponse{Vectors: vectors}, nil
}

type llmMemoryServiceStub struct {
	lastStore      ai.LLMMemoryEntry
	storeResponse  ai.LLMMemoryRecord
	storeErr       error
	lastSearch     ai.LLMMemoryQuery
	searchResponse []ai.LLMMemoryMatch
	searchErr      error
	lastDeleteID   string
	deleteErr      error
}

func (s *llmMemoryServiceStub) Store(_ context.Context, entry ai.LLMMemoryEntry) (ai.LLMMemoryRecord, error) {
	s.lastStore = entry
	if s.storeErr != nil {
		return ai.LLMMemoryRecord{}, s.storeErr
	}
	return s.storeResponse, nil
}

func (s *llmMemoryServiceStub) Search(_ context.Context, query ai.LLMMemoryQuery) ([]ai.LLMMemoryMatch, error) {
	s.lastSearch = query
	if s.searchErr != nil {
		return nil, s.searchErr
	}
	return append([]ai.LLMMemoryMatch(nil), s.searchResponse...), nil
}

func (*llmMemoryServiceStub) Update(context.Context, string, string, []float32) error {
	return nil
}

func (s *llmMemoryServiceStub) Delete(_ context.Context, id string) error {
	s.lastDeleteID = id
	return s.deleteErr
}

func (*llmMemoryServiceStub) ListByScope(context.Context, ai.LLMMemoryScope, int) ([]ai.LLMMemoryRecord, error) {
	return nil, nil
}

func containsAll(value string, substrings ...string) bool {
	for _, substring := range substrings {
		if !strings.Contains(value, substring) {
			return false
		}
	}

	return true
}
