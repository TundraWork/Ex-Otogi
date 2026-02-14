package kernel

import (
	"fmt"
	"sync"

	"ex-otogi/pkg/otogi"
)

// ServiceRegistry is the default in-memory service registry implementation.
type ServiceRegistry struct {
	mu       sync.RWMutex
	services map[string]any
}

// NewServiceRegistry creates an empty service registry.
func NewServiceRegistry() *ServiceRegistry {
	return &ServiceRegistry{
		services: make(map[string]any),
	}
}

// Register registers a named service singleton.
func (r *ServiceRegistry) Register(name string, service any) error {
	if name == "" {
		return fmt.Errorf("register service: empty name")
	}
	if service == nil {
		return fmt.Errorf("register service %s: nil service", name)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.services[name]; exists {
		return fmt.Errorf("register service %s: %w", name, otogi.ErrServiceAlreadyRegistered)
	}

	r.services[name] = service

	return nil
}

// Resolve returns a registered named service.
func (r *ServiceRegistry) Resolve(name string) (any, error) {
	if name == "" {
		return nil, fmt.Errorf("resolve service: empty name")
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	service, exists := r.services[name]
	if !exists {
		return nil, fmt.Errorf("resolve service %s: %w", name, otogi.ErrServiceNotFound)
	}

	return service, nil
}
