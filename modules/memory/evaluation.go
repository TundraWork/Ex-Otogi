package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"ex-otogi/pkg/llm"
	"ex-otogi/pkg/otogi/ai"
	"ex-otogi/pkg/otogi/platform"
)

const evaluationProviderName = "evaluation"

// EvaluationCorpus is the provider-free memory benchmark input contract.
type EvaluationCorpus struct {
	Version   int                  `json:"version"`
	Scenarios []EvaluationScenario `json:"scenarios"`
}

// EvaluationScenario is one isolated formation and retrieval sequence.
type EvaluationScenario struct {
	ID                  string                `json:"id"`
	Language            string                `json:"language"`
	Windows             []EvaluationWindow    `json:"windows"`
	ExpectedMemories    []EvaluationMemory    `json:"expected_memories"`
	ProhibitedFragments []string              `json:"prohibited_fragments"`
	SupersededFragments []string              `json:"superseded_fragments"`
	Retrievals          []EvaluationRetrieval `json:"retrievals"`
}

// EvaluationWindow describes one formation invocation and deterministic extractor response.
type EvaluationWindow struct {
	Articles       []EvaluationArticle `json:"articles"`
	ExtractionJSON json.RawMessage     `json:"extraction_json"`
}

// EvaluationArticle is one conversation turn supplied to formation.
type EvaluationArticle struct {
	ID        string `json:"id"`
	ActorID   string `json:"actor_id"`
	ActorName string `json:"actor_name"`
	Text      string `json:"text"`
	OffsetSec int64  `json:"offset_seconds"`
}

// EvaluationMemory is one durable memory expected after formation.
type EvaluationMemory struct {
	Key      string   `json:"key"`
	Content  string   `json:"content"`
	Category string   `json:"category"`
	Aliases  []string `json:"aliases,omitempty"`
}

// EvaluationRetrieval describes one query and its graded relevance judgments.
type EvaluationRetrieval struct {
	Prompt       string          `json:"prompt"`
	PlannerJSON  json.RawMessage `json:"planner_json"`
	CurrentActor string          `json:"current_actor_id"`
	Relevance    map[string]int  `json:"relevance"`
}

// EvaluationOptions selects mechanisms and supplies the storage boundary.
type EvaluationOptions struct {
	RetrievalPlanningEnabled bool
	StoreFactory             func() ai.SemanticStore
	Now                      time.Time
}

// EvaluationMetrics contains quality, cost, latency, and storage measurements.
type EvaluationMetrics struct {
	FormationPrecision             float64 `json:"formation_precision"`
	FormationRecall                float64 `json:"formation_recall"`
	FormationF1                    float64 `json:"formation_f1"`
	SupersessionCorrectness        float64 `json:"supersession_correctness"`
	DuplicateRate                  float64 `json:"duplicate_rate"`
	ProhibitedMemoryRate           float64 `json:"prohibited_memory_rate"`
	RecallAt5                      float64 `json:"recall_at_5"`
	MRR                            float64 `json:"mrr"`
	NDCGAt5                        float64 `json:"ndcg_at_5"`
	FormationP50MS                 float64 `json:"formation_p50_ms"`
	FormationP95MS                 float64 `json:"formation_p95_ms"`
	RetrievalP50MS                 float64 `json:"retrieval_p50_ms"`
	RetrievalP95MS                 float64 `json:"retrieval_p95_ms"`
	ExtractionCallsPer100          float64 `json:"extraction_calls_per_100_articles"`
	PlanningCallsPerChat           float64 `json:"planning_calls_per_chat_request"`
	FormationEmbeddingCallsPer100  float64 `json:"formation_embedding_calls_per_100_articles"`
	RetrievalEmbeddingCallsPerChat float64 `json:"retrieval_embedding_calls_per_chat_request"`
	ConsolidationCallsPer100       float64 `json:"consolidation_calls_per_100_articles"`
	StoredRecordsPer1000           float64 `json:"stored_records_per_1000_articles"`
	StoredBytesPer1000             float64 `json:"stored_bytes_per_1000_articles"`
	ArticleCount                   int     `json:"article_count"`
	RetrievalCount                 int     `json:"retrieval_count"`
	StoredRecordCount              int     `json:"stored_record_count"`
	ExtractionCallCount            int     `json:"extraction_call_count"`
	PlanningCallCount              int     `json:"planning_call_count"`
	FormationEmbeddingCallCount    int     `json:"formation_embedding_call_count"`
	RetrievalEmbeddingCallCount    int     `json:"retrieval_embedding_call_count"`
	ConsolidationCallCount         int     `json:"consolidation_call_count"`
}

