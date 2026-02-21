package telegram

import "ex-otogi/pkg/otogi"

const (
	// DriverType is the configured driver type token for the Telegram runtime.
	DriverType = "telegram"
	// DriverPlatform is the neutral otogi platform produced by the Telegram runtime.
	DriverPlatform otogi.Platform = otogi.PlatformTelegram
)
