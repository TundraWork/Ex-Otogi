package driver

import (
	"testing"

	"ex-otogi/internal/driver/telegram"
)

func TestNewBuiltinRegistryIncludesTelegram(t *testing.T) {
	t.Parallel()

	registry, err := NewBuiltinRegistry()
	if err != nil {
		t.Fatalf("new builtin registry failed: %v", err)
	}

	platform, err := registry.PlatformForType(telegram.DriverType)
	if err != nil {
		t.Fatalf("platform for telegram type failed: %v", err)
	}
	if platform != telegram.DriverPlatform {
		t.Fatalf("platform = %s, want %s", platform, telegram.DriverPlatform)
	}
}