// EvaluationScenarioResult provides inspectable scenario-level evidence.
type EvaluationScenarioResult struct {
	ID             string              `json:"id"`
	Language       string              `json:"language"`
	StoredMemories []EvaluationMemory  `json:"stored_memories"`
	Retrievals     []EvaluationRanking `json:"retrievals"`
}

// EvaluationRanking contains stable memory keys returned for one query.
type EvaluationRanking struct {
	Prompt        string   `json:"prompt"`
	RetrievedKeys []string `json:"retrieved_keys"`
}

// EvaluationReport is the complete machine-readable benchmark output.
type EvaluationReport struct {
	SchemaVersion            int                        `json:"schema_version"`
	CorpusVersion            int                        `json:"corpus_version"`
	RetrievalPlanningEnabled bool                       `json:"retrieval_planning_enabled"`
	Metrics                  EvaluationMetrics          `json:"metrics"`
	Scenarios                []EvaluationScenarioResult `json:"scenarios"`
	Comparison               *EvaluationComparison      `json:"comparison,omitempty"`
}

// LoadEvaluationCorpus decodes and validates one benchmark corpus.
func LoadEvaluationCorpus(reader io.Reader) (EvaluationCorpus, error) {
	var corpus EvaluationCorpus
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&corpus); err != nil {
		return EvaluationCorpus{}, fmt.Errorf("decode memory evaluation corpus: %w", err)
	}
	if corpus.Version <= 0 || len(corpus.Scenarios) == 0 {
		return EvaluationCorpus{}, fmt.Errorf("validate memory evaluation corpus: version and scenarios are required")
	}
	for index, scenario := range corpus.Scenarios {
		if strings.TrimSpace(scenario.ID) == "" || len(scenario.Windows) == 0 {
			return EvaluationCorpus{}, fmt.Errorf("validate memory evaluation corpus scenario[%d]: id and windows are required", index)
		}
	}
	return corpus, nil
}

// RunEvaluation executes production formation and retrieval algorithms using deterministic providers.
func RunEvaluation(ctx context.Context, corpus EvaluationCorpus, options EvaluationOptions) (EvaluationReport, error) {
	if options.StoreFactory == nil {
		return EvaluationReport{}, fmt.Errorf("run memory evaluation: store factory is required")
	}
	now := options.Now.UTC()
	if now.IsZero() {
		now = time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC)
	}
	acc := &evaluationAccumulator{}
	report := EvaluationReport{SchemaVersion: 1, CorpusVersion: corpus.Version, RetrievalPlanningEnabled: options.RetrievalPlanningEnabled}
	for _, scenario := range corpus.Scenarios {
		result, err := evaluateScenario(ctx, scenario, options, now, acc)
		if err != nil {
			return EvaluationReport{}, fmt.Errorf("run memory evaluation scenario %s: %w", scenario.ID, err)
		}
		report.Scenarios = append(report.Scenarios, result)
	}
	report.Metrics = acc.metrics()
	return report, nil
}

