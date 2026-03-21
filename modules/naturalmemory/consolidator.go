package naturalmemory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"ex-otogi/pkg/otogi/ai"
)

const (
	consolidationMergeSimilarityThreshold = 0.9
	consolidationPruneScoreThreshold      = 1.0
)

func (m *Module) startConsolidation(ctx context.Context) {
	stopCh := make(chan struct{})
	doneCh := make(chan struct{})
	m.stopCh = stopCh
	m.flushDone = doneCh

	go func() {
		defer close(doneCh)
		defer func() {
			if recovered := recover(); recovered != nil && m.logger != nil {
				m.logger.ErrorContext(ctx, "naturalmemory consolidation panic", "recover", recovered)
			}
		}()

		m.debugConsolidationStart(ctx)

		ticker := time.NewTicker(m.cfg.ConsolidationInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-stopCh:
				return
			case <-ticker.C:
				if err := m.runConsolidationCycle(ctx); err != nil && m.logger != nil {
					m.logger.ErrorContext(ctx, "naturalmemory consolidation", "error", err)
				}
			}
		}
	}()
}

func (m *Module) stopConsolidation(ctx context.Context) error {
	if m == nil || m.stopCh == nil {
		return nil
	}

	stopCh := m.stopCh
	doneCh := m.flushDone
	m.stopCh = nil
	m.flushDone = nil
	close(stopCh)

	if doneCh == nil {
		return nil
	}

	select {
	case <-doneCh:
		m.debugConsolidationStop(ctx)
		return nil
	case <-ctx.Done():
		return fmt.Errorf("naturalmemory stop consolidation: %w", ctx.Err())
	}
}

func (m *Module) runConsolidationCycle(ctx context.Context) error {
	scopes := m.drainActiveScopes()
	for _, scope := range scopes {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("consolidation cycle context: %w", err)
		}
		if err := m.consolidateScope(ctx, scope); err != nil && m.logger != nil {
			m.logger.ErrorContext(ctx, "naturalmemory consolidate scope",
				"scope_platform", scope.Platform,
				"scope_conversation_id", scope.ConversationID,
				"error", err,
			)
		}
	}

	return nil
}

func (m *Module) consolidateScope(ctx context.Context, scope ai.LLMMemoryScope) error {
	if m == nil || m.llmMemory == nil {
		return fmt.Errorf("llm memory service unavailable")
	}

	records, err := m.llmMemory.ListByScope(ctx, scope, 0)
	if err != nil {
		return fmt.Errorf("list scope memories: %w", err)
	}
	if len(records) == 0 {
		return nil
	}

	now := m.now()
	kept := make([]ai.LLMMemoryRecord, 0, len(records))
	for _, record := range records {
		if record.Profile.ValidUntil != nil && record.Profile.ValidUntil.Before(now) {
			if err := m.llmMemory.Delete(ctx, record.ID); err != nil {
				return fmt.Errorf("delete expired memory %s: %w", record.ID, err)
			}
			continue
		}
		importance := memoryImportance(record)
		if importance < m.cfg.MinImportance || effectiveScore(record, m.cfg.DecayFactor, now) < consolidationPruneScoreThreshold {
			if err := m.llmMemory.Delete(ctx, record.ID); err != nil {
				return fmt.Errorf("delete pruned memory %s: %w", record.ID, err)
			}
			continue
		}
		kept = append(kept, record)
	}
	if len(kept) == 0 {
		return nil
	}

	clusters := affinityCluster(
		kept,
		m.cfg.ClusterSimilarityThreshold,
		m.cfg.ClusterTemporalWeight,
		m.cfg.DecayFactor,
	)
	for _, cluster := range clusters {
		if len(cluster) >= m.cfg.ClusterMinSize {
			if err := m.generateThemeFromCluster(ctx, scope, cluster, now); err != nil && m.logger != nil {
				m.logger.WarnContext(ctx, "naturalmemory theme generation",
					"scope_platform", scope.Platform,
					"scope_conversation_id", scope.ConversationID,
					"cluster_size", len(cluster),
					"error", err,
				)
			}
		} else if len(cluster) > 1 {
			if err := m.mergeNearDuplicates(ctx, cluster, now); err != nil {
				return err
			}
		}
	}

	refreshed, err := m.llmMemory.ListByScope(ctx, scope, 0)
	if err != nil {
		return fmt.Errorf("refresh scope memories: %w", err)
	}
	if err := m.maybeGenerateReflections(ctx, scope, refreshed, now); err != nil && m.logger != nil {
		m.logger.WarnContext(ctx, "naturalmemory reflection generation",
			"scope_platform", scope.Platform,
			"scope_conversation_id", scope.ConversationID,
			"error", err,
		)
	}

	refreshed, err = m.llmMemory.ListByScope(ctx, scope, 0)
	if err != nil {
		return fmt.Errorf("refresh scope memories after reflection: %w", err)
	}
	if len(refreshed) <= m.cfg.MaxMemoriesPerScope {
		return nil
	}

	sortMemoriesByScore(refreshed, m.cfg.DecayFactor, now)

	for _, record := range refreshed[m.cfg.MaxMemoriesPerScope:] {
		if err := m.llmMemory.Delete(ctx, record.ID); err != nil {
			return fmt.Errorf("delete capped memory %s: %w", record.ID, err)
		}
	}

	return nil
}

