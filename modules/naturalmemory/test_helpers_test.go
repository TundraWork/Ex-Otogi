package naturalmemory

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"ex-otogi/pkg/otogi/ai"
	"ex-otogi/pkg/otogi/core"
	"ex-otogi/pkg/otogi/platform"
)

type llmProviderStub struct {
	stream   ai.LLMStream
	streams  []ai.LLMStream
	err      error
	requests []ai.LLMGenerateRequest
	lastReq  ai.LLMGenerateRequest
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
		return nil, fmt.Errorf("nil stream")
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
		return nil, fmt.Errorf("provider %s not found", provider)
	}

	return resolved, nil
}

type embeddingRegistryStub struct {
	providers map[string]ai.EmbeddingProvider
	err       error
}

func (s *embeddingRegistryStub) Resolve(provider string) (ai.EmbeddingProvider, error) {
	if s.err != nil {
		return nil, s.err
	}
	resolved, ok := s.providers[provider]
	if !ok {
		return nil, fmt.Errorf("embedding provider %s not found", provider)
	}

	return resolved, nil
}

type recordingLLMMemoryService struct {
	storedEntries []ai.LLMMemoryEntry
	searchResp    []ai.LLMMemoryMatch
	searchErr     error
	listResp      []ai.LLMMemoryRecord
	listErr       error
	updates       []ai.LLMMemoryUpdate
	deleted       []string
	lastSearch    ai.LLMMemoryQuery
}

func (s *recordingLLMMemoryService) Store(_ context.Context, entry ai.LLMMemoryEntry) (ai.LLMMemoryRecord, error) {
	s.storedEntries = append(s.storedEntries, entry)
	record := ai.LLMMemoryRecord{
		ID:        fmt.Sprintf("mem-%d", len(s.storedEntries)),
		Scope:     entry.Scope,
		Content:   entry.Content,
		Category:  entry.Category,
		Embedding: append([]float32(nil), entry.Embedding...),
		Profile:   entry.Profile,
		Metadata:  cloneStringMap(entry.Metadata),
	}
	s.listResp = append(s.listResp, record)

	return record, nil
}

func (s *recordingLLMMemoryService) Search(_ context.Context, query ai.LLMMemoryQuery) ([]ai.LLMMemoryMatch, error) {
	s.lastSearch = query
	if s.searchErr != nil {
		return nil, s.searchErr
	}

	return append([]ai.LLMMemoryMatch(nil), s.searchResp...), nil
}

func (s *recordingLLMMemoryService) Update(
	_ context.Context,
	update ai.LLMMemoryUpdate,
) (ai.LLMMemoryRecord, error) {
	s.updates = append(s.updates, update)
	record := ai.LLMMemoryRecord{
		ID:        update.ID,
		Content:   update.Content,
		Category:  update.Category,
		Embedding: append([]float32(nil), update.Embedding...),
		Profile:   update.Profile,
		Metadata:  cloneStringMap(update.Metadata),
	}
	for index := range s.listResp {
		if s.listResp[index].ID != update.ID {
			continue
		}
		record.Scope = s.listResp[index].Scope
		record.CreatedAt = s.listResp[index].CreatedAt
		record.UpdatedAt = time.Now().UTC()
		s.listResp[index] = record
		return record, nil
	}

	return record, nil
}

func (s *recordingLLMMemoryService) Delete(_ context.Context, id string) error {
	s.deleted = append(s.deleted, id)
	filtered := make([]ai.LLMMemoryRecord, 0, len(s.listResp))
	for _, record := range s.listResp {
		if record.ID == id {
			continue
		}
		filtered = append(filtered, record)
	}
	s.listResp = filtered
	return nil
}

func (s *recordingLLMMemoryService) ListByScope(
	_ context.Context,
	scope ai.LLMMemoryScope,
	limit int,
) ([]ai.LLMMemoryRecord, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}

	records := make([]ai.LLMMemoryRecord, 0, len(s.listResp))
	for _, record := range s.listResp {
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

type memoryContextStub struct {
	leadingContext []core.ConversationContextEntry
	listErr        error
}

func (*memoryContextStub) Get(context.Context, core.MemoryLookup) (core.Memory, bool, error) {
	return core.Memory{}, false, nil
}

func (*memoryContextStub) GetBatch(context.Context, []core.MemoryLookup) (map[core.MemoryLookup]core.Memory, error) {
	return nil, nil
}

func (*memoryContextStub) GetReplied(context.Context, *platform.Event) (core.Memory, bool, error) {
	return core.Memory{}, false, nil
}

func (*memoryContextStub) GetReplyChain(context.Context, *platform.Event) ([]core.ReplyChainEntry, error) {
	return nil, nil
}

func (s *memoryContextStub) ListConversationContextBefore(
	_ context.Context,
	_ core.ConversationContextBeforeQuery,
) ([]core.ConversationContextEntry, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}

	return append([]core.ConversationContextEntry(nil), s.leadingContext...), nil
}

