package naturalmemory

import (
	"context"
	"fmt"
	"strings"
	"time"

	"ex-otogi/pkg/otogi/ai"
	panel "ex-otogi/pkg/otogi/management"
)

const consolidationPruneScoreThreshold = 1.0

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
	startedAt := m.now()
	scopes := m.windowManager.ActiveScopes()
	m.debugConsolidationCycleStart(ctx, len(scopes))
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
	m.emitManagementEvent(ctx, nil, "memory.consolidation.cycle.completed", "completed natural memory consolidation cycle", consolidationCycleDescription(len(scopes), m.now().Sub(startedAt).Milliseconds()), panel.MemoryConsolidationCycleCompletedPayload{
		ScopeCount: len(scopes),
		ElapsedMS:  m.now().Sub(startedAt).Milliseconds(),
	})

	return nil
}

func (m *Module) consolidateScope(ctx context.Context, scope ai.LLMMemoryScope) error {
	if m == nil || m.llmMemory == nil {
		return fmt.Errorf("llm memory service unavailable")
	}

	if m.recorder != nil {
		ctx = panel.WithRecorder(ctx, m.recorder)
	}

	// Drain any pending window for this scope first so consolidation
	// operates on the latest state.
	if m.windowManager != nil {
		if drained, ok := m.windowManager.DrainScope(scope); ok {
			if err := m.processWindow(ctx, scope, drained.Articles, FlushReasonScope); err != nil {
				m.emitManagementErrorEvent(ctx, &scope, "consolidation", "memory.window.processing.failed", "failed processing scope-drained memory window", processingFailureDescription(string(FlushReasonScope), drained.Articles, err), panel.MemoryWindowProcessingFailedPayload{
					Reason:       string(FlushReasonScope),
					ArticleCount: len(drained.Articles),
					Error:        err.Error(),
				})
				if m.logger != nil {
					m.logger.WarnContext(ctx, "naturalmemory pre-consolidation flush", "error", err)
				}
			}
		}
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
	expiredCount := 0
	prunedCount := 0
	for _, record := range records {
		if record.Profile.ValidUntil != nil && record.Profile.ValidUntil.Before(now) {
			if err := m.llmMemory.Delete(ctx, record.ID); err != nil {
				return fmt.Errorf("delete expired memory %s: %w", record.ID, err)
			}
			expiredCount++
			continue
		}
		importance := memoryImportance(record)
		if importance < m.cfg.MinImportance || effectiveScore(record, m.cfg.DecayFactor, now) < consolidationPruneScoreThreshold {
			if err := m.llmMemory.Delete(ctx, record.ID); err != nil {
				return fmt.Errorf("delete pruned memory %s: %w", record.ID, err)
			}
			prunedCount++
			continue
		}
		kept = append(kept, record)
	}
	m.debugConsolidationScopeStart(ctx, scope, len(records), expiredCount, prunedCount, len(kept))

	if expiredCount > 0 || prunedCount > 0 {
		m.emitManagementEvent(ctx, &scope, "memory.consolidation.pruned", "pruned expired and low-score memories", consolidationPrunedDescription(expiredCount, prunedCount, len(kept)), panel.MemoryConsolidationPrunedPayload{
			TotalRecords:     len(records),
			ExpiredCount:     expiredCount,
			DecayPrunedCount: prunedCount,
			KeptCount:        len(kept),
		})
	}

	if len(kept) <= m.cfg.MaxMemoriesPerScope {
		return nil
	}

	sortMemoriesByScore(kept, m.cfg.DecayFactor, now)

	overflow := len(kept) - m.cfg.MaxMemoriesPerScope
	removed := kept[m.cfg.MaxMemoriesPerScope:]
	for _, record := range removed {
		if err := m.llmMemory.Delete(ctx, record.ID); err != nil {
			return fmt.Errorf("delete capped memory %s: %w", record.ID, err)
		}
	}
	m.debugConsolidationCapOverflow(ctx, len(kept), m.cfg.MaxMemoriesPerScope, overflow)
	removedPreview := strings.Join(firstMemoryContents(removed, 2), " | ")
	m.emitManagementEvent(ctx, &scope, "memory.consolidation.capped", "enforced per-scope memory cap", consolidationCappedDescription(overflow, m.cfg.MaxMemoriesPerScope, removedPreview), panel.MemoryConsolidationCappedPayload{
		TotalRecords: len(kept),
		MaxAllowed:   m.cfg.MaxMemoriesPerScope,
		RemovedCount: overflow,
	})

	return nil
}

func firstMemoryContents(records []ai.LLMMemoryRecord, limit int) []string {
	previews := make([]string, 0, limit)
	for _, record := range records {
		if strings.TrimSpace(record.Content) == "" {
			continue
		}
		previews = append(previews, record.Content)
		if len(previews) == limit {
			break
		}
	}

	return previews
}
