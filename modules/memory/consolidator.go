package memory

import (
	"context"
	"fmt"
	"time"

	"ex-otogi/pkg/otogi/ai"
	panel "ex-otogi/pkg/otogi/management"
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
				m.logger.ErrorContext(ctx, "memory consolidation panic", "recover", recovered)
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
					m.logger.ErrorContext(ctx, "memory consolidation", "error", err)
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
		return fmt.Errorf("memory stop consolidation: %w", ctx.Err())
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
			m.logger.ErrorContext(ctx, "memory consolidate scope",
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

func (m *Module) consolidateScope(ctx context.Context, scope ai.SemanticScope) error {
	if m == nil || m.semanticStore == nil {
		return fmt.Errorf("semantic store service unavailable")
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
					m.logger.WarnContext(ctx, "memory pre-consolidation flush", "error", err)
				}
			}
		}
	}

	records, err := m.semanticStore.ListByScope(ctx, scope, 0)
	if err != nil {
		return fmt.Errorf("list scope memories: %w", err)
	}
	if len(records) == 0 {
		return nil
	}

	now := m.now()
	expiredCount := 0
	for _, record := range records {
		if record.Profile.ValidUntil != nil && record.Profile.ValidUntil.Before(now) {
			if err := m.semanticStore.Delete(ctx, record.ID); err != nil {
				return fmt.Errorf("delete expired memory %s: %w", record.ID, err)
			}
			expiredCount++
			continue
		}
	}
	keptCount := len(records) - expiredCount
	m.debugConsolidationScopeStart(ctx, scope, len(records), expiredCount, 0, keptCount)

	if expiredCount > 0 {
		m.emitManagementEvent(ctx, &scope, "memory.consolidation.pruned", "removed expired memories", consolidationPrunedDescription(expiredCount, 0, keptCount), panel.MemoryConsolidationPrunedPayload{
			TotalRecords:     len(records),
			ExpiredCount:     expiredCount,
			DecayPrunedCount: 0,
			KeptCount:        keptCount,
		})
	}
	return nil
}
