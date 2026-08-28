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
	RecoveryReady(context.Context) (bool, []telegramstore.RecoveryRecord, error)
	Close() error
}

// OpenStoreFunc validates, opens, and account-binds the driver store.
type OpenStoreFunc func(context.Context, StoreConfig) (DriverStore, error)

// ConfigureClientOptions installs update capture before constructing the one-shot client.
type ConfigureClientOptionsFunc func(*ClientOptions) error

// ManagerRunner owns the gotd update manager until cancellation.
type ManagerRunner func(context.Context, ClientSession, DriverStore, func()) error

// ResynchronizerRunner performs durable startup reconciliation.
type ResynchronizerRunner func(context.Context, ClientSession, DriverStore) error

// DispatcherRunner claims durable inbox work until cancellation.
type DispatcherRunner func(context.Context, DriverStore) error

// DispatcherDrainer stops new claims and waits for accepted work under ctx.
type DispatcherDrainer func(context.Context) error

// Config contains immutable Telegram client settings and consumer-owned lifecycle seams.
type Config struct {
	AppID                  int
	AppHash                string
	AccountKey             string
	SessionPath            string
	DatabasePath           string
	Device                 gotdtelegram.DeviceConfig
	Authenticator          Authenticator
	ClientFactory          ClientFactory
	ConfigureClientOptions ConfigureClientOptionsFunc
	OpenStore              OpenStoreFunc
	RunManager             ManagerRunner
	RunResynchronizer      ResynchronizerRunner
	RunDispatcher          DispatcherRunner
	DrainDispatcher        DispatcherDrainer
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