func (m *Module) mergeNearDuplicates(ctx context.Context, records []ai.LLMMemoryRecord, now time.Time) error {
	mergeThreshold := float32(consolidationMergeSimilarityThreshold)
	if m.cfg.DuplicateSimilarityThreshold > mergeThreshold {
		mergeThreshold = m.cfg.DuplicateSimilarityThreshold
	}

	sort.Slice(records, func(i, j int) bool {
		leftScore := effectiveScore(records[i], m.cfg.DecayFactor, now)
		rightScore := effectiveScore(records[j], m.cfg.DecayFactor, now)
		if leftScore == rightScore {
			return records[i].UpdatedAt.After(records[j].UpdatedAt)
		}
		return leftScore > rightScore
	})

	deleted := make(map[string]struct{})
	for i := 0; i < len(records); i++ {
		if _, removed := deleted[records[i].ID]; removed {
			continue
		}
		for j := i + 1; j < len(records); j++ {
			if _, removed := deleted[records[j].ID]; removed {
				continue
			}
			if similarity := dotProduct(records[i].Embedding, records[j].Embedding); similarity < mergeThreshold {
				continue
			}

			keep, drop := preferredRecord(records[i], records[j], m.cfg.DecayFactor, now)
			if err := m.llmMemory.Delete(ctx, drop.ID); err != nil {
				return fmt.Errorf("delete duplicate memory %s: %w", drop.ID, err)
			}
			deleted[drop.ID] = struct{}{}
			if len(keep.Links) > len(records[i].Links) {
				if _, err := m.llmMemory.Update(ctx, ai.LLMMemoryUpdate{
					ID:        keep.ID,
					Content:   keep.Content,
					Category:  keep.Category,
					Embedding: append([]float32(nil), keep.Embedding...),
					Profile:   keep.Profile,
					Metadata:  keep.Metadata,
					Keywords:  keep.Keywords,
					Tags:      keep.Tags,
					Links:     keep.Links,
				}); err != nil {
					return fmt.Errorf("update merged links on memory %s: %w", keep.ID, err)
				}
			}
			records[i] = keep
		}
	}

	return nil
}

func preferredRecord(
	left ai.LLMMemoryRecord,
	right ai.LLMMemoryRecord,
	decayFactor float64,
	now time.Time,
) (keep ai.LLMMemoryRecord, drop ai.LLMMemoryRecord) {
	leftImportance := memoryImportance(left)
	rightImportance := memoryImportance(right)
	switch {
	case leftImportance > rightImportance:
		keep, drop = left, right
	case rightImportance > leftImportance:
		keep, drop = right, left
	default:
		leftScore := effectiveScore(left, decayFactor, now)
		rightScore := effectiveScore(right, decayFactor, now)
		switch {
		case leftScore > rightScore:
			keep, drop = left, right
		case rightScore > leftScore:
			keep, drop = right, left
		case left.UpdatedAt.After(right.UpdatedAt):
			keep, drop = left, right
		default:
			keep, drop = right, left
		}
	}

	keep.Links = mergeLinks(keep.Links, drop.Links)

	return keep, drop
}

func dotProduct(left []float32, right []float32) float32 {
	if len(left) == 0 || len(left) != len(right) {
		return 0
	}

	var total float32
	for index := range left {
		total += left[index] * right[index]
	}

	return total
}

