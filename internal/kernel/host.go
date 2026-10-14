package kernel

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// ModuleStorage provides module state; the composition root supplies an
// implementation (internal/adapter/modulestore).
type ModuleStorage interface {
	// Store returns the key/value store of module.
	Store(module otogi.ModuleID) otogi.Store
	// Database opens the private database of module.
	Database(ctx context.Context, module otogi.ModuleID) (*sql.DB, error)
	// SetTimer persists a timer, replacing one with the same key.
	SetTimer(ctx context.Context, module otogi.ModuleID, key string, at time.Time, payload []byte) error
	// CancelTimer removes a timer.
	CancelTimer(ctx context.Context, module otogi.ModuleID, key string) error
	// DueTimers calls fn for every timer of modules whose next attempt is due.
	DueTimers(ctx context.Context, now time.Time, modules []otogi.ModuleID, fn func(otogi.ModuleID, otogi.Timer)) error
	// FinishTimer deletes a timer run unless the timer was replaced meanwhile.
	FinishTimer(ctx context.Context, module otogi.ModuleID, key string, at time.Time) error
	// RetryTimer postpones the next attempt of a timer run.
	RetryTimer(ctx context.Context, module otogi.ModuleID, key string, at, next time.Time) error
}

// host is one module's otogi.Host.
type host struct {
	kernel *Kernel
	entry  *moduleEntry
	log    *slog.Logger
}

func (h *host) Module() otogi.ModuleID { return h.entry.descriptor.ID }
func (h *host) Logger() *slog.Logger   { return h.log }

func (h *host) Record(ctx context.Context, r otogi.Record) {
	r.Component = "module:" + string(h.Module())
	if r.At.IsZero() {
		r.At = time.Now()
	}
	h.kernel.cfg.Recorder.Record(ctx, r)
}

func (h *host) Store() otogi.Store {
	if h.kernel.cfg.Storage == nil {
		return unavailableStore{}
	}
	return h.kernel.cfg.Storage.Store(h.Module())
}

func (h *host) Database(ctx context.Context) (*sql.DB, error) {
	if h.kernel.cfg.Storage == nil {
		return nil, errNoStorage
	}
	return h.kernel.cfg.Storage.Database(ctx, h.Module())
}

func (h *host) Schedule(name, spec string, fn otogi.Job) error {
	schedule, err := cron.ParseStandard(spec)
	if err != nil {
		return fmt.Errorf("schedule %q: spec %q: %w: %w", name, spec, otogi.ErrInvalid, err)
	}
	k := h.kernel
	k.mu.Lock()
	defer k.mu.Unlock()
	if !k.starting {
		return fmt.Errorf("schedule %q: only during Start: %w", name, otogi.ErrInvalid)
	}
	for _, j := range k.jobs {
		if j.entry == h.entry && j.name == name {
			return fmt.Errorf("schedule %q: name already used: %w", name, otogi.ErrInvalid)
		}
	}
	k.jobs = append(k.jobs, &job{entry: h.entry, name: name, schedule: schedule, fn: fn, next: schedule.Next(time.Now())})
	return nil
}

func (h *host) SetTimer(ctx context.Context, key string, at time.Time, payload []byte) error {
	if _, ok := h.entry.module.(otogi.TimerHandler); !ok {
		return fmt.Errorf("set timer: module %q does not implement HandleTimer: %w", h.Module(), otogi.ErrInvalid)
	}
	if h.kernel.cfg.Storage == nil {
		return errNoStorage
	}
	return h.kernel.cfg.Storage.SetTimer(ctx, h.Module(), key, at, payload)
}

func (h *host) CancelTimer(ctx context.Context, key string) error {
	if h.kernel.cfg.Storage == nil {
		return errNoStorage
	}
	return h.kernel.cfg.Storage.CancelTimer(ctx, h.Module(), key)
}

func (h *host) Provide(name string, service any) error {
	if !h.entry.provides[name] {
		return fmt.Errorf("provide %q: not in the descriptor's Provides: %w", name, otogi.ErrInvalid)
	}
	k := h.kernel
	k.mu.Lock()
	defer k.mu.Unlock()
	if !k.starting {
		return fmt.Errorf("provide %q: only during Start: %w", name, otogi.ErrInvalid)
	}
	k.services[name] = service
	return nil
}

func (h *host) Service(name string) (any, bool) {
	if !h.entry.requires[name] {
		return nil, false
	}
	k := h.kernel
	k.mu.Lock()
	defer k.mu.Unlock()
	service, ok := k.services[name]
	return service, ok
}

// unavailableStore is the store when the kernel has no storage configured.
type unavailableStore struct{}

var errNoStorage = fmt.Errorf("module storage: none configured: %w", otogi.ErrUnavailable)

func (unavailableStore) Get(context.Context, string) ([]byte, bool, error) {
	return nil, false, errNoStorage
}
func (unavailableStore) Put(context.Context, string, []byte) error      { return errNoStorage }
func (unavailableStore) Delete(context.Context, string) error           { return errNoStorage }
func (unavailableStore) List(context.Context, string) ([]string, error) { return nil, errNoStorage }
