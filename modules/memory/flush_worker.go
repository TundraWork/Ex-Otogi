package memory

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	panel "ex-otogi/pkg/otogi/management"
)

// flushWorker periodically scans the window manager for ready windows
// and dispatches them to processWindow for extraction.
type flushWorker struct {
	interval time.Duration
	clock    func() time.Time
	manager  *windowManager
	process  func(ctx context.Context, w readyWindow) error
	logger   *slog.Logger
	recorder panel.Recorder
	stopCh   chan struct{}
	doneCh   chan struct{}
}

// Start begins the flush worker loop in a background goroutine.
func (w *flushWorker) Start(ctx context.Context) {
	w.stopCh = make(chan struct{})
	w.doneCh = make(chan struct{})

	// Capture channels as local variables to avoid racing with Stop(),
	// which nils the struct fields while the goroutine is still running.
	stopCh := w.stopCh
	doneCh := w.doneCh

	go func() {
		defer close(doneCh)
		defer func() {
			if recovered := recover(); recovered != nil && w.logger != nil {
				w.logger.ErrorContext(ctx, "memory flush worker panic", "recover", recovered)
			}
		}()

		ticker := time.NewTicker(w.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				w.drainAll(ctx)
				return
			case <-stopCh:
				w.drainAll(ctx)
				return
			case <-ticker.C:
				w.flushReady(ctx)
			}
		}
	}()
}

// Stop signals the worker to stop and waits for it to finish.
func (w *flushWorker) Stop(ctx context.Context) error {
	if w == nil || w.stopCh == nil {
		return nil
	}

	stopCh := w.stopCh
	doneCh := w.doneCh
	w.stopCh = nil
	w.doneCh = nil
	close(stopCh)

	if doneCh == nil {
		return nil
	}

	select {
	case <-doneCh:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("memory flush worker stop: %w", ctx.Err())
	}
}

func (w *flushWorker) flushReady(ctx context.Context) {
	if w.manager == nil || w.process == nil {
		return
	}

	ready := w.manager.Ready(w.clock())
	for _, rw := range ready {
		if err := ctx.Err(); err != nil {
			return
		}
		if err := w.process(ctx, rw); err != nil {
			w.recordProcessingFailure(ctx, rw, err)
			if w.logger == nil {
				continue
			}
			w.logger.WarnContext(ctx, "memory flush worker process",
				"scope_platform", rw.Scope.Platform,
				"scope_conversation_id", rw.Scope.ConversationID,
				"reason", string(rw.Reason),
				"article_count", len(rw.Articles),
				"error", err,
			)
		}
	}
}

func (w *flushWorker) drainAll(ctx context.Context) {
	if w.manager == nil || w.process == nil {
		return
	}

	drained := w.manager.DrainAll()
	for _, rw := range drained {
		if err := ctx.Err(); err != nil {
			return
		}
		if err := w.process(ctx, rw); err != nil {
			w.recordProcessingFailure(ctx, rw, err)
			if w.logger == nil {
				continue
			}
			w.logger.WarnContext(ctx, "memory flush worker drain",
				"scope_platform", rw.Scope.Platform,
				"scope_conversation_id", rw.Scope.ConversationID,
				"reason", string(rw.Reason),
				"article_count", len(rw.Articles),
				"error", err,
			)
		}
	}
}

func (w *flushWorker) recordProcessingFailure(ctx context.Context, rw readyWindow, err error) {
	if w == nil || w.recorder == nil || err == nil {
		return
	}

	occurredAt := time.Now().UTC()
	if w.clock != nil {
		occurredAt = w.clock().UTC()
	}

	_, recordErr := w.recorder.RecordEvent(ctx, newNaturalMemoryEvent(
		occurredAt,
		&rw.Scope,
		panel.EventLevelError,
		"flush-worker",
		"memory.window.processing.failed",
		"failed processing flushed memory window",
		processingFailureDescription(string(rw.Reason), rw.Articles, err),
		panel.MemoryWindowProcessingFailedPayload{
			Reason:       string(rw.Reason),
			ArticleCount: len(rw.Articles),
			Error:        err.Error(),
		},
	))
	if recordErr != nil {
		return
	}
}
