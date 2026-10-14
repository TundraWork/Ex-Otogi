package otogi

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
)

// Host gives a module its long-lived capabilities. The kernel passes it to
// Starter.Start and exposes it on every Invocation. New capabilities are
// added as new methods.
type Host interface {
	// Module is the module's ID.
	Module() ModuleID
	// Logger logs with the module ID attached.
	Logger() *slog.Logger
	// Record writes an event log record with component "module:<id>".
	Record(ctx context.Context, r Record)
	// Store is the module's own key/value store.
	Store() Store
	// Database opens (on first use) the module's private SQLite database.
	// The module owns its schema; the runtime closes it at shutdown.
	Database(ctx context.Context) (*sql.DB, error)
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
