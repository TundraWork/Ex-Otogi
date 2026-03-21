package naturalmemory

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"ex-otogi/pkg/otogi/ai"
)

func validConsolidationConfig() Config {
	return Config{
		Enabled:                      true,
		DecayFactor:                  0.99,
		MinImportance:                1,
		MaxMemoriesPerScope:          10,
		DuplicateSimilarityThreshold: 0.85,
		ExtractionTimeout:            time.Second,
		ExtractionMaxInputRunes:      1000,
		ConsolidationInterval:        time.Hour,
		ConsolidationTimeout:         time.Second,
		ContextWindowSize:            5,
		ClusterMinSize:               3,
		ClusterSimilarityThreshold:   0.80,
		ClusterTemporalWeight:        0.3,
	}
}

func TestConsolidateScopePrunesDecayedMemories(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 16, 12, 0, 0, 0, time.UTC)
	scope := ai.LLMMemoryScope{Platform: "telegram", ConversationID: "chat-1"}
	store := newMemoryServiceStub([]ai.LLMMemoryRecord{
		newScopedRecord(scope, "keep", 5, now.Add(-1*time.Hour), []float32{1, 0}),
		newScopedRecord(scope, "prune", 3, now.Add(-8*time.Hour), []float32{0, 1}),
	})
	cfg := validConsolidationConfig()
	cfg.DecayFactor = 0.7
	cfg.MinImportance = 3
	module := New(withClock(func() time.Time { return now }), withConfig(cfg))
	module.llmMemory = store

	if err := module.consolidateScope(context.Background(), scope); err != nil {
		t.Fatalf("consolidateScope failed: %v", err)
	}

	records, err := store.ListByScope(context.Background(), scope, 0)
	if err != nil {
		t.Fatalf("ListByScope failed: %v", err)
	}
	if len(records) != 1 || records[0].ID != "keep" {
		t.Fatalf("records = %+v, want only keep", records)
	}
}

func TestConsolidateScopeMergesNearDuplicates(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 16, 12, 0, 0, 0, time.UTC)
	scope := ai.LLMMemoryScope{Platform: "telegram", ConversationID: "chat-1"}
	store := newMemoryServiceStub([]ai.LLMMemoryRecord{
		newScopedRecord(scope, "high", 8, now.Add(-1*time.Hour), []float32{1, 0}),
		newScopedRecord(scope, "low", 4, now.Add(-30*time.Minute), []float32{1, 0}),
	})
	module := New(withClock(func() time.Time { return now }), withConfig(validConsolidationConfig()))
	module.llmMemory = store

	if err := module.consolidateScope(context.Background(), scope); err != nil {
		t.Fatalf("consolidateScope failed: %v", err)
	}

	records, err := store.ListByScope(context.Background(), scope, 0)
	if err != nil {
		t.Fatalf("ListByScope failed: %v", err)
	}
	if len(records) != 1 || records[0].ID != "high" {
		t.Fatalf("records = %+v, want only high", records)
	}
}

func TestConsolidateScopeCapsLowestScores(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 16, 12, 0, 0, 0, time.UTC)
	scope := ai.LLMMemoryScope{Platform: "telegram", ConversationID: "chat-1"}
	store := newMemoryServiceStub([]ai.LLMMemoryRecord{
		newScopedRecord(scope, "a", 8, now.Add(-1*time.Hour), []float32{1, 0}),
		newScopedRecord(scope, "b", 7, now.Add(-2*time.Hour), []float32{0, 1}),
		newScopedRecord(scope, "c", 3, now.Add(-3*time.Hour), []float32{0, 0.5}),
	})
	cfg := validConsolidationConfig()
	cfg.DecayFactor = 1
	cfg.MaxMemoriesPerScope = 2
	module := New(withClock(func() time.Time { return now }), withConfig(cfg))
	module.llmMemory = store

	if err := module.consolidateScope(context.Background(), scope); err != nil {
		t.Fatalf("consolidateScope failed: %v", err)
	}

	records, err := store.ListByScope(context.Background(), scope, 0)
	if err != nil {
		t.Fatalf("ListByScope failed: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("record count = %d, want 2", len(records))
	}

	ids := []string{records[0].ID, records[1].ID}
	sort.Strings(ids)
	if ids[0] != "a" || ids[1] != "b" {
		t.Fatalf("ids = %v, want [a b]", ids)
	}
}