type registrationRuntimeStub struct {
	registry  core.ServiceRegistry
	configs   core.ConfigRegistry
	subscribe func(context.Context, core.InterestSet, core.SubscriptionSpec, core.EventHandler) (core.Subscription, error)
}

func (s registrationRuntimeStub) Services() core.ServiceRegistry {
	return s.registry
}

func (s registrationRuntimeStub) Config() core.ConfigRegistry {
	return s.configs
}

func (s registrationRuntimeStub) Subscribe(
	ctx context.Context,
	interest core.InterestSet,
	spec core.SubscriptionSpec,
	handler core.EventHandler,
) (core.Subscription, error) {
	if s.subscribe != nil {
		return s.subscribe(ctx, interest, spec, handler)
	}

	return registrationSubscriptionStub{name: spec.Name}, nil
}

type registrationSubscriptionStub struct {
	name string
}

func (s registrationSubscriptionStub) Name() string {
	return s.name
}

func (registrationSubscriptionStub) Close(context.Context) error {
	return nil
}

type recordingServiceRegistry struct {
	values map[string]any
}

func newRecordingServiceRegistry(values map[string]any) *recordingServiceRegistry {
	cloned := make(map[string]any, len(values))
	for name, value := range values {
		cloned[name] = value
	}

	return &recordingServiceRegistry{values: cloned}
}

func (r *recordingServiceRegistry) Register(name string, service any) error {
	if _, exists := r.values[name]; exists {
		return fmt.Errorf("register service %s: %w", name, core.ErrServiceAlreadyRegistered)
	}
	r.values[name] = service
	return nil
}

func (r *recordingServiceRegistry) Resolve(name string) (any, error) {
	service, exists := r.values[name]
	if !exists {
		return nil, core.ErrServiceNotFound
	}

	return service, nil
}

type configRegistryStub struct {
	configs map[string]json.RawMessage
}

func newConfigRegistryStub() *configRegistryStub {
	return &configRegistryStub{configs: make(map[string]json.RawMessage)}
}

func (r *configRegistryStub) Register(moduleName string, raw json.RawMessage) error {
	r.configs[moduleName] = append(json.RawMessage(nil), raw...)
	return nil
}

func (r *configRegistryStub) Resolve(moduleName string) (json.RawMessage, error) {
	raw, exists := r.configs[moduleName]
	if !exists {
		return nil, fmt.Errorf("resolve module config %s: %w", moduleName, core.ErrConfigNotFound)
	}

	return append(json.RawMessage(nil), raw...), nil
}

func writeNaturalMemoryLLMConfigFile(t testingT) string {
	t.Helper()

	path := filepath.Join(os.TempDir(), fmt.Sprintf("naturalmemory-%d.json", time.Now().UnixNano()))
	body := `{
		"request_timeout":"90s",
		"providers":{
			"openai-main":{"type":"openai","api_key":"sk-test"},
			"openai-consolidation":{"type":"openai","api_key":"sk-test"}
		},
		"natural_memory":{
			"enabled":true,
			"extraction_provider":"openai-main",
			"extraction_model":"gpt-4.1-mini",
			"embedding_provider":"openai-main",
			"extraction_timeout":"30s",
			"consolidation_interval":"1h",
			"consolidation_provider":"openai-consolidation",
			"consolidation_model":"gpt-4.1-mini",
			"consolidation_timeout":"60s"
		},
		"agents":[
			{
				"name":"Otogi",
				"description":"Assistant",
				"provider":"openai-main",
				"model":"gpt-5-mini",
				"system_prompt_template":"You are {{.AgentName}}",
				"request_timeout":"30s"
			}
		]
	}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write llm config: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })

	return path
}

func testNaturalMemoryConfigRegistry(t testingT, configFile string) core.ConfigRegistry {
	t.Helper()

	raw, err := json.Marshal(fileModuleConfig{ConfigFile: configFile})
	if err != nil {
		t.Fatalf("marshal module config: %v", err)
	}

	registry := newConfigRegistryStub()
	if err := registry.Register("naturalmemory", raw); err != nil {
		t.Fatalf("register module config: %v", err)
	}

	return registry
}

type testingT interface {
	Helper()
	Fatalf(string, ...any)
	Cleanup(func())
}

func cloneStringMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}

	cloned := make(map[string]string, len(input))
	for key, value := range input {
		cloned[key] = value
	}

	return cloned
}