func evaluateScenario(ctx context.Context, scenario EvaluationScenario, options EvaluationOptions, now time.Time, acc *evaluationAccumulator) (EvaluationScenarioResult, error) {
	store := options.StoreFactory()
	if store == nil {
		return EvaluationScenarioResult{}, fmt.Errorf("store factory returned nil")
	}
	provider := &evaluationLLMProvider{}
	embedder := &evaluationEmbeddingProvider{}
	providerRegistry, err := llm.NewRegistry(map[string]ai.LLMProvider{evaluationProviderName: provider})
	if err != nil {
		return EvaluationScenarioResult{}, fmt.Errorf("create evaluation LLM registry: %w", err)
	}
	embeddingRegistry, err := llm.NewEmbeddingRegistry(map[string]ai.EmbeddingProvider{evaluationProviderName: embedder})
	if err != nil {
		return EvaluationScenarioResult{}, fmt.Errorf("create evaluation embedding registry: %w", err)
	}
	module := New(withClock(func() time.Time { return now }))
	module.cfg = defaultConfig()
	module.cfg.Enabled = true
	module.cfg.ExtractionProvider, module.cfg.ExtractionModel = evaluationProviderName, "deterministic"
	module.cfg.EmbeddingProvider = evaluationProviderName
	module.cfg.RetrievalPlanningEnabled = options.RetrievalPlanningEnabled
	module.semanticStore, module.extractionProvider, module.embeddingProvider = store, provider, embedder
	module.providerRegistry, module.embeddingRegistry = providerRegistry, embeddingRegistry

	scope := ai.SemanticScope{Platform: "evaluation", ConversationID: scenario.ID}
	formationDurations := make([]time.Duration, 0, len(scenario.Windows))
	for _, window := range scenario.Windows {
		extractionResponse, resolveErr := resolveEvaluationMemoryIDs(ctx, store, scope, scenario.ExpectedMemories, window.ExtractionJSON)
		if resolveErr != nil {
			return EvaluationScenarioResult{}, fmt.Errorf("resolve extraction memory ids: %w", resolveErr)
		}
		provider.enqueueExtraction(extractionResponse)
		articles := make([]bufferedArticle, 0, len(window.Articles))
		for _, article := range window.Articles {
			occurredAt := now.Add(time.Duration(article.OffsetSec) * time.Second)
			articles = append(articles, bufferedArticle{Article: platform.Article{ID: article.ID, Text: article.Text}, Actor: platform.Actor{ID: article.ActorID, DisplayName: article.ActorName}, OccurredAt: occurredAt, ReceivedAt: occurredAt})
			acc.articleCount++
		}
		startedAt := time.Now()
		if err := module.processWindow(ctx, scope, articles, FlushReasonQuiet); err != nil {
			return EvaluationScenarioResult{}, fmt.Errorf("process formation window: %w", err)
		}
		formationDurations = append(formationDurations, time.Since(startedAt))
	}
	records, err := store.ListByScope(ctx, scope, 0)
	if err != nil {
		return EvaluationScenarioResult{}, fmt.Errorf("list formed memories: %w", err)
	}
	keyByID, score := scoreFormation(scenario, records)
	acc.addFormation(score, records, formationDurations)
	result := EvaluationScenarioResult{ID: scenario.ID, Language: scenario.Language}
	for _, record := range records {
		result.StoredMemories = append(result.StoredMemories, EvaluationMemory{Key: keyByID[record.ID], Content: record.Content, Category: record.Category})
	}
	for _, retrieval := range scenario.Retrievals {
		if options.RetrievalPlanningEnabled {
			provider.enqueuePlan(string(retrieval.PlannerJSON))
		}
		startedAt := time.Now()
		retrieved, retrieveErr := module.Retrieve(ctx, ai.SemanticRetrievalRequest{Scope: scope, Prompt: retrieval.Prompt, Policy: ai.SemanticRetrievalPolicy{MaxRetrievedMemories: 5, MinSimilarity: 0.05, MaxMemoryRunes: 4000}, CurrentActor: ai.SemanticActorRef{ID: retrieval.CurrentActor}})
		if retrieveErr != nil {
			return EvaluationScenarioResult{}, fmt.Errorf("retrieve %q: %w", retrieval.Prompt, retrieveErr)
		}
		keys := retrievedKeys(retrieved.Content, keyByID)
		acc.addRetrieval(keys, retrieval.Relevance, time.Since(startedAt))
		result.Retrievals = append(result.Retrievals, EvaluationRanking{Prompt: retrieval.Prompt, RetrievedKeys: keys})
	}
	acc.extractionCalls += provider.extractionCallCount()
	acc.planningCalls += provider.planningCallCount()
	formationEmbeddingCalls, retrievalEmbeddingCalls := embedder.counts()
	acc.formationEmbeddingCalls += formationEmbeddingCalls
	acc.retrievalEmbeddingCalls += retrievalEmbeddingCalls
	return result, nil
}