// affinityCluster groups records using union-find based on pairwise affinity.
// Affinity combines embedding similarity and temporal proximity:
//
//	affinity = (1 - temporalWeight) * dotProduct(vi, vj)
//	         + temporalWeight * pow(decayFactor, |hoursBetween(ti, tj)|)
//
// Pairs with affinity >= simThreshold are merged into the same cluster.
func affinityCluster(
	records []ai.LLMMemoryRecord,
	simThreshold float32,
	temporalWeight float64,
	decayFactor float64,
) [][]ai.LLMMemoryRecord {
	n := len(records)
	if n <= 1 {
		if n == 1 {
			return [][]ai.LLMMemoryRecord{{records[0]}}
		}
		return nil
	}

	parent := make([]int, n)
	rank := make([]int, n)
	for i := range parent {
		parent[i] = i
	}

	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			aff := pairAffinity(records[i], records[j], temporalWeight, decayFactor)
			if aff >= float64(simThreshold) {
				ufUnion(parent, rank, i, j)
			}
		}
	}

	groups := make(map[int][]ai.LLMMemoryRecord, n)
	for i := range records {
		root := ufFind(parent, i)
		groups[root] = append(groups[root], records[i])
	}

	clusters := make([][]ai.LLMMemoryRecord, 0, len(groups))
	for _, group := range groups {
		clusters = append(clusters, group)
	}
	// Sort for deterministic output: largest clusters first, tie-break by
	// first record ID.
	sort.Slice(clusters, func(i, j int) bool {
		if len(clusters[i]) != len(clusters[j]) {
			return len(clusters[i]) > len(clusters[j])
		}
		return clusters[i][0].ID < clusters[j][0].ID
	})

	return clusters
}

func pairAffinity(
	left ai.LLMMemoryRecord,
	right ai.LLMMemoryRecord,
	temporalWeight float64,
	decayFactor float64,
) float64 {
	embSim := float64(dotProduct(left.Embedding, right.Embedding))

	leftTime := memoryLastAccessed(left)
	rightTime := memoryLastAccessed(right)
	hoursBetween := math.Abs(leftTime.Sub(rightTime).Hours())
	temporalSim := math.Pow(decayFactor, hoursBetween)

	return (1-temporalWeight)*embSim + temporalWeight*temporalSim
}

func ufFind(parent []int, i int) int {
	for parent[i] != i {
		parent[i] = parent[parent[i]]
		i = parent[i]
	}
	return i
}

func ufUnion(parent []int, rank []int, i int, j int) {
	ri := ufFind(parent, i)
	rj := ufFind(parent, j)
	if ri == rj {
		return
	}
	if rank[ri] < rank[rj] {
		parent[ri] = rj
	} else if rank[ri] > rank[rj] {
		parent[rj] = ri
	} else {
		parent[rj] = ri
		rank[ri]++
	}
}

const themeSystemPrompt = `You are a memory consolidation system. Synthesize a concise theme memory from a cluster of related memories in one conversation.`

type themeCandidate struct {
	Content          string `json:"content"`
	Importance       int    `json:"importance"`
	SubjectActorID   string `json:"subject_actor_id"`
	SubjectActorName string `json:"subject_actor_name"`
}

func (m *Module) generateThemeFromCluster(
	ctx context.Context,
	scope ai.LLMMemoryScope,
	cluster []ai.LLMMemoryRecord,
	now time.Time,
) error {
	if m == nil || m.consolidationProvider == nil || m.embeddingProvider == nil {
		return fmt.Errorf("consolidation or embedding provider unavailable")
	}

	candidate, err := m.callThemeLLM(ctx, cluster)
	if err != nil {
		return err
	}

	sourceIDs := make([]string, 0, len(cluster))
	for _, record := range cluster {
		sourceIDs = append(sourceIDs, record.ID)
	}

	// Compute embedding for the theme.
	embedding, err := embedSingleText(ctx, m.embeddingProvider, candidate.Content, ai.EmbeddingTaskTypeDocument)
	if err != nil {
		return fmt.Errorf("embed theme: %w", err)
	}

	profile := ai.LLMMemoryProfile{
		Kind:              ai.LLMMemoryKindSynthesized,
		Importance:        candidate.Importance,
		LastAccessedAt:    now.UTC(),
		AccessCount:       0,
		Source:            "natural.theme",
		EvidenceRecordIDs: uniqueRecordIDs(sourceIDs),
	}
	if strings.TrimSpace(candidate.SubjectActorID) != "" || strings.TrimSpace(candidate.SubjectActorName) != "" {
		profile.SubjectActor = resolveSubjectActor(extractedMemory{
			SubjectActorID:   candidate.SubjectActorID,
			SubjectActorName: candidate.SubjectActorName,
		}, extractionContext{
			AnchorTime:   now,
			Participants: participantsFromRecords(cluster),
		})
	}

	metadata := buildProfileMetadata(profile)
	if _, err := m.llmMemory.Store(ctx, ai.LLMMemoryEntry{
		Scope:     scope,
		Content:   candidate.Content,
		Category:  "theme",
		Embedding: embedding,
		Profile:   profile,
		Metadata:  metadata,
	}); err != nil {
		return fmt.Errorf("store theme: %w", err)
	}

	// Delete source records.
	for _, record := range cluster {
		if err := m.llmMemory.Delete(ctx, record.ID); err != nil {
			return fmt.Errorf("delete clustered source %s: %w", record.ID, err)
		}
	}

	return nil
}