func TestConsolidateScopeDeletesExpiredMemories(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 16, 12, 0, 0, 0, time.UTC)
	past := now.Add(-1 * time.Hour)
	future := now.Add(24 * time.Hour)
	scope := ai.LLMMemoryScope{Platform: "telegram", ConversationID: "chat-1"}

	expiredRecord := newScopedRecord(scope, "expired", 8, now.Add(-1*time.Hour), []float32{1, 0})
	expiredRecord.Profile.ValidUntil = &past

	validRecord := newScopedRecord(scope, "valid", 8, now.Add(-1*time.Hour), []float32{0, 1})
	validRecord.Profile.ValidUntil = &future

	noExpiryRecord := newScopedRecord(scope, "no-expiry", 8, now.Add(-1*time.Hour), []float32{0.5, 0.5})

	store := newMemoryServiceStub([]ai.LLMMemoryRecord{expiredRecord, validRecord, noExpiryRecord})
	module := New(withClock(func() time.Time { return now }), withConfig(validConsolidationConfig()))
	module.llmMemory = store

	if err := module.consolidateScope(context.Background(), scope); err != nil {
		t.Fatalf("consolidateScope failed: %v", err)
	}

	records, err := store.ListByScope(context.Background(), scope, 0)
	if err != nil {
		t.Fatalf("ListByScope failed: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("record count = %d, want 2 (expired should be deleted)", len(records))
	}
	for _, record := range records {
		if record.ID == "expired" {
			t.Fatal("expired memory should have been deleted")
		}
	}
}

func TestMergeNearDuplicatesPreservesLinks(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 16, 12, 0, 0, 0, time.UTC)
	scope := ai.LLMMemoryScope{Platform: "telegram", ConversationID: "chat-1"}

	highRecord := newScopedRecord(scope, "high", 8, now.Add(-1*time.Hour), []float32{1, 0})
	highRecord.Links = []ai.LLMMemoryLink{{TargetID: "mem-a", Relation: "related"}}

	lowRecord := newScopedRecord(scope, "low", 4, now.Add(-30*time.Minute), []float32{1, 0})
	lowRecord.Links = []ai.LLMMemoryLink{
		{TargetID: "mem-a", Relation: "refines"},
		{TargetID: "mem-b", Relation: "related"},
	}

	store := newMemoryServiceStub([]ai.LLMMemoryRecord{highRecord, lowRecord})
	module := New(withClock(func() time.Time { return now }), withConfig(validConsolidationConfig()))
	module.llmMemory = store

	if err := module.consolidateScope(context.Background(), scope); err != nil {
		t.Fatalf("consolidateScope failed: %v", err)
	}

	records, err := store.ListByScope(context.Background(), scope, 0)
	if err != nil {
		t.Fatalf("ListByScope failed: %v", err)
	}
	if len(records) != 1 || records[0].ID != "high" {
		t.Fatalf("records = %+v, want only high", records)
	}
	// The kept record should have links from both records (deduplicated).
	if len(records[0].Links) != 2 {
		t.Fatalf("links len = %d, want 2 (union of both records' links)", len(records[0].Links))
	}
	linkTargets := make(map[string]bool)
	for _, link := range records[0].Links {
		linkTargets[link.TargetID] = true
	}
	if !linkTargets["mem-a"] || !linkTargets["mem-b"] {
		t.Fatalf("link targets = %v, want mem-a and mem-b", linkTargets)
	}
}

func TestAffinityClusterGroupsSimilarRecords(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 16, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name             string
		records          []ai.LLMMemoryRecord
		simThreshold     float32
		temporalWeight   float64
		decayFactor      float64
		wantClusterCount int
		wantLargest      int
	}{
		{
			name: "identical embeddings form one cluster",
			records: []ai.LLMMemoryRecord{
				{ID: "a", Embedding: []float32{1, 0}, CreatedAt: now.Add(-1 * time.Hour)},
				{ID: "b", Embedding: []float32{1, 0}, CreatedAt: now.Add(-2 * time.Hour)},
				{ID: "c", Embedding: []float32{1, 0}, CreatedAt: now.Add(-3 * time.Hour)},
			},
			simThreshold:     0.80,
			temporalWeight:   0.3,
			decayFactor:      0.99,
			wantClusterCount: 1,
			wantLargest:      3,
		},
		{
			name: "orthogonal embeddings form separate clusters",
			records: []ai.LLMMemoryRecord{
				{ID: "a", Embedding: []float32{1, 0}, CreatedAt: now.Add(-1 * time.Hour)},
				{ID: "b", Embedding: []float32{0, 1}, CreatedAt: now.Add(-1 * time.Hour)},
				{ID: "c", Embedding: []float32{1, 0}, CreatedAt: now.Add(-2 * time.Hour)},
			},
			simThreshold:     0.80,
			temporalWeight:   0.3,
			decayFactor:      0.99,
			wantClusterCount: 2,
			wantLargest:      2,
		},
		{
			name: "single record returns one cluster",
			records: []ai.LLMMemoryRecord{
				{ID: "a", Embedding: []float32{1, 0}, CreatedAt: now},
			},
			simThreshold:     0.80,
			temporalWeight:   0.3,
			decayFactor:      0.99,
			wantClusterCount: 1,
			wantLargest:      1,
		},
		{
			name:             "empty input returns nil",
			records:          nil,
			simThreshold:     0.80,
			temporalWeight:   0.3,
			decayFactor:      0.99,
			wantClusterCount: 0,
			wantLargest:      0,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			clusters := affinityCluster(
				testCase.records,
				testCase.simThreshold,
				testCase.temporalWeight,
				testCase.decayFactor,
			)
			if len(clusters) != testCase.wantClusterCount {
				t.Fatalf("cluster count = %d, want %d", len(clusters), testCase.wantClusterCount)
			}
			if testCase.wantLargest > 0 && len(clusters[0]) != testCase.wantLargest {
				t.Fatalf("largest cluster size = %d, want %d", len(clusters[0]), testCase.wantLargest)
			}
		})
	}
}