func resolveEvaluationMemoryIDs(ctx context.Context, store ai.SemanticStore, scope ai.SemanticScope, expected []EvaluationMemory, response json.RawMessage) (string, error) {
	resolved := string(response)
	if !strings.Contains(resolved, "{{memory:") {
		return resolved, nil
	}
	records, err := store.ListByScope(ctx, scope, 0)
	if err != nil {
		return "", fmt.Errorf("list existing memories: %w", err)
	}
	for _, memory := range expected {
		placeholder := "{{memory:" + memory.Key + "}}"
		if !strings.Contains(resolved, placeholder) {
			continue
		}
		contents := append([]string{memory.Content}, memory.Aliases...)
		recordID := ""
		for _, record := range records {
			for _, content := range contents {
				if evaluationTextMatches(record.Content, content) {
					recordID = record.ID
					break
				}
			}
			if recordID != "" {
				break
			}
		}
		if recordID == "" {
			return "", fmt.Errorf("memory key %q has no matching stored record", memory.Key)
		}
		resolved = strings.ReplaceAll(resolved, placeholder, recordID)
	}
	if strings.Contains(resolved, "{{memory:") {
		return "", fmt.Errorf("unresolved memory placeholder in extraction response")
	}
	return resolved, nil
}

type formationScore struct {
	tp, fp, fn, duplicates, prohibited, supersessionChecks, supersessionPasses int
}

func scoreFormation(scenario EvaluationScenario, records []ai.SemanticRecord) (map[string]string, formationScore) {
	keys := make(map[string]string, len(records))
	matchedExpected := make(map[string]struct{}, len(scenario.ExpectedMemories))
	score := formationScore{}
	seen := make(map[string]struct{}, len(records))
	for _, record := range records {
		normalized := normalizeEvaluationText(record.Content)
		if _, exists := seen[normalized]; exists {
			score.duplicates++
		}
		seen[normalized] = struct{}{}
		matched := false
		for _, expected := range scenario.ExpectedMemories {
			if evaluationTextMatches(record.Content, expected.Content) && strings.EqualFold(strings.TrimSpace(record.Category), strings.TrimSpace(expected.Category)) {
				keys[record.ID] = expected.Key
				matchedExpected[expected.Key] = struct{}{}
				matched = true
				break
			}
		}
		if matched {
			score.tp++
		} else {
			score.fp++
			keys[record.ID] = "unexpected:" + record.ID
		}
		for _, prohibited := range scenario.ProhibitedFragments {
			if containsEvaluationText(record.Content, prohibited) {
				score.prohibited++
				break
			}
		}
	}
	score.fn = len(scenario.ExpectedMemories) - len(matchedExpected)
	for _, fragment := range scenario.SupersededFragments {
		score.supersessionChecks++
		found := false
		for _, record := range records {
			found = found || containsEvaluationText(record.Content, fragment)
		}
		if !found {
			score.supersessionPasses++
		}
	}
	return keys, score
}

type evaluationAccumulator struct {
	tp, fp, fn, duplicates, prohibited, storedBytes  int
	supersessionChecks, supersessionPasses           int
	articleCount, retrievalCount, storedRecordCount  int
	extractionCalls, planningCalls                   int
	formationEmbeddingCalls, retrievalEmbeddingCalls int
	recallSum, mrrSum, ndcgSum                       float64
	formationDurations, retrievalDurations           []time.Duration
}

func (a *evaluationAccumulator) addFormation(score formationScore, records []ai.SemanticRecord, durations []time.Duration) {
	a.tp, a.fp, a.fn = a.tp+score.tp, a.fp+score.fp, a.fn+score.fn
	a.duplicates, a.prohibited = a.duplicates+score.duplicates, a.prohibited+score.prohibited
	a.supersessionChecks += score.supersessionChecks
	a.supersessionPasses += score.supersessionPasses
	a.storedRecordCount += len(records)
	for _, record := range records {
		stableRecord := record
		stableRecord.ID = "00000000-0000-0000-0000-000000000000"
		stableRecord.CreatedAt = time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
		stableRecord.UpdatedAt = stableRecord.CreatedAt
		encoded, err := json.Marshal(stableRecord)
		if err == nil {
			a.storedBytes += len(encoded)
		}
	}
	a.formationDurations = append(a.formationDurations, durations...)
}

