package telegram

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/gotd/td/session"
	gotdtelegram "github.com/gotd/td/telegram"
)

// SessionConfig identifies a Telegram application and a session file.
type SessionConfig struct {
	// AppID and AppHash identify the Telegram application.
	AppID   int
	AppHash string
	// SessionPath is the gotd session file.
	SessionPath string
}

func (c SessionConfig) client() (*gotdtelegram.Client, error) {
	if err := os.MkdirAll(filepath.Dir(c.SessionPath), 0o700); err != nil {
		return nil, fmt.Errorf("telegram: create session directory: %w", err)
	}
	return gotdtelegram.NewClient(c.AppID, c.AppHash, gotdtelegram.Options{SessionStorage: &session.FileStorage{Path: c.SessionPath}}), nil
}

// CheckSession connects with an existing session and confirms it is logged
// in, without prompting.
func CheckSession(ctx context.Context, cfg SessionConfig) error {
	c, err := cfg.client()
	if err != nil {
		return err
	}
	return c.Run(ctx, func(ctx context.Context) error {
		status, err := c.Auth().Status(ctx)
		if err != nil {
			return fmt.Errorf("telegram: check login: %w", err)
		}
		if !status.Authorized {
			return errors.New("telegram: session is not logged in")
		}
		return nil
	})
}
