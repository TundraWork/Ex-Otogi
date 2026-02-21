package driver

import (
	"context"
	"fmt"
	"log/slog"

	"ex-otogi/internal/driver/telegram"
)

// NewBuiltinRegistry constructs the runtime registry with all built-in drivers.
func NewBuiltinRegistry() (*Registry, error) {
	return NewRegistry([]Descriptor{
		{
			Type:     telegram.DriverType,
			Platform: telegram.DriverPlatform,
			Builder: func(
				_ context.Context,
				definition Definition,
				builderLogger *slog.Logger,
			) (Runtime, error) {
				source, runtimeDriver, sinkDispatcher, err := telegram.BuildRuntimeFromConfig(
					definition.Name,
					builderLogger,
					definition.Config,
				)
				if err != nil {
					return Runtime{}, fmt.Errorf("build telegram runtime from config: %w", err)
				}

				return Runtime{
					Source:         source,
					Driver:         runtimeDriver,
					SinkDispatcher: sinkDispatcher,
				}, nil
			},
		},
	})
}