func (a *evaluationAccumulator) addRetrieval(keys []string, relevance map[string]int, duration time.Duration) {
	a.retrievalCount++
	a.retrievalDurations = append(a.retrievalDurations, duration)
	relevantTotal, found, firstRelevant := 0, 0, 0
	for _, grade := range relevance {
		if grade > 0 {
			relevantTotal++
		}
	}
	dcg := 0.0
	for index, key := range keys {
		if index >= 5 {
			break
		}
		grade := relevance[key]
		if grade > 0 {
			found++
			if firstRelevant == 0 {
				firstRelevant = index + 1
			}
		}
		dcg += (math.Pow(2, float64(grade)) - 1) / math.Log2(float64(index+2))
	}
	if relevantTotal > 0 {
		a.recallSum += float64(found) / float64(relevantTotal)
	}
	if firstRelevant > 0 {
		a.mrrSum += 1 / float64(firstRelevant)
	}
	grades := make([]int, 0, len(relevance))
	for _, grade := range relevance {
		grades = append(grades, grade)
	}
	slices.SortFunc(grades, func(left, right int) int { return right - left })
	idcg := 0.0
	for index, grade := range grades {
		if index >= 5 {
			break
		}
		idcg += (math.Pow(2, float64(grade)) - 1) / math.Log2(float64(index+2))
	}
	if idcg > 0 {
		a.ndcgSum += dcg / idcg
	}
}

func (a *evaluationAccumulator) metrics() EvaluationMetrics {
	precision, recall := ratio(a.tp, a.tp+a.fp), ratio(a.tp, a.tp+a.fn)
	f1 := 0.0
	if precision+recall > 0 {
		f1 = 2 * precision * recall / (precision + recall)
	}
	articleScale, retrievalScale := float64(max(a.articleCount, 1)), float64(max(a.retrievalCount, 1))
	return EvaluationMetrics{
		FormationPrecision: precision, FormationRecall: recall, FormationF1: f1,
		SupersessionCorrectness: ratio(a.supersessionPasses, a.supersessionChecks),
		DuplicateRate:           ratio(a.duplicates, a.storedRecordCount), ProhibitedMemoryRate: ratio(a.prohibited, a.storedRecordCount),
		RecallAt5: a.recallSum / retrievalScale, MRR: a.mrrSum / retrievalScale, NDCGAt5: a.ndcgSum / retrievalScale,
		FormationP50MS: percentileMS(a.formationDurations, 0.50), FormationP95MS: percentileMS(a.formationDurations, 0.95),
		RetrievalP50MS: percentileMS(a.retrievalDurations, 0.50), RetrievalP95MS: percentileMS(a.retrievalDurations, 0.95),
		ExtractionCallsPer100:          float64(a.extractionCalls) * 100 / articleScale,
		PlanningCallsPerChat:           float64(a.planningCalls) / retrievalScale,
		FormationEmbeddingCallsPer100:  float64(a.formationEmbeddingCalls) * 100 / articleScale,
		RetrievalEmbeddingCallsPerChat: float64(a.retrievalEmbeddingCalls) / retrievalScale,
		ConsolidationCallsPer100:       0,
		StoredRecordsPer1000:           float64(a.storedRecordCount) * 1000 / articleScale,
		StoredBytesPer1000:             float64(a.storedBytes) * 1000 / articleScale,
		ArticleCount:                   a.articleCount, RetrievalCount: a.retrievalCount, StoredRecordCount: a.storedRecordCount,
		ExtractionCallCount: a.extractionCalls, PlanningCallCount: a.planningCalls,
		FormationEmbeddingCallCount: a.formationEmbeddingCalls, RetrievalEmbeddingCallCount: a.retrievalEmbeddingCalls,
		ConsolidationCallCount: 0,
	}
}

func ratio(numerator, denominator int) float64 {
	if denominator == 0 {
		return 1
	}
	return float64(numerator) / float64(denominator)
}

func percentileMS(values []time.Duration, percentile float64) float64 {
	if len(values) == 0 {
		return 0
	}
	cloned := append([]time.Duration(nil), values...)
	slices.Sort(cloned)
	index := max(int(math.Ceil(float64(len(cloned))*percentile))-1, 0)
	return float64(cloned[index].Nanoseconds()) / float64(time.Millisecond)
}

func retrievedKeys(content string, keyByRecordID map[string]string) []string {
	type rankedKey struct {
		key    string
		offset int
	}
	ranked := make([]rankedKey, 0, len(keyByRecordID))
	for recordID, key := range keyByRecordID {
		marker := `id="` + recordID + `"`
		if offset := strings.Index(content, marker); offset >= 0 {
			ranked = append(ranked, rankedKey{key: key, offset: offset})
		}
	}
	sort.Slice(ranked, func(left, right int) bool { return ranked[left].offset < ranked[right].offset })
	keys := make([]string, len(ranked))
	for index := range ranked {
		keys[index] = ranked[index].key
	}
	return keys
}

