package telegram

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gotd/td/session"
	gotdtelegram "github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
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

// Login logs the session in, prompting through a when it is not logged in
// already, and returns a description of the account.
func Login(ctx context.Context, cfg SessionConfig, a Authenticator) (string, error) {
	c, err := cfg.client()
	if err != nil {
		return "", err
	}
	var account string
	err = c.Run(ctx, func(ctx context.Context) error {
		if err := c.Auth().IfNecessary(ctx, auth.NewFlow(userAuthenticator{a}, auth.SendCodeOptions{})); err != nil {
			return fmt.Errorf("telegram: log in: %w", err)
		}
		self, err := c.Self(ctx)
		if err != nil {
			return fmt.Errorf("telegram: load own account: %w", err)
		}
		account = strings.TrimSpace(fmt.Sprintf("%s %s (@%s, id %d)", self.FirstName, self.LastName, self.Username, self.ID))
		return nil
	})
	return account, err
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
