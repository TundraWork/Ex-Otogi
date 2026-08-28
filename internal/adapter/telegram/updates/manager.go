package updates

import (
	"context"
	"errors"
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
	// Cause is the operation failure that caused degradation, when available.
	Cause error
	// Err is the persistence failure, when durable recording also failed.
	Err error
}

// Health tracks update consistency readiness and publishes its first armed failure.
type Health struct {
	// boundary orders degradation commits with downstream health checks and capture.
	// Lock order is boundary, then mu; durable gap I/O is never performed by degrade.
	boundary       sync.Mutex
	mu             sync.RWMutex
	reasons        []DegradedReason
	failure        error
	captureFailure error
	armed          bool
	failed         chan error
}

// NewHealth creates healthy update readiness state.
func NewHealth() *Health { return &Health{failed: make(chan error, 1)} }

func (h *Health) degrade(reason DegradedReason) {
	h.boundary.Lock()
	h.degradeWithinBoundary(reason)
	h.boundary.Unlock()
}

// degradeWithinBoundary records a failure while the caller owns boundary.
func (h *Health) degradeWithinBoundary(reason DegradedReason) {
	h.mu.Lock()
	h.recordDegradationLocked(reason)
	h.mu.Unlock()
}

// degradeCaptureWithinBoundary additionally latches a downstream failure even
// before lifecycle notifications are armed during startup replay.
func (h *Health) degradeCaptureWithinBoundary(reason DegradedReason) {
	h.mu.Lock()
	if h.captureFailure == nil {
		h.captureFailure = degradationError(reason)
	}
	h.recordDegradationLocked(reason)
	h.mu.Unlock()
}

func (h *Health) recordDegradationLocked(reason DegradedReason) {
	h.reasons = append(h.reasons, reason)
	if h.armed && h.failure == nil {
		h.failure = degradationError(reason)
		h.failed <- h.failure
	}
}

// enterCaptureBoundary serializes the final health check through normalized
// capture. A nil error transfers boundary ownership to the caller, which must
// leave it. An armed lifecycle failure takes precedence; the capture latch also
// rejects queued startup work before lifecycle notification is armed.
func (h *Health) enterCaptureBoundary() error {
	h.boundary.Lock()
	h.mu.RLock()
	err := h.failure
	if err == nil {
		err = h.captureFailure
	}
	h.mu.RUnlock()
	if err != nil {
		h.boundary.Unlock()
		return err
	}
	return nil
}

func (h *Health) leaveCaptureBoundary() { h.boundary.Unlock() }

func degradationError(reason DegradedReason) error {
	message := fmt.Sprintf("updates degraded for %s at %d: %s", reason.Box, reason.Position, reason.Reason)
	switch {
	case reason.Cause != nil && reason.Err != nil:
		return fmt.Errorf("%s: %w (record degraded state: %w)", message, reason.Cause, reason.Err)
	case reason.Cause != nil:
		return fmt.Errorf("%s: %w", message, reason.Cause)
	case reason.Err != nil:
		return fmt.Errorf("%s: record degraded state: %w", message, reason.Err)
	default:
		return errors.New(message)
	}
}

func (h *Health) arm() error {
	h.boundary.Lock()
	defer h.boundary.Unlock()
	h.mu.Lock()
	defer h.mu.Unlock()
	h.armed = true
	if h.failure == nil && h.captureFailure != nil {
		h.failure = h.captureFailure
		h.failed <- h.failure
	}
	return h.failure
}

// Failed returns the bounded one-shot lifecycle failure notification.
func (h *Health) Failed() <-chan error { return h.failed }

func (h *Health) failureCause() error {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.failure
}

// Ready reports whether update consistency is intact and returns an owned copy
// of all reasons when degraded.
func (h *Health) Ready() (bool, []DegradedReason) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.reasons) == 0, append([]DegradedReason(nil), h.reasons...)
}

func recordGap(ctx context.Context, store GapStore, health *Health, reason DegradedReason) error {
	if reason.Cause == nil {
		reason.Cause = reason.Err
	}
	durableReason := reason.Reason
	if reason.Cause != nil {
		durableReason += ": " + reason.Cause.Error()
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
func RunManager(ctx context.Context, manager *gotdupdates.Manager, gate *JournalGate, api gotdupdates.API, userID int64, options gotdupdates.AuthOptions, ready func()) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	callerStart := options.OnStart
	started := make(chan struct{})
	gateResult := make(chan error, 1)
	options.OnStart = func(ctx context.Context) {
		close(started)
		go func() {
			err := gate.Open(ctx)
			if err == nil && ready != nil {
				ready()
			}
			gateResult <- err
			if err != nil {
				cancel()
			}
		}()
		if callerStart != nil {
			callerStart(ctx)
		}
	}
	healthResult := make(chan error, 1)
	go func() {
		select {
		case err := <-gate.health.Failed():
			healthResult <- err
			cancel()
		case <-ctx.Done():
		}
	}()
	managerErr := manager.Run(ctx, api, userID, options)
	select {
	case <-started:
		if gateErr := <-gateResult; gateErr != nil {
			return gateErr
		}
	default:
	}
	select {
	case healthErr := <-healthResult:
		return healthErr
	default:
		if healthErr := gate.health.failureCause(); healthErr != nil {
			return healthErr
		}
	}
	return managerErr
}
