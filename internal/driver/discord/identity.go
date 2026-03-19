package discord

import "ex-otogi/pkg/otogi/platform"

const (
	// DriverType is the configured driver type token for the Discord runtime.
	DriverType = "discord"
	// DriverPlatform is the Otogi platform produced by the Discord runtime.
	DriverPlatform platform.Platform = platform.PlatformDiscord
)