func (m *Module) callThemeLLM(
	ctx context.Context,
	cluster []ai.LLMMemoryRecord,
) (themeCandidate, error) {
	requestCtx := ctx
	cancel := func() {}
	if m.cfg.ConsolidationTimeout > 0 {
		requestCtx, cancel = context.WithTimeout(ctx, m.cfg.ConsolidationTimeout)
	}
	defer cancel()

	stream, err := m.consolidationProvider.GenerateStream(requestCtx, ai.LLMGenerateRequest{
		Model: m.cfg.ConsolidationModel,
		Messages: []ai.LLMMessage{
			{Role: ai.LLMMessageRoleSystem, Content: themeSystemPrompt},
			{Role: ai.LLMMessageRoleUser, Content: renderThemePrompt(cluster)},
		},
		Temperature: 0.2,
	})
	if err != nil {
		return themeCandidate{}, fmt.Errorf("theme generate: %w", err)
	}

	responseText, err := collectStreamText(requestCtx, stream)
	closeErr := stream.Close()
	if err != nil {
		if closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close theme stream: %w", closeErr))
		}
		return themeCandidate{}, err
	}
	if closeErr != nil {
		return themeCandidate{}, fmt.Errorf("close theme stream: %w", closeErr)
	}

	candidate, err := parseThemeResponse(responseText)
	if err != nil {
		return themeCandidate{}, err
	}

	return candidate, nil
}

func renderThemePrompt(cluster []ai.LLMMemoryRecord) string {
	var builder strings.Builder

	builder.WriteString("Synthesize a concise theme from the following cluster of related memories.\n\n")
	builder.WriteString("Rules:\n")
	builder.WriteString("- Produce exactly one theme that captures the shared thread across all source memories\n")
	builder.WriteString("- The theme must be self-contained and specify the subject explicitly\n")
	builder.WriteString("- Do not duplicate individual memory details; capture the overarching pattern\n")
	builder.WriteString("- Keep the theme concise (1-2 sentences)\n\n")
	builder.WriteString("<memories>\n")
	for _, record := range cluster {
		builder.WriteString(fmt.Sprintf(
			"<memory id=%q importance=%d kind=%q category=%q>\n%s\n</memory>\n",
			record.ID,
			memoryImportance(record),
			record.Profile.Kind,
			record.Category,
			record.Content,
		))
	}
	builder.WriteString("</memories>\n\n")
	builder.WriteString(`Respond with a JSON object only. Format: {"content":"...","importance":8,"subject_actor_id":"...","subject_actor_name":"..."}`)

	return builder.String()
}

func parseThemeResponse(text string) (themeCandidate, error) {
	trimmed := strings.TrimSpace(stripMarkdownCodeFence(text))
	if trimmed == "" {
		return themeCandidate{}, fmt.Errorf("empty theme response")
	}

	var candidate themeCandidate
	if err := json.Unmarshal([]byte(trimmed), &candidate); err != nil {
		// Try extracting JSON object from prose.
		start := strings.Index(trimmed, "{")
		end := strings.LastIndex(trimmed, "}")
		if start < 0 || end <= start {
			return themeCandidate{}, fmt.Errorf("parse theme response: %w", err)
		}
		if err := json.Unmarshal([]byte(trimmed[start:end+1]), &candidate); err != nil {
			return themeCandidate{}, fmt.Errorf("parse theme response: %w", err)
		}
	}

	candidate.Content = strings.TrimSpace(candidate.Content)
	candidate.SubjectActorID = strings.TrimSpace(candidate.SubjectActorID)
	candidate.SubjectActorName = strings.TrimSpace(candidate.SubjectActorName)
	if candidate.Content == "" {
		return themeCandidate{}, fmt.Errorf("parse theme response: empty content")
	}
	if candidate.Importance < 1 || candidate.Importance > 10 {
		candidate.Importance = 7
	}

	return candidate, nil
}
