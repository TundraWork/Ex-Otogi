package otogi

import (
	"context"
	"fmt"
)

// ModuleDescriptor is an immutable declaration of module identity and consumed event kinds.
type ModuleDescriptor struct {
	id       ModuleID
	consumes []EventKind
}

// NewModuleDescriptor validates identity and a nonempty unique set of consumed kinds and copies the slice.
func NewModuleDescriptor(id ModuleID, consumes []EventKind) (ModuleDescriptor, error) {
	if id.value == "" || len(consumes) == 0 {
		return ModuleDescriptor{}, fmt.Errorf("module descriptor: %w", ErrInvalid)
	}
	owned := make([]EventKind, len(consumes))
	seen := make(map[EventKind]struct{}, len(consumes))
	for i, kind := range consumes {
		if kind.value == "" {
			return ModuleDescriptor{}, fmt.Errorf("module descriptor: %w", ErrInvalid)
		}
		if _, exists := seen[kind]; exists {
			return ModuleDescriptor{}, fmt.Errorf("module descriptor: %w", ErrInvalid)
		}
		seen[kind] = struct{}{}
		owned[i] = kind
	}
	return ModuleDescriptor{id: id, consumes: owned}, nil
}

// ID returns the module identity.
func (d ModuleDescriptor) ID() ModuleID { return d.id }

// Consumes returns an independent copy of the consumed event kinds.
func (d ModuleDescriptor) Consumes() []EventKind { return append([]EventKind(nil), d.consumes...) }

// Module handles events according to its immutable descriptor.
type Module interface {
	// Descriptor returns the module's immutable identity and routing declaration.
	Descriptor() ModuleDescriptor
	// Handle processes one event and returns exactly one terminal result.
	Handle(context.Context, Event, Invocation) Result
}