func evaluationTextMatches(actual, expected string) bool {
	return normalizeEvaluationText(actual) == normalizeEvaluationText(expected)
}

func containsEvaluationText(actual, fragment string) bool {
	return strings.Contains(normalizeEvaluationText(actual), normalizeEvaluationText(fragment))
}

func normalizeEvaluationText(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, value)
}

type evaluationLLMProvider struct {
	mu                         sync.Mutex
	extractions, plans         []string
	extractionCalls, planCalls int
}

func (p *evaluationLLMProvider) enqueueExtraction(response string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.extractions = append(p.extractions, response)
}

func (p *evaluationLLMProvider) enqueuePlan(response string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.plans = append(p.plans, response)
}

func (p *evaluationLLMProvider) GenerateStream(_ context.Context, request ai.LLMGenerateRequest) (ai.LLMStream, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	isPlan := len(request.Messages) > 0 && strings.Contains(request.Messages[0].Content, "retrieval planner")
	if isPlan {
		if len(p.plans) == 0 {
			return nil, fmt.Errorf("evaluation planner response queue exhausted")
		}
		response := p.plans[0]
		p.plans = p.plans[1:]
		p.planCalls++
		return &evaluationStream{response: response}, nil
	}
	if len(p.extractions) == 0 {
		return nil, fmt.Errorf("evaluation extraction response queue exhausted")
	}
	response := p.extractions[0]
	p.extractions = p.extractions[1:]
	p.extractionCalls++
	return &evaluationStream{response: response}, nil
}

func (p *evaluationLLMProvider) extractionCallCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.extractionCalls
}

func (p *evaluationLLMProvider) planningCallCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.planCalls
}

type evaluationStream struct {
	response string
	done     bool
}

func (s *evaluationStream) Recv(context.Context) (ai.LLMGenerateChunk, error) {
	if s.done {
		return ai.LLMGenerateChunk{}, io.EOF
	}
	s.done = true
	return ai.LLMGenerateChunk{Kind: ai.LLMGenerateChunkKindOutputText, Delta: s.response}, nil
}

func (*evaluationStream) Close() error { return nil }

type evaluationEmbeddingProvider struct {
	mu                             sync.Mutex
	formationCalls, retrievalCalls int
}

func (p *evaluationEmbeddingProvider) Embed(_ context.Context, request ai.EmbeddingRequest) (ai.EmbeddingResponse, error) {
	p.mu.Lock()
	if request.TaskType == ai.EmbeddingTaskTypeQuery {
		p.retrievalCalls++
	} else {
		p.formationCalls++
	}
	p.mu.Unlock()
	vectors := make([][]float32, len(request.Texts))
	for index, text := range request.Texts {
		vectors[index] = evaluationEmbedding(text)
	}
	return ai.EmbeddingResponse{Vectors: vectors}, nil
}

func (p *evaluationEmbeddingProvider) counts() (int, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.formationCalls, p.retrievalCalls
}

func evaluationEmbedding(text string) []float32 {
	const dimensions = 256
	vector := make([]float32, dimensions)
	for _, token := range evaluationTokens(text) {
		var hash uint32 = 2166136261
		for _, r := range token {
			hash ^= uint32(r)
			hash *= 16777619
		}
		vector[int(hash%dimensions)]++
	}
	var magnitude float64
	for _, value := range vector {
		magnitude += float64(value * value)
	}
	if magnitude == 0 {
		vector[0] = 1
		return vector
	}
	magnitude = math.Sqrt(magnitude)
	for index := range vector {
		vector[index] = float32(float64(vector[index]) / magnitude)
	}
	return vector
}

func evaluationTokens(text string) []string {
	lower := strings.ToLower(text)
	words := strings.FieldsFunc(lower, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
	tokens := append([]string(nil), words...)
	runes := []rune(strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return r
		}
		return -1
	}, lower))
	for index := 0; index+1 < len(runes); index++ {
		tokens = append(tokens, string(runes[index:index+2]))
	}
	return tokens
}
