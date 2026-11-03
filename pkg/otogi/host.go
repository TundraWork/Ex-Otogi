package otogi

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

// Host gives a module its long-lived capabilities. The kernel passes it to
// Starter.Start and exposes it on every Invocation. New capabilities are
// added as new methods.
type Host interface {
	// Module is the module's ID.
	Module() ModuleID
	// Logger logs with the module ID attached.
	Logger() *slog.Logger
	// Record writes an event log record with component "module:<id>". A
	// record without an event, written while handling one, joins that
	// event's trace.
	Record(ctx context.Context, r Record)
	// Store is the module's own key/value store.
	Store() Store
	// Database opens (on first use) the module's private SQLite database.
	// The module owns its schema; the runtime closes it at shutdown.
	Database(ctx context.Context) (*sql.DB, error)

	// Schedule runs job on spec while the process runs: a cron expression
	// ("*/5 * * * *") or "@every 30s". Call it only from Start. A run is
	// skipped if the previous one has not finished; runs missed while the
	// process was down are not replayed.
	Schedule(name, spec string, job Job) error
	// SetTimer persists a timer that calls the module's HandleTimer at at,
	// even across restarts; a timer that came due while the process was down
	// fires on start. Setting an existing key replaces it. The module must
	// implement TimerHandler.
	SetTimer(ctx context.Context, key string, at time.Time, payload []byte) error
	// CancelTimer removes a timer; a missing key is not an error.
	CancelTimer(ctx context.Context, key string) error

	// Provide registers a service other modules can look up. Call it only
	// from Start, with a name listed in the descriptor's Provides.
	Provide(name string, service any) error
	// Service returns a service listed in the descriptor's Requires. Use
	// Lookup for a typed result.
	Service(name string) (any, bool)
}

// Job is a scheduled unit of module work. inv has no event: reply with
// SendTo.
type Job func(ctx context.Context, inv Invocation) error

// Timer is a persisted timer as delivered to HandleTimer.
type Timer struct {
	// Key identifies the timer within the module.
	Key string `json:"key"`
	// At is when the timer was due.
	At time.Time `json:"at"`
	// Payload is the data given to SetTimer.
	Payload []byte `json:"payload,omitempty"`
}

// TimerHandler is implemented by modules that set timers. A failed run is
// retried a minute later; effects stay deduplicated across retries.
type TimerHandler interface {
	// HandleTimer runs when t is due.
	HandleTimer(ctx context.Context, t Timer, inv Invocation) error
}

// Origin says what a unit of module work is for: exactly one of an event, a
// job run, or a timer.
type Origin struct {
	// Event is set for event deliveries.
	Event *Event `json:"event,omitempty"`
	// Job is the job name for job runs.
	Job string `json:"job,omitempty"`
	// Timer is set for timer runs.
	Timer *Timer `json:"timer,omitempty"`
	// ScheduledAt is when a job or timer run was due.
	ScheduledAt time.Time `json:"scheduled_at,omitzero"`
}

// Lookup returns the service name as a T; see Host.Service.
func Lookup[T any](h Host, name string) (T, error) {
	var zero T
	v, ok := h.Service(name)
	if !ok {
		return zero, fmt.Errorf("service %q is not available to module %q: %w", name, h.Module(), ErrUnavailable)
	}
	t, ok := v.(T)
	if !ok {
		return zero, fmt.Errorf("service %q is a %T, not a %T: %w", name, v, zero, ErrInvalid)
	}
	return t, nil
}

// Store is a module's persistent key/value store. Keys are the module's own;
// other modules cannot see them.
type Store interface {
	// Get returns the value at key and whether it exists.
	Get(ctx context.Context, key string) ([]byte, bool, error)
	// Put sets the value at key.
	Put(ctx context.Context, key string, value []byte) error
	// Delete removes key; deleting a missing key is not an error.
	Delete(ctx context.Context, key string) error
	// List returns the keys starting with prefix, sorted.
	List(ctx context.Context, prefix string) ([]string, error)
}

// Starter is implemented by modules that set up before receiving events,
// for example to open their database. Start runs once, before any event.
type Starter interface {
	// Start prepares the module. An error fails startup.
	Start(ctx context.Context, host Host) error
}

// Stopper is implemented by modules that clean up after the last event.
type Stopper interface {
	// Stop releases the module's resources.
	Stop(ctx context.Context) error
}

// GetJSON reads the JSON value at key into a T.
func GetJSON[T any](ctx context.Context, s Store, key string) (T, bool, error) {
	var v T
	data, ok, err := s.Get(ctx, key)
	if err != nil || !ok {
		return v, ok, err
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return v, false, fmt.Errorf("decode %q: %w", key, err)
	}
	return v, true, nil
}

// PutJSON stores v as JSON at key.
func PutJSON(ctx context.Context, s Store, key string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encode %q: %w", key, err)
	}
	return s.Put(ctx, key, data)
}
