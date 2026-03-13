package telegram

import "ex-otogi/pkg/otogi/platform"

const (
	// DriverType is the configured driver type token for the Telegram runtime.
	DriverType = "telegram"
	// DriverPlatform is the Otogi platform produced by the Telegram runtime.
	DriverPlatform platform.Platform = platform.PlatformTelegram
)
