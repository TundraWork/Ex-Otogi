package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseLogLevel(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    slog.Level
		wantErr bool
	}{
		{name: "debug", input: "debug", want: slog.LevelDebug},
		{name: "info", input: "info", want: slog.LevelInfo},
		{name: "warn", input: "warn", want: slog.LevelWarn},
		{name: "warning", input: "warning", want: slog.LevelWarn},
		{name: "error", input: "error", want: slog.LevelError},
		{name: "invalid", input: "trace", wantErr: true},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			got, err := parseLogLevel(testCase.input)
			if testCase.wantErr && err == nil {
				t.Fatal("expected error")
			}
			if !testCase.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if testCase.wantErr {
				return
			}
			if got != testCase.want {
				t.Fatalf("level = %v, want %v", got, testCase.want)
			}
		})
	}
}

func TestLoadConfigFromEnv(t *testing.T) {
	t.Setenv(envLogLevel, "debug")
	t.Setenv(envTelegramPublishTimeout, "5s")
	t.Setenv(envTelegramAuthTimeout, "2m")
	t.Setenv(envTelegramUpdateBuffer, "128")
	t.Setenv(envTelegramPhone, "+15551234567")
	t.Setenv(envTelegramSessionFile, "tmp/session.json")

	cfg, err := loadConfigFromEnv()
	if err != nil {
		t.Fatalf("load config failed: %v", err)
	}

	if cfg.logLevel != slog.LevelDebug {
		t.Fatalf("log level = %v, want %v", cfg.logLevel, slog.LevelDebug)
	}
	if cfg.telegramPublishTimeout != 5*time.Second {
		t.Fatalf("publish timeout = %s, want 5s", cfg.telegramPublishTimeout)
	}
	if cfg.telegramAuthTimeout != 2*time.Minute {
		t.Fatalf("auth timeout = %s, want 2m", cfg.telegramAuthTimeout)
	}
	if cfg.telegramUpdateBuffer != 128 {
		t.Fatalf("update buffer = %d, want 128", cfg.telegramUpdateBuffer)
	}
	if cfg.telegramPhone != "+15551234567" {
		t.Fatalf("telegram phone = %q, want +15551234567", cfg.telegramPhone)
	}
	if cfg.telegramSessionFile != "tmp/session.json" {
		t.Fatalf("telegram session file = %q, want tmp/session.json", cfg.telegramSessionFile)
	}
}

