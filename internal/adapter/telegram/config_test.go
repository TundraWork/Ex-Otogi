package telegram

import (
	"context"
	"errors"
	gotdtelegram "github.com/gotd/td/telegram"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestConfigValidateRejectsInvalidRequiredValues(t *testing.T) {
	valid := validDriverConfig(t)
	tests := []struct {
		name string
		edit func(*Config)
	}{
		{"app id", func(c *Config) { c.AppID = 0 }},
		{"app hash", func(c *Config) { c.AppHash = "" }},
		{"account key", func(c *Config) { c.AccountKey = "" }},
		{"session path", func(c *Config) { c.SessionPath = "relative" }},
		{"database path", func(c *Config) { c.DatabasePath = "relative" }},
		{"device model", func(c *Config) { c.Device.DeviceModel = "" }},
		{"system version", func(c *Config) { c.Device.SystemVersion = "" }},
		{"app version", func(c *Config) { c.Device.AppVersion = "" }},
		{"language", func(c *Config) { c.Device.LangCode = "" }},
		{"system language", func(c *Config) { c.Device.SystemLangCode = "" }},
		{"authenticator", func(c *Config) { c.Authenticator = nil }},
		{"client factory", func(c *Config) { c.ClientFactory = nil }},
		{"options configurator", func(c *Config) { c.ConfigureClientOptions = nil }},
		{"store opener", func(c *Config) { c.OpenStore = nil }},
		{"manager", func(c *Config) { c.RunManager = nil }},
		{"resynchronizer", func(c *Config) { c.RunResynchronizer = nil }},
		{"dispatcher", func(c *Config) { c.RunDispatcher = nil }},
		{"dispatcher drainer", func(c *Config) { c.DrainDispatcher = nil }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := valid
			tt.edit(&cfg)
			if err := cfg.Validate(); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("Validate() error = %v, want ErrInvalidConfig", err)
			}
		})
	}
}

func TestConfigValidateAcceptsCompleteDependencies(t *testing.T) {
	if err := validDriverConfig(t).Validate(); err != nil {
		t.Fatal(err)
	}
}

func validDriverConfig(t *testing.T) Config {
	t.Helper()
	dir := t.TempDir()
	if runtime.GOOS != "windows" {
		if err := os.Chmod(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	auth := AuthenticatorFuncs{PhoneFunc: func(context.Context) (string, error) { return "+15550001", nil }, CodeFunc: func(context.Context, CodeChallenge) (string, error) { return "12345", nil }, PasswordFunc: func(context.Context) (string, error) { return "password", nil }}
	return Config{
		AppID: 1, AppHash: "hash", AccountKey: "account",
		SessionPath: filepath.Join(dir, "session"), DatabasePath: filepath.Join(dir, "state.db"),
		Device:        gotdtelegram.DeviceConfig{DeviceModel: "model", SystemVersion: "system", AppVersion: "app", LangCode: "en", SystemLangCode: "en"},
		Authenticator: auth, ClientFactory: fakeClientFactory{}, ConfigureClientOptions: func(*ClientOptions) error { return nil },
		OpenStore: func(context.Context, StoreConfig) (DriverStore, error) { return &fakeDriverStore{ready: true}, nil },
		RunManager: func(ctx context.Context, _ ClientSession, _ DriverStore, _ int64, ready func()) error {
			ready()
			<-ctx.Done()
			return ctx.Err()
		},
		RunResynchronizer: func(context.Context, ClientSession, DriverStore) error { return nil },
		RunDispatcher:     func(ctx context.Context, _ DriverStore, ready func()) error { ready(); <-ctx.Done(); return ctx.Err() },
		DrainDispatcher:   func(context.Context) error { return nil },
	}
}
