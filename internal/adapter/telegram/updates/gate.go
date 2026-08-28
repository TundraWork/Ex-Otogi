// Package updates integrates gotd update recovery with durable Telegram storage.
package updates

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"

	telegramstore "github.com/TundraWork/Ex-Otogi/internal/adapter/telegram/store"
)

const (
	// StartupJournalLimit bounds journal row IDs retained before manager startup.
	StartupJournalLimit = 64
)

var (
	// ErrStartupOverflow reports that the startup journal gate exceeded its bound.
	ErrStartupOverflow = telegramstore.ErrStartupOverflow
)

type journalStore interface {
	AppendJournal(context.Context, []byte, time.Time) (int64, error)
	StartupJournalIDs(context.Context, int) ([]int64, error)
	Journal(context.Context, int64) (telegramstore.JournalRow, bool, error)
	MarkJournalNormalized(context.Context, int64, time.Time) error
}

type journalContextKey struct{}

type journalCompleteKey struct{}

func journalComplete(ctx context.Context) bool {
	complete, found := ctx.Value(journalCompleteKey{}).(bool)
	return !found || complete
}

// JournalID returns the raw transport journal row associated with the current
// manager delivery. It is zero for manager-synthesized difference updates.
func JournalID(ctx context.Context) int64 {
	id, _ := ctx.Value(journalContextKey{}).(int64)
	return id
}

// JournalGate durably records update packets and prevents Manager.Handle from
// running before manager initialization. A single gate must serve both socket
// UpdateHandler and RPC UpdateHook paths.
type JournalGate struct {
	store       journalStore
	gaps        GapStore
	health      *Health
	next        telegram.UpdateHandler
	correlation *journalCorrelation

	mu      sync.Mutex
	open    bool
	failed  error
	pending []int64
}

// NewJournalGate constructs a closed startup gate.
func NewJournalGate(store journalStore, gaps GapStore, health *Health, next telegram.UpdateHandler) *JournalGate {
	return newJournalGate(store, gaps, health, next, newJournalCorrelation())
}

func newJournalGate(store journalStore, gaps GapStore, health *Health, next telegram.UpdateHandler, correlation *journalCorrelation) *JournalGate {
	if store == nil || gaps == nil || health == nil || next == nil || correlation == nil {
		panic("updates: nil journal gate dependency")
	}
	return &JournalGate{store: store, gaps: gaps, health: health, next: next, correlation: correlation}
}

// Handle implements telegram.UpdateHandler. Receipt serialization preserves
// journal order and prevents a live manager delivery from overtaking replay.
func (g *JournalGate) Handle(ctx context.Context, update tg.UpdatesClass) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if healthErr := g.health.failureCause(); healthErr != nil {
		g.failed = healthErr
		return healthErr
	}
	if g.failed != nil {
		return g.failed
	}
	payload, err := encodeUpdates(update)
	if err != nil {
		return fmt.Errorf("encode update journal: %w", err)
	}
	id, err := g.store.AppendJournal(ctx, payload, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("append update journal: %w", err)
	}
	if !g.open {
		if len(g.pending) == StartupJournalLimit {
			g.failed = ErrStartupOverflow
			return g.failed
		}
		g.pending = append(g.pending, id)
		return nil
	}
	err = g.deliver(ctx, id, update)
	if err != nil {
		g.failed = err
	}
	return err
}

// Open recovers pending journal rows, atomically opens the gate, and replays all
// buffered rows in receipt order before allowing a live delivery to overtake.
func (g *JournalGate) Open(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.failed != nil {
		return g.failed
	}
	if g.open {
		return nil
	}

	ids, err := g.store.StartupJournalIDs(ctx, StartupJournalLimit)
	if err != nil {
		if errors.Is(err, telegramstore.ErrStartupOverflow) {
			g.failed = ErrStartupOverflow
		}
		return err
	}
	ids = mergeOrderedIDs(ids, g.pending)
	if len(ids) > StartupJournalLimit {
		g.failed = ErrStartupOverflow
		return g.failed
	}
	for _, id := range ids {
		row, found, err := g.store.Journal(ctx, id)
		if err != nil {
			return fmt.Errorf("load update journal %d: %w", id, err)
		}
		if !found {
			return fmt.Errorf("load update journal %d: missing row", id)
		}
		update, err := decodeUpdates(row.Payload)
		if err != nil {
			return fmt.Errorf("decode update journal %d: %w", id, err)
		}
		if err := g.deliver(ctx, id, update); err != nil {
			g.failed = err
			return err
		}
	}
	g.pending = nil
	g.health.arm()
	g.open = true
	return nil
}

func (g *JournalGate) deliver(ctx context.Context, id int64, update tg.UpdatesClass) error {
	conservative := noDownstreamAck(update)
	if !conservative {
		if err := g.correlation.register(id, update); err != nil {
			return fmt.Errorf("correlate update journal %d: %w", id, err)
		}
	}
	if err := g.next.Handle(context.WithValue(ctx, journalContextKey{}, id), update); err != nil {
		return fmt.Errorf("handle journal %d: %w", id, err)
	}
	if conservative {
		const reason = "manager accepted source without public completion acknowledgement; full resync required"
		if err := recordGap(ctx, g.gaps, g.health, DegradedReason{Box: "common", Reason: reason}); err != nil {
			return fmt.Errorf("record journal %d resync requirement: %w", id, err)
		}
		if err := g.store.MarkJournalNormalized(ctx, id, time.Now().UTC()); err != nil {
			return fmt.Errorf("complete journal %d after resync requirement: %w", id, err)
		}
	}
	return nil
}

func noDownstreamAck(update tg.UpdatesClass) bool {
	switch value := update.(type) {
	case *tg.Updates:
		return len(value.Updates) == 0
	case *tg.UpdatesCombined:
		return len(value.Updates) == 0
	case *tg.UpdatesTooLong:
		return true
	default:
		return false
	}
}

func encodeUpdates(update tg.UpdatesClass) ([]byte, error) {
	if update == nil {
		return nil, errors.New("nil update")
	}
	var b bin.Buffer
	if err := (&tg.UpdatesBox{Updates: update}).Encode(&b); err != nil {
		return nil, err
	}
	return b.Copy(), nil
}

func decodeUpdates(payload []byte) (tg.UpdatesClass, error) {
	b := bin.Buffer{Buf: payload}
	update, err := tg.DecodeUpdates(&b)
	if err != nil {
		return nil, err
	}
	if b.Len() != 0 {
		return nil, fmt.Errorf("trailing update bytes: %d", b.Len())
	}
	return update, nil
}

func mergeOrderedIDs(stored, memory []int64) []int64 {
	merged := make([]int64, 0, len(stored)+len(memory))
	i, j := 0, 0
	for i < len(stored) || j < len(memory) {
		var id int64
		switch {
		case j >= len(memory) || i < len(stored) && stored[i] < memory[j]:
			id, i = stored[i], i+1
		case i >= len(stored) || memory[j] < stored[i]:
			id, j = memory[j], j+1
		default:
			id, i, j = stored[i], i+1, j+1
		}
		if len(merged) == 0 || merged[len(merged)-1] != id {
			merged = append(merged, id)
		}
	}
	return merged
}
