package kernel

import (
	"encoding/json"
	"fmt"
	"sync"

	"ex-otogi/pkg/otogi"
)

// ConfigRegistry is the default in-memory per-module configuration store.
type ConfigRegistry struct {
	mu      sync.RWMutex
	configs map[string]json.RawMessage
}

// NewConfigRegistry creates an empty module configuration registry.
func NewConfigRegistry() *ConfigRegistry {
	return &ConfigRegistry{
		configs: make(map[string]json.RawMessage),
	}
}

// Register stores raw JSON configuration for the named module.
func (r *ConfigRegistry) Register(moduleName string, raw json.RawMessage) error {
	if moduleName == "" {
		return fmt.Errorf("register module config: empty module name")
	}
	if len(raw) == 0 {
		return fmt.Errorf("register module config %s: empty config", moduleName)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.configs[moduleName]; exists {
		return fmt.Errorf("register module config %s: %w", moduleName, otogi.ErrConfigAlreadyRegistered)
	}

	r.configs[moduleName] = append(json.RawMessage(nil), raw...)

	return nil
}

// Resolve returns the raw JSON configuration for the named module.
func (r *ConfigRegistry) Resolve(moduleName string) (json.RawMessage, error) {
	if moduleName == "" {
		return nil, fmt.Errorf("resolve module config: empty module name")
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	raw, exists := r.configs[moduleName]
	if !exists {
		return nil, fmt.Errorf("resolve module config %s: %w", moduleName, otogi.ErrConfigNotFound)
	}

	return append(json.RawMessage(nil), raw...), nil
}
