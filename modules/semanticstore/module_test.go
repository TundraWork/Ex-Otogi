package semanticstore

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ex-otogi/pkg/otogi/ai"
	"ex-otogi/pkg/otogi/core"
)

func TestModuleOnRegister(t *testing.T) {
	t.Parallel()

	module := New()
	services := newSemanticStoreRegistryStub()
	if err := services.Register(serviceLogger, slog.Default()); err != nil {
		t.Fatalf("register logger failed: %v", err)
	}
	runtime := semanticStoreRuntimeStub{
		services: services,
		config: semanticStoreConfigRegistryStub{
			values: map[string]json.RawMessage{
				"semanticstore": json.RawMessage(`{"flush_interval":"1m","max_entries":50}`),
			},
		},
	}

	if err := module.OnRegister(context.Background(), runtime); err != nil {
		t.Fatalf("OnRegister failed: %v", err)
	}
	if module.cfg.MaxEntries != 50 {
		t.Fatalf("max_entries = %d, want 50", module.cfg.MaxEntries)
	}
	if module.cfg.FlushInterval != time.Minute {
		t.Fatalf("flush_interval = %s, want 1m", module.cfg.FlushInterval)
	}

	resolved, err := services.Resolve(ai.ServiceSemanticStore)
	if err != nil {
		t.Fatalf("resolve semantic store service failed: %v", err)
	}
	if resolved != module {
		t.Fatal("resolved service is not the module instance")
	}
}

func TestModuleOnRegisterInvalidConfig(t *testing.T) {
	t.Parallel()

	module := New()
	runtime := semanticStoreRuntimeStub{
		services: newSemanticStoreRegistryStub(),
		config: semanticStoreConfigRegistryStub{
			values: map[string]json.RawMessage{
				"semanticstore": json.RawMessage(`{"flush_interval":"soon"}`),
			},
		},
	}

	err := module.OnRegister(context.Background(), runtime)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "parse flush_interval") {
		t.Fatalf("error = %v, want parse flush_interval", err)
	}
}

func TestModuleOnStartAndShutdownPersistence(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "llm-memory.json")
	scope := testMemoryScope("chat-1")
	module := New(
		WithLogger(slog.Default()),
		withConfig(Config{
			PersistenceFile: path,
			MaxEntries:      10,
		}),
		withClock(fixedClock(time.Unix(100, 0).UTC())),
		withIDGenerator(sequenceIDs("mem-1")),
	)

	if err := module.OnStart(context.Background()); err != nil {
		t.Fatalf("OnStart failed: %v", err)
	}
	if _, err := module.Store(context.Background(), ai.SemanticEntry{
		Scope:     scope,
		Content:   "Persist me",
		Category:  "knowledge",
		Embedding: []float32{1, 0},
	}); err != nil {
		t.Fatalf("Store failed: %v", err)
	}
	if err := module.OnShutdown(context.Background()); err != nil {
		t.Fatalf("OnShutdown failed: %v", err)
	}

	reloaded := New(
		WithLogger(slog.Default()),
		withConfig(Config{
			PersistenceFile: path,
			MaxEntries:      10,
		}),
	)
	if err := reloaded.OnStart(context.Background()); err != nil {
		t.Fatalf("OnStart failed: %v", err)
	}
	records, err := reloaded.ListByScope(context.Background(), scope, 10)
	if err != nil {
		t.Fatalf("ListByScope failed: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("records len = %d, want 1", len(records))
	}
	if records[0].Content != "Persist me" {
		t.Fatalf("content = %q, want Persist me", records[0].Content)
	}
}

type semanticStoreRuntimeStub struct {
	services core.ServiceRegistry
	config   core.ConfigRegistry
}

func (r semanticStoreRuntimeStub) Services() core.ServiceRegistry { return r.services }
func (r semanticStoreRuntimeStub) Config() core.ConfigRegistry    { return r.config }
func (r semanticStoreRuntimeStub) Subscribe(context.Context, core.InterestSet, core.SubscriptionSpec, core.EventHandler) (core.Subscription, error) {
	return nil, errors.New("subscribe not implemented")
}

type semanticStoreConfigRegistryStub struct {
	values map[string]json.RawMessage
}

func (r semanticStoreConfigRegistryStub) Register(moduleName string, raw json.RawMessage) error {
	if r.values == nil {
		r.values = make(map[string]json.RawMessage)
	}
	r.values[moduleName] = raw
	return nil
}

func (r semanticStoreConfigRegistryStub) Resolve(moduleName string) (json.RawMessage, error) {
	if raw, exists := r.values[moduleName]; exists {
		return raw, nil
	}
	return nil, core.ErrConfigNotFound
}

type semanticStoreServiceRegistryStub struct {
	values map[string]any
}

func newSemanticStoreRegistryStub() *semanticStoreServiceRegistryStub {
	return &semanticStoreServiceRegistryStub{values: make(map[string]any)}
}

func (r *semanticStoreServiceRegistryStub) Register(name string, service any) error {
	if _, exists := r.values[name]; exists {
		return core.ErrServiceAlreadyRegistered
	}
	r.values[name] = service
	return nil
}

func (r *semanticStoreServiceRegistryStub) Resolve(name string) (any, error) {
	value, exists := r.values[name]
	if !exists {
		return nil, core.ErrServiceNotFound
	}
	return value, nil
}
