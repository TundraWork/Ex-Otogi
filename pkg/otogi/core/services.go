package core

import (
	"fmt"
)

// ServiceRegistry provides runtime dependency injection to modules and drivers.
//
// Stable service names in pkg/otogi define the standard cross-package contract;
// modules should resolve dependencies by those names instead of depending on
// concrete driver or kernel types.
type ServiceRegistry interface {
	// Register binds a singleton service value to a stable name.
	Register(name string, service any) error
	// Resolve returns a registered service by name.
	Resolve(name string) (any, error)
}

// ResolveAs resolves a service and casts it to the requested type.
func ResolveAs[T any](registry ServiceRegistry, name string) (T, error) {
	var zero T

	service, err := registry.Resolve(name)
	if err != nil {
		return zero, fmt.Errorf("resolve service %s: %w", name, err)
	}

	typed, ok := service.(T)
	if !ok {
		return zero, fmt.Errorf("resolve service %s: type assertion failed", name)
	}

	return typed, nil
}
