package naturalmemory

import (
	"context"
	"fmt"
	"sort"
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

	if err := m.mergeNearDuplicates(ctx, kept, now); err != nil {
		return err
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
		return left, right
	case rightImportance > leftImportance:
		return right, left
	}

	leftScore := effectiveScore(left, decayFactor, now)
	rightScore := effectiveScore(right, decayFactor, now)
	switch {
	case leftScore > rightScore:
		return left, right
	case rightScore > leftScore:
		return right, left
	case left.UpdatedAt.After(right.UpdatedAt):
		return left, right
	default:
		return right, left
	}
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