func TestConsolidateScopeGeneratesThemeFromLargeCluster(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 16, 12, 0, 0, 0, time.UTC)
	scope := ai.LLMMemoryScope{Platform: "telegram", ConversationID: "chat-1"}

	// 4 records with identical embeddings — will form one cluster of size 4.
	store := newMemoryServiceStub([]ai.LLMMemoryRecord{
		newScopedRecord(scope, "m-1", 6, now.Add(-1*time.Hour), []float32{1, 0}),
		newScopedRecord(scope, "m-2", 5, now.Add(-2*time.Hour), []float32{1, 0}),
		newScopedRecord(scope, "m-3", 7, now.Add(-3*time.Hour), []float32{1, 0}),
		newScopedRecord(scope, "m-4", 4, now.Add(-4*time.Hour), []float32{1, 0}),
	})

	themeLLM := &llmProviderStub{
		stream: &llmStreamStub{
			chunks: []ai.LLMGenerateChunk{
				{
					Kind:  ai.LLMGenerateChunkKindOutputText,
					Delta: `{"content":"User has consistent knowledge interests","importance":8,"subject_actor_id":"","subject_actor_name":""}`,
				},
			},
		},
	}
	embeddingStub := &embeddingProviderStub{
		response: ai.EmbeddingResponse{Vectors: [][]float32{{0.9, 0.1}}},
	}

	cfg := validConsolidationConfig()
	cfg.ClusterMinSize = 3
	cfg.ConsolidationModel = "test-model"
	module := New(withClock(func() time.Time { return now }), withConfig(cfg))
	module.llmMemory = store
	module.consolidationProvider = themeLLM
	module.embeddingProvider = embeddingStub

	if err := module.consolidateScope(context.Background(), scope); err != nil {
		t.Fatalf("consolidateScope failed: %v", err)
	}

	records, err := store.ListByScope(context.Background(), scope, 0)
	if err != nil {
		t.Fatalf("ListByScope failed: %v", err)
	}

	// All 4 originals should be deleted; 1 theme stored.
	if len(records) != 1 {
		t.Fatalf("record count = %d, want 1 (theme only)", len(records))
	}
	if records[0].Category != "theme" {
		t.Fatalf("record category = %q, want theme", records[0].Category)
	}
	if !strings.Contains(records[0].Content, "knowledge interests") {
		t.Fatalf("theme content = %q, want LLM-generated theme", records[0].Content)
	}
	if records[0].Profile.Kind != ai.LLMMemoryKindSynthesized {
		t.Fatalf("theme kind = %q, want synthesized", records[0].Profile.Kind)
	}
	if len(records[0].Profile.EvidenceRecordIDs) != 4 {
		t.Fatalf("evidence record IDs count = %d, want 4", len(records[0].Profile.EvidenceRecordIDs))
	}
	if records[0].Profile.Source != "natural.theme" {
		t.Fatalf("theme source = %q, want natural.theme", records[0].Profile.Source)
	}
	// Verify the LLM was called with the consolidation model.
	if themeLLM.lastReq.Model != "test-model" {
		t.Fatalf("theme model = %q, want test-model", themeLLM.lastReq.Model)
	}
}

