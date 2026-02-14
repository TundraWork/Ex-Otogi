package otogi

import (
	"fmt"
)

// ServiceRegistry provides runtime dependency injection to modules and drivers.
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
