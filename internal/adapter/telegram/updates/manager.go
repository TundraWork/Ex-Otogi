package updates

import (
	"context"
	"fmt"
	"sync"

	"github.com/gotd/td/telegram"
	gotdupdates "github.com/gotd/td/telegram/updates"
	"github.com/gotd/td/telegram/updates/hook"
)

// GapStore durably records an unrecoverable update boundary.
type GapStore interface {
	// RecordGap marks one consistency stream degraded at a durable boundary.
	RecordGap(context.Context, string, int64, string) error
}

// DegradedReason is one readiness-blocking consistency failure.
type DegradedReason struct {
	// Box identifies the common or channel consistency stream.
	Box string
	// Position is the last known source position.
	Position int64
	// Reason classifies the consistency failure.
	Reason string
	// Err is the persistence failure, when durable recording also failed.
	Err error
}

// Health tracks update consistency readiness independently of logging.
type Health struct {
	mu      sync.RWMutex
	reasons []DegradedReason
}

// NewHealth creates healthy update readiness state.
func NewHealth() *Health { return &Health{} }

func (h *Health) degrade(reason DegradedReason) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.reasons = append(h.reasons, reason)
}

// Ready reports whether update consistency is intact and returns an owned copy
// of all reasons when degraded.
func (h *Health) Ready() (bool, []DegradedReason) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.reasons) == 0, append([]DegradedReason(nil), h.reasons...)
}

func recordGap(ctx context.Context, store GapStore, health *Health, reason DegradedReason) error {
	durableReason := reason.Reason
	if reason.Err != nil {
		durableReason += ": " + reason.Err.Error()
	}
	err := store.RecordGap(ctx, reason.Box, reason.Position, durableReason)
	reason.Err = err
	health.degrade(reason)
	return err
}

// Callbacks contains all gotd manager gap callbacks. None are left to gotd's
// log-only defaults.
type Callbacks struct {
	// OnChannelTooLong records an unrecoverable channel difference.
	OnChannelTooLong func(int64)
	// OnChannelInaccessible records lost channel access.
	OnChannelInaccessible func(int64)
	// OnTooLong records an unrecoverable common difference.
	OnTooLong func()
	// OnLoadUserStateFailed records missing common state at startup.
	OnLoadUserStateFailed func()
	// OnLoadChannelStateFailed records missing channel recovery state.
	OnLoadChannelStateFailed func(int64)
}

// GapCallbacks constructs callbacks that synchronously persist degraded state.
func GapCallbacks(store GapStore, health *Health) Callbacks {
	if store == nil || health == nil {
		panic("updates: nil gap dependency")
	}
	record := func(box string, position int64, reason string) {
		_ = recordGap(context.Background(), store, health, DegradedReason{Box: box, Position: position, Reason: reason})
	}
	return Callbacks{
		OnLoadUserStateFailed:    func() { record("common", 0, "load user state failed") },
		OnLoadChannelStateFailed: func(id int64) { record(fmt.Sprintf("channel:%d", id), 0, "load channel state failed") },
		OnTooLong:                func() { record("common", 0, "difference too long") },
		OnChannelTooLong:         func(id int64) { record(fmt.Sprintf("channel:%d", id), 0, "channel difference too long") },
		OnChannelInaccessible:    func(id int64) { record(fmt.Sprintf("channel:%d", id), 0, "channel inaccessible") },
	}
}

// ManagerConfig supplies durable manager dependencies.
type ManagerConfig struct {
	// APIStorage persists gotd common and channel cursors.
	APIStorage gotdupdates.StateStorage
	// AccessHasher persists channel access hashes.
	AccessHasher gotdupdates.ChannelAccessHasher
	// UserAccessHasher persists user access hashes.
	UserAccessHasher gotdupdates.UserAccessHasher
	// Journal stores raw transport packets.
	Journal journalStore
	// Downstream receives manager-ordered updates.
	Downstream telegram.UpdateHandler
	// GapStore persists degraded consistency state.
	GapStore GapStore
	// Health is shared by manager storage/callbacks and downstream capture.
	Health *Health
}

// NewManager creates a gotd manager, its shared closed journal gate, complete
// callbacks, and readiness state.
func NewManager(config ManagerConfig) (*gotdupdates.Manager, *JournalGate, Callbacks, *Health) {
	if config.Health == nil {
		panic("updates: nil shared health")
	}
	health := config.Health
	callbacks := GapCallbacks(config.GapStore, health)
	correlation := newJournalCorrelation()
	manager := gotdupdates.New(gotdupdates.Config{
		Handler:                  correlatingHandler{correlation: correlation, next: config.Downstream},
		Storage:                  monitoredStateStorage{next: config.APIStorage, gaps: config.GapStore, health: health},
		AccessHasher:             config.AccessHasher,
		UserAccessHasher:         config.UserAccessHasher,
		OnChannelTooLong:         callbacks.OnChannelTooLong,
		OnChannelInaccessible:    callbacks.OnChannelInaccessible,
		OnTooLong:                callbacks.OnTooLong,
		OnLoadUserStateFailed:    callbacks.OnLoadUserStateFailed,
		OnLoadChannelStateFailed: callbacks.OnLoadChannelStateFailed,
	})
	return manager, newJournalGate(config.Journal, config.GapStore, health, manager, correlation), callbacks, health
}

// Middlewares returns the required raw-invoker middleware order: journaled
// UpdatesClass capture first, then direct affected-pts application.
func Middlewares(gate *JournalGate, manager *gotdupdates.Manager) []telegram.Middleware {
	return []telegram.Middleware{hook.UpdateHook(gate.Handle), hook.AffectedHook(manager)}
}

// ConfigureOptions installs the same journal gate for socket and RPC update
// paths, followed by direct affected-pts handling on the raw invoker.
func ConfigureOptions(options *telegram.Options, gate *JournalGate, manager *gotdupdates.Manager) {
	if options == nil || gate == nil || manager == nil {
		panic("updates: nil client option dependency")
	}
	options.UpdateHandler = gate
	options.Middlewares = append(options.Middlewares, Middlewares(gate, manager)...)
}

// RunManager starts gotd state recovery and opens the journal gate asynchronously
// after state initialization. Replay retains the gate lock until complete, while
// the manager worker can consume its bounded internal queue.
func RunManager(ctx context.Context, manager *gotdupdates.Manager, gate *JournalGate, api gotdupdates.API, userID int64, options gotdupdates.AuthOptions) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	callerStart := options.OnStart
	started := make(chan struct{})
	gateResult := make(chan error, 1)
	options.OnStart = func(ctx context.Context) {
		close(started)
		go func() {
			err := gate.Open(ctx)
			gateResult <- err
			if err != nil {
				cancel()
			}
		}()
		if callerStart != nil {
			callerStart(ctx)
		}
	}
	managerErr := manager.Run(ctx, api, userID, options)
	select {
	case <-started:
		if gateErr := <-gateResult; gateErr != nil {
			return gateErr
		}
	default:
	}
	return managerErr
}
