// Command telegram-smoke runs the explicitly opted-in Telegram connectivity smoke.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/telegram"
)

type liveConfig struct {
	AppID       int
	AppHash     string
	SessionPath string
}

func loadConfig(getenv func(string) string) (liveConfig, error) {
	required := []string{"OTOGI_TELEGRAM_APP_ID", "OTOGI_TELEGRAM_APP_HASH", "OTOGI_TELEGRAM_SESSION_PATH"}
	values := make(map[string]string, len(required))
	for _, key := range required {
		value := strings.TrimSpace(getenv(key))
		if value == "" {
			return liveConfig{}, fmt.Errorf("telegram smoke: required environment variable %s is missing", key)
		}
		values[key] = value
	}
	appID, err := strconv.Atoi(values[required[0]])
	if err != nil || appID <= 0 {
		return liveConfig{}, errors.New("telegram smoke: OTOGI_TELEGRAM_APP_ID must be a positive integer")
	}
	return liveConfig{AppID: appID, AppHash: values[required[1]], SessionPath: values[required[2]]}, nil
}

func run(ctx context.Context, getenv func(string) string, output io.Writer, live func(context.Context, liveConfig) error) error {
	cfg, err := loadConfig(getenv)
	if err != nil {
		return err
	}
	if err = live(ctx, cfg); err != nil {
		return err
	}
	_, err = fmt.Fprintln(output, "telegram smoke: session connectivity verified")
	return err
}

func main() { os.Exit(mainExit()) }

func mainExit() int {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Getenv, os.Stdout, liveSmoke); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func liveSmoke(ctx context.Context, cfg liveConfig) error {
	return telegram.RunLiveSmoke(ctx, telegram.LiveSmokeConfig{
		AppID: cfg.AppID, AppHash: cfg.AppHash, SessionPath: cfg.SessionPath,
	})
}
