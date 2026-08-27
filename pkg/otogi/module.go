package otogi

import (
	"context"
	"fmt"
)

// ModuleDescriptor is an immutable declaration of module identity and consumed event actions.
type ModuleDescriptor struct {
	id       ModuleID
	consumes []EventAction
}

// NewModuleDescriptor validates identity and a nonempty unique set of consumed actions and copies the slice.
func NewModuleDescriptor(id ModuleID, consumes []EventAction) (ModuleDescriptor, error) {
	if id.value == "" || len(consumes) == 0 {
		return ModuleDescriptor{}, fmt.Errorf("module descriptor: %w", ErrInvalid)
	}
	owned := make([]EventAction, len(consumes))
	seen := make(map[EventAction]struct{}, len(consumes))
	for i, action := range consumes {
		if action.value == "" {
			return ModuleDescriptor{}, fmt.Errorf("module descriptor: %w", ErrInvalid)
		}
		if _, exists := seen[action]; exists {
			return ModuleDescriptor{}, fmt.Errorf("module descriptor: %w", ErrInvalid)
		}
		seen[action] = struct{}{}
		owned[i] = action
	}
	return ModuleDescriptor{id: id, consumes: owned}, nil
}

// ID returns the module identity.
func (d ModuleDescriptor) ID() ModuleID { return d.id }

// Consumes returns an independent copy of the consumed event actions.
func (d ModuleDescriptor) Consumes() []EventAction { return append([]EventAction(nil), d.consumes...) }

// Module handles events according to its immutable descriptor.
type Module interface {
	// Descriptor returns the module's immutable identity and routing declaration.
	Descriptor() ModuleDescriptor
	// Handle processes one event and returns exactly one terminal result.
	Handle(context.Context, Event, Invocation) Result
}
