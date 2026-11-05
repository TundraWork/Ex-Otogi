package qq

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	qqstore "github.com/TundraWork/Ex-Otogi/internal/adapter/qq/store"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// Submitter admits events into the kernel; *kernel.Kernel implements it.
type Submitter interface {
	// Submit admits event and calls done exactly once when it is processed.
	Submit(ctx context.Context, event otogi.Event, done func(otogi.Outcome)) error
}

const dispatchBatch = 100

// dispatcher maps inbox rows to events in arrival order and submits them to
// the kernel without waiting for them to finish; the kernel provides ordering
// per conversation and backpressure. A row is marked done when its event is.
// Rows left pending (because the process stopped first) are dispatched again
// on the next start.
type dispatcher struct {
	store  *qqstore.Store
	mapper *mapper
	api    *api
	kernel Submitter
	wake   <-chan struct{}
	poll   time.Duration
	log    *slog.Logger
	now    func() time.Time
	record func(kind, outcome string, payload any)

	mu     sync.Mutex
	failed error // first error from a done callback, surfaced by run
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
func (d *dispatcher) dispatch(ctx context.Context, row qqstore.InboxRow) (bool, error) {
	var f frame
	err := json.Unmarshal(row.Payload, &f)
	var event otogi.Event
	if err == nil {
		event, err = d.mapper.mapFrame(ctx, f, row.ReceivedAt)
	}
	if err == nil {
		event.ID = inboxEventID(row.ID)
		err = event.Validate()
	}
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if err != nil {
		d.log.Error("qq: cannot map inbox row; marking failed", "row", row.ID, "error", err)
		d.record("inbox.failed", "error", map[string]any{"row": row.ID, "error": err.Error(), "payload": json.RawMessage(row.Payload)})
		return true, d.store.MarkInboxFailed(ctx, row.ID, err.Error(), d.now())
	}
	if f.T == "INTERACTION_CREATE" {
		d.acknowledge(ctx, f)
	}
	if isMessage(f.T) && event.Direction == otogi.DirectionIncoming {
		// A reply to it may then be a passive one.
		if err := d.store.PutOrigin(ctx, string(event.ID), string(event.SourceID), string(event.Conversation.ID), row.ReceivedAt); err != nil {
			return false, err
		}
	}
	if err := d.kernel.Submit(ctx, event, func(o otogi.Outcome) { d.finish(row.ID, o) }); err != nil {
		// Events are validated above, so the kernel is shutting down or ctx
		// ended. The row stays pending for the next start.
		d.log.Info("qq: event not admitted; row left pending", "row", row.ID, "event", event.ID, "error", err)
		return false, nil
	}
	return true, nil
}

// acknowledge answers an interaction, which QQ requires: without it the
// user's client spins until it times out. A failure costs only that.
func (d *dispatcher) acknowledge(ctx context.Context, f frame) {
	var in struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(f.D, &in) != nil || in.ID == "" {
		return
	}
	if err := d.api.call(ctx, http.MethodPut, "/interactions/"+in.ID, map[string]int{"code": 0}, nil); err != nil {
		d.log.Warn("qq: acknowledge interaction", "interaction", in.ID, "error", err)
		d.record("effect.failed", "error", map[string]any{"effect": "acknowledge interaction", "interaction": in.ID, "error": err.Error()})
	}
}

// finish runs on a kernel goroutine when a row's event completes.
func (d *dispatcher) finish(rowID int64, outcome otogi.Outcome) {
	if outcome.Err != nil && outcome.DroppedBy == "" {
		return // the kernel stopped first; replay on next start
	}
	// A short, independent context: the dispatcher's may already be cancelled
	// during shutdown, and losing this write only causes a harmless replay.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := d.store.MarkInboxDone(ctx, rowID, d.now()); err != nil {
		d.mu.Lock()
		if d.failed == nil {
			d.failed = fmt.Errorf("qq: mark inbox row %d done: %w", rowID, err)
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
// event ID and therefore the same send idempotency keys.
func inboxEventID(rowID int64) otogi.EventID {
	sum := sha256.Sum256([]byte("qq-inbox:" + strconv.FormatInt(rowID, 10)))
	return otogi.EventID(fmt.Sprintf("qq-%x", sum[:16]))
}