func TestConsolidateScopeUsesPairwiseMergeForSmallClusters(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 16, 12, 0, 0, 0, time.UTC)
	scope := ai.LLMMemoryScope{Platform: "telegram", ConversationID: "chat-1"}

	// 2 records with identical embeddings — cluster size 2, below ClusterMinSize=3.
	store := newMemoryServiceStub([]ai.LLMMemoryRecord{
		newScopedRecord(scope, "high", 8, now.Add(-1*time.Hour), []float32{1, 0}),
		newScopedRecord(scope, "low", 4, now.Add(-30*time.Minute), []float32{1, 0}),
	})

	cfg := validConsolidationConfig()
	cfg.ClusterMinSize = 3
	module := New(withClock(func() time.Time { return now }), withConfig(cfg))
	module.llmMemory = store

	if err := module.consolidateScope(context.Background(), scope); err != nil {
		t.Fatalf("consolidateScope failed: %v", err)
	}

	records, err := store.ListByScope(context.Background(), scope, 0)
	if err != nil {
		t.Fatalf("ListByScope failed: %v", err)
	}
	// Pairwise merge: higher importance wins.
	if len(records) != 1 || records[0].ID != "high" {
		t.Fatalf("records = %+v, want only high (pairwise merge)", records)
	}
}

func TestGenerateThemeFromCluster(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 16, 12, 0, 0, 0, time.UTC)
	scope := ai.LLMMemoryScope{Platform: "telegram", ConversationID: "chat-1"}

	cluster := []ai.LLMMemoryRecord{
		newScopedRecord(scope, "src-1", 6, now.Add(-1*time.Hour), []float32{1, 0}),
		newScopedRecord(scope, "src-2", 7, now.Add(-2*time.Hour), []float32{1, 0}),
		newScopedRecord(scope, "src-3", 5, now.Add(-3*time.Hour), []float32{1, 0}),
	}
	store := newMemoryServiceStub(cluster)
	themeLLM := &llmProviderStub{
		stream: &llmStreamStub{
			chunks: []ai.LLMGenerateChunk{
				{
					Kind:  ai.LLMGenerateChunkKindOutputText,
					Delta: `{"content":"Shared theme across sources","importance":8}`,
				},
			},
		},
	}
	embeddingStub := &embeddingProviderStub{
		response: ai.EmbeddingResponse{Vectors: [][]float32{{0.5, 0.5}}},
	}

	cfg := validConsolidationConfig()
	module := New(withClock(func() time.Time { return now }), withConfig(cfg))
	module.llmMemory = store
	module.consolidationProvider = themeLLM
	module.embeddingProvider = embeddingStub

	if err := module.generateThemeFromCluster(context.Background(), scope, cluster, now); err != nil {
		t.Fatalf("generateThemeFromCluster failed: %v", err)
	}

	records, err := store.ListByScope(context.Background(), scope, 0)
	if err != nil {
		t.Fatalf("ListByScope failed: %v", err)
	}
	// Source records deleted, theme stored.
	if len(records) != 1 {
		t.Fatalf("record count = %d, want 1", len(records))
	}
	if records[0].Content != "Shared theme across sources" {
		t.Fatalf("theme content = %q, want Shared theme across sources", records[0].Content)
	}
	if records[0].Category != "theme" {
		t.Fatalf("category = %q, want theme", records[0].Category)
	}
	if len(records[0].Profile.EvidenceRecordIDs) != 3 {
		t.Fatalf("evidence IDs count = %d, want 3", len(records[0].Profile.EvidenceRecordIDs))
	}

	// Verify LLM prompt includes memory markup.
	if !strings.Contains(themeLLM.lastReq.Messages[1].Content, "<memories>") {
		t.Fatalf("theme prompt = %q, want <memories> markup", themeLLM.lastReq.Messages[1].Content)
	}
}

func TestParseThemeResponse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		input       string
		wantContent string
		wantImp     int
		wantErr     bool
	}{
		{
			name:        "plain json",
			input:       `{"content":"A shared theme","importance":7}`,
			wantContent: "A shared theme",
			wantImp:     7,
		},
		{
			name:        "json in prose",
			input:       `Here is the theme: {"content":"Found it","importance":8} done.`,
			wantContent: "Found it",
			wantImp:     8,
		},
		{
			name:        "importance clamped to default",
			input:       `{"content":"Valid theme","importance":0}`,
			wantContent: "Valid theme",
			wantImp:     7,
		},
		{
			name:    "empty content",
			input:   `{"content":"","importance":5}`,
			wantErr: true,
		},
		{
			name:    "invalid json",
			input:   `not json at all`,
			wantErr: true,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			candidate, err := parseThemeResponse(testCase.input)
			if testCase.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseThemeResponse failed: %v", err)
			}
			if candidate.Content != testCase.wantContent {
				t.Fatalf("content = %q, want %q", candidate.Content, testCase.wantContent)
			}
			if candidate.Importance != testCase.wantImp {
				t.Fatalf("importance = %d, want %d", candidate.Importance, testCase.wantImp)
			}
		})
	}
}

