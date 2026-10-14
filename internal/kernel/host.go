package kernel

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// ModuleStorage provides module state; the composition root supplies an
// implementation (internal/adapter/modulestore).
type ModuleStorage interface {
	// Store returns the key/value store of module.
	Store(module otogi.ModuleID) otogi.Store
	// Database opens the private database of module.
	Database(ctx context.Context, module otogi.ModuleID) (*sql.DB, error)
}

// host is one module's otogi.Host.
type host struct {
	kernel *Kernel
	module otogi.ModuleID
	log    *slog.Logger
}

func (h *host) Module() otogi.ModuleID { return h.module }
func (h *host) Logger() *slog.Logger   { return h.log }

func (h *host) Record(ctx context.Context, r otogi.Record) {
	r.Component = "module:" + string(h.module)
	if r.At.IsZero() {
		r.At = time.Now()
	}
	h.kernel.cfg.Recorder.Record(ctx, r)
}

func (h *host) Store() otogi.Store {
	if h.kernel.cfg.Storage == nil {
		return unavailableStore{}
	}
	return h.kernel.cfg.Storage.Store(h.module)
}

func (h *host) Database(ctx context.Context) (*sql.DB, error) {
	if h.kernel.cfg.Storage == nil {
		return nil, fmt.Errorf("module database: no storage configured: %w", otogi.ErrUnavailable)
	}
	return h.kernel.cfg.Storage.Database(ctx, h.module)
}

// unavailableStore is the store when the kernel has no storage configured.
type unavailableStore struct{}

var errNoStorage = fmt.Errorf("module store: no storage configured: %w", otogi.ErrUnavailable)

func (unavailableStore) Get(context.Context, string) ([]byte, bool, error) {
	return nil, false, errNoStorage
}
func (unavailableStore) Put(context.Context, string, []byte) error      { return errNoStorage }
func (unavailableStore) Delete(context.Context, string) error           { return errNoStorage }
func (unavailableStore) List(context.Context, string) ([]string, error) { return nil, errNoStorage }
