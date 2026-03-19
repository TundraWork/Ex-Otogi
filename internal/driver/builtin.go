package driver

import (
	"context"
	"fmt"
	"log/slog"

	"ex-otogi/internal/driver/discord"
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
				source, runtimeDriver, mediaDownloader, outbound, err := telegram.BuildRuntimeFromConfig(
					definition.Name,
					builderLogger,
					definition.Config,
				)
				if err != nil {
					return Runtime{}, fmt.Errorf("build telegram runtime from config: %w", err)
				}

				return Runtime{
					Source:               source,
					Driver:               runtimeDriver,
					MediaDownloader:      mediaDownloader,
					SinkDispatcher:       outbound,
					ModerationDispatcher: outbound,
				}, nil
			},
		},
		{
			Type:     discord.DriverType,
			Platform: discord.DriverPlatform,
			Builder: func(
				_ context.Context,
				definition Definition,
				builderLogger *slog.Logger,
			) (Runtime, error) {
				source, runtimeDriver, mediaDownloader, sink, moderation, err := discord.BuildRuntimeFromConfig(
					definition.Name,
					builderLogger,
					definition.Config,
				)
				if err != nil {
					return Runtime{}, fmt.Errorf("build discord runtime from config: %w", err)
				}

				return Runtime{
					Source:               source,
					Driver:               runtimeDriver,
					MediaDownloader:      mediaDownloader,
					SinkDispatcher:       sink,
					ModerationDispatcher: moderation,
				}, nil
			},
		},
	})
}
