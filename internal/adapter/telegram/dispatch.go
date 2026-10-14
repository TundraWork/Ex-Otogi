package telegram

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/telegram/mapper"
	telegramstore "github.com/TundraWork/Ex-Otogi/internal/adapter/telegram/store"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// Submitter admits events into the kernel; *kernel.Kernel implements it.
type Submitter interface {
	// Submit admits event and calls done exactly once when it is processed.
	Submit(ctx context.Context, event otogi.Event, done func(otogi.Outcome)) error
}

type dispatchStore interface {
	PendingInbox(context.Context, int64, int) ([]telegramstore.InboxRow, error)
	MarkInboxDone(context.Context, int64, time.Time) error
	MarkInboxFailed(context.Context, int64, string, time.Time) error
	PutLocator(context.Context, string, int, string, time.Time) error
	FindLocator(context.Context, string, int) (string, bool, error)
}

const dispatchBatch = 100

// dispatcher maps inbox rows to events in arrival order and submits them to
// the kernel without waiting for them to finish; the kernel provides ordering
// per conversation and backpressure. A row is marked done once all its events
// are done. Rows left pending (because the process stopped first) are
// dispatched again on the next start.
type dispatcher struct {
	store  dispatchStore
	mapper *mapper.Mapper
	kernel Submitter
	wake   <-chan struct{}
	poll   time.Duration
	log    *slog.Logger
	now    func() time.Time

	mu      sync.Mutex
	pending map[int64]*rowProgress
	failed  error // first error from a done callback, surfaced by run
}

type rowProgress struct {
	remaining int
	abandoned bool
}

// run dispatches rows until ctx ends, the kernel stops admitting events, or
// a store write fails (the only error it returns).
func (d *dispatcher) run(ctx context.Context) error {
	var after int64
	for {
		rows, err := d.store.PendingInbox(ctx, after, dispatchBatch)
		if err != nil {
			return ignoreCancel(ctx, err)
		}
		for _, row := range rows {
			admitted, err := d.dispatch(ctx, row)
			if err != nil {
				return ignoreCancel(ctx, err)
			}
			if !admitted {
				return nil // the kernel is shutting down
			}
			after = row.ID
		}
		if err := d.callbackError(); err != nil {
			return err
		}
		if len(rows) == dispatchBatch {
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-d.wake:
		case <-time.After(d.poll):
		}
	}
}

// dispatch maps and submits one row. It reports false when the kernel no
// longer admits events; the row then stays pending for the next start.
func (d *dispatcher) dispatch(ctx context.Context, row telegramstore.InboxRow) (bool, error) {
	events, err := d.mapRow(ctx, row)
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if err != nil {
		d.log.Error("telegram: cannot map inbox row; marking failed", "row", row.ID, "error", err)
		return true, d.store.MarkInboxFailed(ctx, row.ID, err.Error(), d.now())
	}
	if len(events) == 0 {
		return true, d.store.MarkInboxDone(ctx, row.ID, d.now())
	}
	d.mu.Lock()
	d.pending[row.ID] = &rowProgress{remaining: len(events)}
	d.mu.Unlock()
	for i, event := range events {
		event.ID = inboxEventID(row.ID, i)
		if err := d.kernel.Submit(ctx, event, func(o otogi.Outcome) { d.finish(row.ID, o) }); err != nil {
			// Events are validated in mapRow, so the kernel is shutting down
			// or ctx ended. The row stays pending for the next start.
			d.log.Info("telegram: event not admitted; row left pending", "row", row.ID, "event", event.ID, "error", err)
			d.mu.Lock()
			d.pending[row.ID].abandoned = true
			d.mu.Unlock()
			return false, nil
		}
	}
	return true, nil
}

// mapRow decodes a row, records its locators, and maps it to events,
// resolving common-box deletions through earlier locators.
func (d *dispatcher) mapRow(ctx context.Context, row telegramstore.InboxRow) ([]otogi.Event, error) {
	update, err := tg.DecodeUpdate(&bin.Buffer{Buf: row.Payload})
	if err != nil {
		return nil, fmt.Errorf("decode update: %w", err)
	}
	result, err := d.mapper.Map(update)
	if err != nil {
		return nil, err
	}
	for _, l := range result.Locators {
		if err := d.store.PutLocator(ctx, l.Box, l.MessageID, l.Conversation, row.ReceivedAt); err != nil {
			return nil, err
		}
	}
	events := result.Events
	for _, del := range result.Deletes {
		conversation := del.Conversation
		if conversation == "" {
			found := false
			if conversation, found, err = d.store.FindLocator(ctx, del.Box, del.MessageID); err != nil {
				return nil, err
			}
			if !found {
				d.log.Warn("telegram: deleted message has no known conversation; skipping", "box", del.Box, "message", del.MessageID)
				continue
			}
		}
		events = append(events, d.mapper.MapDeletion(del, conversation))
	}
	for _, event := range events {
		if err := event.Validate(); err != nil {
			return nil, err
		}
	}
	return events, nil
}

// finish runs on a kernel goroutine when one event of a row completes.
func (d *dispatcher) finish(rowID int64, outcome otogi.Outcome) {
	d.mu.Lock()
	progress := d.pending[rowID]
	progress.remaining--
	if outcome.Err != nil && outcome.DroppedBy == "" {
		progress.abandoned = true // kernel stopped first; replay on next start
	}
	last, abandoned := progress.remaining == 0, progress.abandoned
	if last {
		delete(d.pending, rowID)
	}
	d.mu.Unlock()
	if !last || abandoned {
		return
	}
	// A short, independent context: the dispatcher's may already be cancelled
	// during shutdown, and losing this write only causes a harmless replay.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := d.store.MarkInboxDone(ctx, rowID, d.now()); err != nil {
		d.mu.Lock()
		if d.failed == nil {
			d.failed = fmt.Errorf("telegram: mark inbox row %d done: %w", rowID, err)
		}
		d.mu.Unlock()
	}
}

func (d *dispatcher) callbackError() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.failed
}

func ignoreCancel(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return nil
	}
	return err
}

// inboxEventID derives a stable event ID, so a replayed row produces the same
// event IDs and therefore the same send idempotency keys.
func inboxEventID(rowID int64, index int) otogi.EventID {
	sum := sha256.Sum256([]byte("telegram-inbox:" + strconv.FormatInt(rowID, 10) + ":" + strconv.Itoa(index)))
	return otogi.EventID(fmt.Sprintf("tg-%x", sum[:16]))
}
