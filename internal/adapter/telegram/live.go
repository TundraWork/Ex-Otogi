package telegram

import (
	"context"
	"errors"
	"fmt"
	"strings"

	gotdtelegram "github.com/gotd/td/telegram"
)

// LiveSmokeConfig contains the explicit, environment-sourced inputs for the opt-in Telegram smoke.
type LiveSmokeConfig struct {
	// AppID is the Telegram application identifier.
	AppID int
	// AppHash is the Telegram application secret.
	AppHash string
	// AccountKey is the expected deployment account identity.
	AccountKey string
	// SessionPath is an already-authorized private gotd session file.
	SessionPath string
	// DatabasePath is the distinct private SQLite path reserved for the full driver.
	DatabasePath string
}

// RunLiveSmoke verifies an existing session against Telegram without prompting for credentials.
func RunLiveSmoke(ctx context.Context, cfg LiveSmokeConfig) error {
	if cfg.AppID <= 0 || strings.TrimSpace(cfg.AppHash) == "" || strings.TrimSpace(cfg.AccountKey) == "" {
		return ErrInvalidConfig
	}
	if cfg.SessionPath == cfg.DatabasePath || strings.TrimSpace(cfg.DatabasePath) == "" {
		return ErrInvalidConfig
	}
	storage, err := NewSessionStorage(cfg.SessionPath)
	if err != nil {
		return err
	}
	client := gotdtelegram.NewClient(cfg.AppID, cfg.AppHash, gotdtelegram.Options{SessionStorage: storage})
	return client.Run(ctx, func(ctx context.Context) error {
		status, err := client.Auth().Status(ctx)
		if err != nil {
			return fmt.Errorf("telegram smoke: authorization status: %w", err)
		}
		if !status.Authorized {
			return errors.New("telegram smoke: session is not authorized")
		}
		if _, err = client.Self(ctx); err != nil {
			return fmt.Errorf("telegram smoke: load authorized account: %w", err)
		}
		return nil
	})
}
