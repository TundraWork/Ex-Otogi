package kernel

import (
	"context"
	"fmt"
	"sort"

	"ex-otogi/pkg/otogi"
)

// kernelCommandCatalog exposes kernel command registrations through ServiceRegistry.
type kernelCommandCatalog struct {
	kernel *Kernel
}

// ListCommands returns all registered command entries sorted by command then module.
func (c *kernelCommandCatalog) ListCommands(ctx context.Context) ([]otogi.RegisteredCommand, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("list commands: %w", err)
	}
	if c == nil || c.kernel == nil {
		return nil, fmt.Errorf("list commands: nil catalog")
	}

	c.kernel.mu.RLock()
	commands := make([]otogi.RegisteredCommand, 0, len(c.kernel.commands))
	for _, registration := range c.kernel.commands {
		commands = append(commands, otogi.RegisteredCommand{
			ModuleName: registration.moduleName,
			Command:    cloneCommandSpec(registration.spec),
		})
	}
	c.kernel.mu.RUnlock()

	sort.Slice(commands, func(i, j int) bool {
		left := fmt.Sprintf("%s%s", commands[i].Command.Prefix, commands[i].Command.Name)
		right := fmt.Sprintf("%s%s", commands[j].Command.Prefix, commands[j].Command.Name)
		if left == right {
			return commands[i].ModuleName < commands[j].ModuleName
		}
		return left < right
	})

	return commands, nil
}

var _ otogi.CommandCatalog = (*kernelCommandCatalog)(nil)
