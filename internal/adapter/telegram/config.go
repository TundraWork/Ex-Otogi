package telegram

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	telegramstore "github.com/TundraWork/Ex-Otogi/internal/adapter/telegram/store"
	gotdtelegram "github.com/gotd/td/telegram"
)

// ErrInvalidConfig reports a missing or invalid Telegram driver setting.
var ErrInvalidConfig = errors.New("telegram: invalid configuration")

// StoreConfig is the durable store configuration owned by the driver.
type StoreConfig = telegramstore.Config

// DriverStore is the narrow lifecycle and readiness view consumed by Driver.
type DriverStore interface {
	// RecoveryReady reports durable recovery readiness.
	RecoveryReady(context.Context) (bool, []telegramstore.RecoveryRecord, error)
	// Close releases the durable store.
	Close() error
}

// OpenStoreFunc validates, opens, and account-binds the driver store.
type OpenStoreFunc func(context.Context, StoreConfig) (DriverStore, error)

// ConfigureClientOptionsFunc installs update capture before constructing the one-shot client.
type ConfigureClientOptionsFunc func(*ClientOptions) error

// ManagerRunner owns the gotd update manager until cancellation.
type ManagerRunner func(context.Context, ClientSession, DriverStore, func()) error

// ResynchronizerRunner performs durable startup reconciliation.
type ResynchronizerRunner func(context.Context, ClientSession, DriverStore) error

// DispatcherRunner claims durable inbox work until cancellation.
type DispatcherRunner func(context.Context, DriverStore, func()) error

// DispatcherDrainer stops new claims and waits for accepted work under ctx.
type DispatcherDrainer func(context.Context) error

// Config contains immutable Telegram client settings and consumer-owned lifecycle seams.
type Config struct {
	// AppID is the Telegram application identifier.
	AppID int
	// AppHash is the Telegram application secret.
	AppHash string
	// AccountKey is the immutable deployment account identity.
	AccountKey string
	// SessionPath is the private local gotd session file.
	SessionPath string
	// DatabasePath is the private local SQLite state file.
	DatabasePath string
	// Device describes the Telegram client device identity.
	Device gotdtelegram.DeviceConfig
	// Authenticator supplies interactive authorization credentials.
	Authenticator Authenticator
	// ClientFactory constructs the one-shot Telegram client.
	ClientFactory ClientFactory
	// ConfigureClientOptions installs update capture.
	ConfigureClientOptions ConfigureClientOptionsFunc
	// OpenStore opens and account-binds durable state.
	OpenStore OpenStoreFunc
	// RunManager owns update manager execution.
	RunManager ManagerRunner
	// RunResynchronizer performs durable startup recovery.
	RunResynchronizer ResynchronizerRunner
	// RunDispatcher owns inbox dispatch.
	RunDispatcher DispatcherRunner
	// DrainDispatcher gracefully stops new claims.
	DrainDispatcher DispatcherDrainer
}

// Validate rejects incomplete settings without exposing secret values.
func (c Config) Validate() error {
	invalid := func(field string) error { return fmt.Errorf("%w: %s", ErrInvalidConfig, field) }
	if c.AppID <= 0 {
		return invalid("app ID")
	}
	if strings.TrimSpace(c.AppHash) == "" {
		return invalid("app hash")
	}
	if strings.TrimSpace(c.AccountKey) == "" || len(c.AccountKey) > 128 {
		return invalid("account key")
	}
	if !filepath.IsAbs(c.SessionPath) || filepath.Clean(c.SessionPath) != c.SessionPath {
		return invalid("session path")
	}
	if !filepath.IsAbs(c.DatabasePath) || filepath.Clean(c.DatabasePath) != c.DatabasePath || c.DatabasePath == c.SessionPath {
		return invalid("database path")
	}
	if strings.TrimSpace(c.Device.DeviceModel) == "" || strings.TrimSpace(c.Device.SystemVersion) == "" || strings.TrimSpace(c.Device.AppVersion) == "" || strings.TrimSpace(c.Device.LangCode) == "" || strings.TrimSpace(c.Device.SystemLangCode) == "" {
		return invalid("device identity")
	}
	if c.Authenticator == nil {
		return invalid("authenticator")
	}
	if c.ClientFactory == nil {
		return invalid("client factory")
	}
	if c.ConfigureClientOptions == nil {
		return invalid("client options configurator")
	}
	if c.OpenStore == nil {
		return invalid("store opener")
	}
	if c.RunManager == nil {
		return invalid("manager runner")
	}
	if c.RunResynchronizer == nil {
		return invalid("resynchronizer runner")
	}
	if c.RunDispatcher == nil {
		return invalid("dispatcher runner")
	}
	if c.DrainDispatcher == nil {
		return invalid("dispatcher drainer")
	}
	return nil
}
