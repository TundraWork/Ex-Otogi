package core

import (
	"encoding/json"
	"fmt"
)

// ConfigRegistry stores and retrieves per-module raw JSON configuration.
//
// Modules read their own configuration during OnRegister via
// ModuleRuntime.Config(). Configuration is populated before module
// registration by the application wiring layer. The registry is intentionally
// raw so pkg/otogi remains the stable configuration boundary while each module
// owns its typed schema.
type ConfigRegistry interface {
	// Register stores raw JSON configuration for the named module.
	Register(moduleName string, raw json.RawMessage) error
	// Resolve returns the raw JSON configuration for the named module.
	// Returns ErrConfigNotFound when no configuration has been registered.
	Resolve(moduleName string) (json.RawMessage, error)
}

// ParseModuleConfig resolves raw JSON for moduleName and unmarshals it into T.
func ParseModuleConfig[T any](registry ConfigRegistry, moduleName string) (T, error) {
	var zero T

	if registry == nil {
		return zero, fmt.Errorf("parse module config %s: nil config registry", moduleName)
	}

	raw, err := registry.Resolve(moduleName)
	if err != nil {
		return zero, fmt.Errorf("parse module config %s: %w", moduleName, err)
	}

	var cfg T
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return zero, fmt.Errorf("parse module config %s: unmarshal: %w", moduleName, err)
	}

	return cfg, nil
}