func TestRenderThemePromptIncludesMemories(t *testing.T) {
	t.Parallel()

	cluster := []ai.LLMMemoryRecord{
		{ID: "m-1", Content: "Alice likes tea", Category: "preference", Profile: ai.LLMMemoryProfile{Importance: 7}},
		{ID: "m-2", Content: "Alice studies CS", Category: "knowledge", Profile: ai.LLMMemoryProfile{Importance: 6}},
	}

	prompt := renderThemePrompt(cluster)
	if !strings.Contains(prompt, "<memories>") || !strings.Contains(prompt, "</memories>") {
		t.Fatalf("prompt = %q, want <memories> block", prompt)
	}
	if !strings.Contains(prompt, `id="m-1"`) || !strings.Contains(prompt, `id="m-2"`) {
		t.Fatalf("prompt = %q, want both memory IDs", prompt)
	}
	if !strings.Contains(prompt, "Alice likes tea") {
		t.Fatalf("prompt = %q, want memory content", prompt)
	}
	if !strings.Contains(prompt, "exactly one theme") {
		t.Fatalf("prompt = %q, want theme generation instructions", prompt)
	}
}

type memoryServiceStub struct {
	records    map[string]ai.LLMMemoryRecord
	nextID     int
	storeCalls []ai.LLMMemoryEntry
}

func newMemoryServiceStub(records []ai.LLMMemoryRecord) *memoryServiceStub {
	store := &memoryServiceStub{records: make(map[string]ai.LLMMemoryRecord, len(records))}
	for _, record := range records {
		store.records[record.ID] = record
	}

	return store
}

func (s *memoryServiceStub) Store(_ context.Context, entry ai.LLMMemoryEntry) (ai.LLMMemoryRecord, error) {
	s.storeCalls = append(s.storeCalls, entry)
	s.nextID++
	id := fmt.Sprintf("gen-%d", s.nextID)
	record := ai.LLMMemoryRecord{
		ID:        id,
		Scope:     entry.Scope,
		Content:   entry.Content,
		Category:  entry.Category,
		Embedding: append([]float32(nil), entry.Embedding...),
		Profile:   entry.Profile,
		Metadata:  entry.Metadata,
		Keywords:  entry.Keywords,
		Tags:      entry.Tags,
		Links:     entry.Links,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	s.records[id] = record

	return record, nil
}

func (s *memoryServiceStub) Search(context.Context, ai.LLMMemoryQuery) ([]ai.LLMMemoryMatch, error) {
	return nil, fmt.Errorf("not implemented")
}

func (s *memoryServiceStub) Update(_ context.Context, update ai.LLMMemoryUpdate) (ai.LLMMemoryRecord, error) {
	record, exists := s.records[update.ID]
	if !exists {
		return ai.LLMMemoryRecord{}, fmt.Errorf("record %s not found", update.ID)
	}
	record.Content = update.Content
	record.Category = update.Category
	record.Embedding = append([]float32(nil), update.Embedding...)
	record.Profile = update.Profile
	record.Metadata = update.Metadata
	record.Keywords = update.Keywords
	record.Tags = update.Tags
	record.Links = update.Links
	s.records[update.ID] = record

	return record, nil
}

func (s *memoryServiceStub) Delete(_ context.Context, id string) error {
	delete(s.records, id)
	return nil
}

func (s *memoryServiceStub) ListByScope(
	_ context.Context,
	scope ai.LLMMemoryScope,
	limit int,
) ([]ai.LLMMemoryRecord, error) {
	records := make([]ai.LLMMemoryRecord, 0, len(s.records))
	for _, record := range s.records {
		if record.Scope != scope {
			continue
		}
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].ID < records[j].ID
	})
	if limit > 0 && len(records) > limit {
		records = records[:limit]
	}

	return records, nil
}

func newScopedRecord(scope ai.LLMMemoryScope, id string, importance int, lastAccessed time.Time, embedding []float32) ai.LLMMemoryRecord {
	return ai.LLMMemoryRecord{
		ID:        id,
		Scope:     scope,
		Content:   id,
		Category:  "knowledge",
		Embedding: embedding,
		Profile: ai.LLMMemoryProfile{
			Importance:     importance,
			LastAccessedAt: lastAccessed,
		},
		Metadata: map[string]string{
			"importance":    fmt.Sprintf("%d", importance),
			"last_accessed": lastAccessed.Format(time.RFC3339),
		},
		CreatedAt: lastAccessed,
		UpdatedAt: lastAccessed,
	}
}
