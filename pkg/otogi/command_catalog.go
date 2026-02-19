package otogi

import (
	"context"
)

// ServiceCommandCatalog is the canonical service registry key for command discovery.
const ServiceCommandCatalog = "otogi.command_catalog"

// RegisteredCommand describes one runtime command registration entry.
type RegisteredCommand struct {
	// ModuleName identifies which module registered this command.
	ModuleName string
	// Command is the registered command specification.
	Command CommandSpec
}

// CommandCatalog provides read access to registered command specifications.
//
// Implementations must be concurrency-safe because modules can list commands
// from multiple workers at the same time.
type CommandCatalog interface {
	// ListCommands returns all currently registered command entries.
	//
	// Returned entries should be a defensive copy so caller mutation does not
	// affect runtime command registration state.
	ListCommands(ctx context.Context) ([]RegisteredCommand, error)
}