func TestLoadConfigFromEnvInvalid(t *testing.T) {
	tests := []struct {
		name   string
		key    string
		value  string
		hasErr bool
	}{
		{name: "invalid log level", key: envLogLevel, value: "trace", hasErr: true},
		{name: "invalid publish timeout", key: envTelegramPublishTimeout, value: "bad", hasErr: true},
		{name: "zero publish timeout", key: envTelegramPublishTimeout, value: "0s", hasErr: true},
		{name: "invalid auth timeout", key: envTelegramAuthTimeout, value: "bad", hasErr: true},
		{name: "invalid update buffer", key: envTelegramUpdateBuffer, value: "bad", hasErr: true},
		{name: "non-positive update buffer", key: envTelegramUpdateBuffer, value: "0", hasErr: true},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv(testCase.key, testCase.value)
			_, err := loadConfigFromEnv()
			if testCase.hasErr && err == nil {
				t.Fatal("expected error")
			}
			if !testCase.hasErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestLoadConfig(t *testing.T) {
	t.Run("loads all supported fields from config file", func(t *testing.T) {
		configPath := filepath.Join(t.TempDir(), "bot.json")
		if err := os.WriteFile(configPath, []byte(`{
			"log_level":"warn",
			"kernel":{
				"module_hook_timeout":"7s",
				"shutdown_timeout":"15s",
				"subscription_buffer":64,
				"subscription_workers":5
			},
			"telegram":{
				"publish_timeout":"11s",
				"update_buffer":222,
				"auth_timeout":"4m",
				"code_env":"CUSTOM_TELEGRAM_CODE",
				"bot_token_env":"CUSTOM_TELEGRAM_BOT_TOKEN",
				"phone":"+15550001111",
				"password":"secret",
				"session_file":"state/telegram/session.json"
			}
		}`), 0o600); err != nil {
			t.Fatalf("write config file: %v", err)
		}
		t.Setenv(envConfigFile, configPath)

		cfg, err := loadConfig()
		if err != nil {
			t.Fatalf("load config failed: %v", err)
		}

		if cfg.logLevel != slog.LevelWarn {
			t.Fatalf("log level = %v, want %v", cfg.logLevel, slog.LevelWarn)
		}
		if cfg.moduleHookTimeout != 7*time.Second {
			t.Fatalf("module hook timeout = %s, want 7s", cfg.moduleHookTimeout)
		}
		if cfg.shutdownTimeout != 15*time.Second {
			t.Fatalf("shutdown timeout = %s, want 15s", cfg.shutdownTimeout)
		}
		if cfg.subscriptionBuffer != 64 {
			t.Fatalf("subscription buffer = %d, want 64", cfg.subscriptionBuffer)
		}
		if cfg.subscriptionWorkers != 5 {
			t.Fatalf("subscription workers = %d, want 5", cfg.subscriptionWorkers)
		}
		if cfg.telegramPublishTimeout != 11*time.Second {
			t.Fatalf("telegram publish timeout = %s, want 11s", cfg.telegramPublishTimeout)
		}
		if cfg.telegramUpdateBuffer != 222 {
			t.Fatalf("telegram update buffer = %d, want 222", cfg.telegramUpdateBuffer)
		}
		if cfg.telegramAuthTimeout != 4*time.Minute {
			t.Fatalf("telegram auth timeout = %s, want 4m", cfg.telegramAuthTimeout)
		}
		if cfg.telegramCodeEnv != "CUSTOM_TELEGRAM_CODE" {
			t.Fatalf("telegram code env = %q, want CUSTOM_TELEGRAM_CODE", cfg.telegramCodeEnv)
		}
		if cfg.telegramBotTokenEnv != "CUSTOM_TELEGRAM_BOT_TOKEN" {
			t.Fatalf("telegram bot token env = %q, want CUSTOM_TELEGRAM_BOT_TOKEN", cfg.telegramBotTokenEnv)
		}
		if cfg.telegramPhone != "+15550001111" {
			t.Fatalf("telegram phone = %q, want +15550001111", cfg.telegramPhone)
		}
		if cfg.telegramPassword != "secret" {
			t.Fatalf("telegram password = %q, want secret", cfg.telegramPassword)
		}
		if cfg.telegramSessionFile != "state/telegram/session.json" {
			t.Fatalf("telegram session file = %q, want state/telegram/session.json", cfg.telegramSessionFile)
		}
	})

	t.Run("env overrides config file", func(t *testing.T) {
		configPath := filepath.Join(t.TempDir(), "bot.json")
		if err := os.WriteFile(configPath, []byte(`{
			"log_level":"error",
			"telegram":{
				"publish_timeout":"20s",
				"auth_timeout":"4m",
				"update_buffer":333,
				"phone":"+15550001111",
				"session_file":"state/telegram/session.json"
			}
		}`), 0o600); err != nil {
			t.Fatalf("write config file: %v", err)
		}
		t.Setenv(envConfigFile, configPath)
		t.Setenv(envLogLevel, "debug")
		t.Setenv(envTelegramPublishTimeout, "5s")
		t.Setenv(envTelegramAuthTimeout, "2m")
		t.Setenv(envTelegramUpdateBuffer, "128")
		t.Setenv(envTelegramPhone, "+15552223333")
		t.Setenv(envTelegramSessionFile, "overrides/session.json")

		cfg, err := loadConfig()
		if err != nil {
			t.Fatalf("load config failed: %v", err)
		}

		if cfg.logLevel != slog.LevelDebug {
			t.Fatalf("log level = %v, want %v", cfg.logLevel, slog.LevelDebug)
		}
		if cfg.telegramPublishTimeout != 5*time.Second {
			t.Fatalf("publish timeout = %s, want 5s", cfg.telegramPublishTimeout)
		}
		if cfg.telegramAuthTimeout != 2*time.Minute {
			t.Fatalf("auth timeout = %s, want 2m", cfg.telegramAuthTimeout)
		}
		if cfg.telegramUpdateBuffer != 128 {
			t.Fatalf("update buffer = %d, want 128", cfg.telegramUpdateBuffer)
		}
		if cfg.telegramPhone != "+15552223333" {
			t.Fatalf("telegram phone = %q, want +15552223333", cfg.telegramPhone)
		}
		if cfg.telegramSessionFile != "overrides/session.json" {
			t.Fatalf("telegram session file = %q, want overrides/session.json", cfg.telegramSessionFile)
		}
	})

	t.Run("invalid config values fail", func(t *testing.T) {
		tests := []struct {
			name       string
			fileJSON   string
			wantErrSub string
		}{
			{
				name:       "invalid log level",
				fileJSON:   `{"log_level":"trace"}`,
				wantErrSub: "parse log_level",
			},
			{
				name:       "invalid kernel timeout",
				fileJSON:   `{"kernel":{"module_hook_timeout":"bad"}}`,
				wantErrSub: "parse kernel.module_hook_timeout",
			},
			{
				name:       "non-positive kernel buffer",
				fileJSON:   `{"kernel":{"subscription_buffer":0}}`,
				wantErrSub: "parse kernel.subscription_buffer",
			},
			{
				name:       "invalid telegram timeout",
				fileJSON:   `{"telegram":{"auth_timeout":"bad"}}`,
				wantErrSub: "parse telegram.auth_timeout",
			},
			{
				name:       "non-positive telegram update buffer",
				fileJSON:   `{"telegram":{"update_buffer":0}}`,
				wantErrSub: "parse telegram.update_buffer",
			},
		}

		for _, testCase := range tests {
			testCase := testCase
			t.Run(testCase.name, func(t *testing.T) {
				configPath := filepath.Join(t.TempDir(), "bot.json")
				if err := os.WriteFile(configPath, []byte(testCase.fileJSON), 0o600); err != nil {
					t.Fatalf("write config file: %v", err)
				}
				t.Setenv(envConfigFile, configPath)

				_, err := loadConfig()
				if err == nil {
					t.Fatal("expected error")
				}
				if !strings.Contains(err.Error(), testCase.wantErrSub) {
					t.Fatalf("error = %v, want substring %q", err, testCase.wantErrSub)
				}
			})
		}
	})

	t.Run("missing explicit config file fails", func(t *testing.T) {
		t.Setenv(envConfigFile, filepath.Join(t.TempDir(), "missing.json"))
		if _, err := loadConfig(); err == nil {
			t.Fatal("expected error for missing config file")
		}
	})
}

func TestNewGotdSessionStorage(t *testing.T) {
	t.Run("creates parent directories and absolute path", func(t *testing.T) {
		sessionPath := filepath.Join(t.TempDir(), "nested", "telegram", "session.json")

		storage, err := newGotdSessionStorage(sessionPath)
		if err != nil {
			t.Fatalf("new gotd session storage failed: %v", err)
		}

		if !filepath.IsAbs(storage.Path) {
			t.Fatalf("session path = %q, want absolute path", storage.Path)
		}
		parent := filepath.Dir(storage.Path)
		info, err := os.Stat(parent)
		if err != nil {
			t.Fatalf("stat session parent dir: %v", err)
		}
		if !info.IsDir() {
			t.Fatalf("session parent %q is not a directory", parent)
		}
	})

	t.Run("empty path fails", func(t *testing.T) {
		if _, err := newGotdSessionStorage("   "); err == nil {
			t.Fatal("expected error for empty session path")
		}
	})
}
