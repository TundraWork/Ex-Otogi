package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeConfigFile(t *testing.T, path string, contents string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write config file: %v", err)
	}
}

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

func TestLoadConfig(t *testing.T) {
	t.Run("loads all supported fields from config file", func(t *testing.T) {
		configPath := filepath.Join(t.TempDir(), "bot.json")
		writeConfigFile(t, configPath, `{
			"log_level":"warn",
			"kernel":{
				"module_hook_timeout":"7s",
				"shutdown_timeout":"15s",
				"subscription_buffer":64,
				"subscription_workers":5
			},
			"telegram":{
				"app_id":123456,
				"app_hash":"sample_hash",
				"publish_timeout":"11s",
				"update_buffer":222,
				"auth_timeout":"4m",
				"code":"998877",
				"bot_token":"123:abc",
				"phone":"+15550001111",
				"password":"secret",
				"session_file":"state/telegram/session.json"
			}
		}`)
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
		if cfg.telegramAppID != 123456 {
			t.Fatalf("telegram app id = %d, want 123456", cfg.telegramAppID)
		}
		if cfg.telegramAppHash != "sample_hash" {
			t.Fatalf("telegram app hash = %q, want sample_hash", cfg.telegramAppHash)
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
		if cfg.telegramCode != "998877" {
			t.Fatalf("telegram code = %q, want 998877", cfg.telegramCode)
		}
		if cfg.telegramBotToken != "123:abc" {
			t.Fatalf("telegram bot token = %q, want 123:abc", cfg.telegramBotToken)
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

	t.Run("loads fallback path bin/config/bot.json when no explicit path is set", func(t *testing.T) {
		workDir := t.TempDir()
		configPath := filepath.Join(workDir, "bin", "config", "bot.json")
		writeConfigFile(t, configPath, `{
			"telegram":{
				"app_id":777,
				"app_hash":"fallback_hash"
			}
		}`)

		currentDir, err := os.Getwd()
		if err != nil {
			t.Fatalf("get working directory: %v", err)
		}
		if err := os.Chdir(workDir); err != nil {
			t.Fatalf("chdir to temp work dir: %v", err)
		}
		t.Cleanup(func() {
			if err := os.Chdir(currentDir); err != nil {
				t.Fatalf("restore working directory: %v", err)
			}
		})
		t.Setenv(envConfigFile, "")

		cfg, err := loadConfig()
		if err != nil {
			t.Fatalf("load config failed: %v", err)
		}

		if cfg.telegramAppID != 777 {
			t.Fatalf("telegram app id = %d, want 777", cfg.telegramAppID)
		}
		if cfg.telegramAppHash != "fallback_hash" {
			t.Fatalf("telegram app hash = %q, want fallback_hash", cfg.telegramAppHash)
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
				fileJSON:   `{"log_level":"trace","telegram":{"app_id":1,"app_hash":"hash"}}`,
				wantErrSub: "parse log_level",
			},
			{
				name:       "invalid kernel timeout",
				fileJSON:   `{"kernel":{"module_hook_timeout":"bad"},"telegram":{"app_id":1,"app_hash":"hash"}}`,
				wantErrSub: "parse kernel.module_hook_timeout",
			},
			{
				name:       "non-positive kernel buffer",
				fileJSON:   `{"kernel":{"subscription_buffer":0},"telegram":{"app_id":1,"app_hash":"hash"}}`,
				wantErrSub: "parse kernel.subscription_buffer",
			},
			{
				name:       "invalid telegram timeout",
				fileJSON:   `{"telegram":{"app_id":1,"app_hash":"hash","auth_timeout":"bad"}}`,
				wantErrSub: "parse telegram.auth_timeout",
			},
			{
				name:       "non-positive telegram update buffer",
				fileJSON:   `{"telegram":{"app_id":1,"app_hash":"hash","update_buffer":0}}`,
				wantErrSub: "parse telegram.update_buffer",
			},
			{
				name:       "non-positive telegram app id",
				fileJSON:   `{"telegram":{"app_id":0,"app_hash":"hash"}}`,
				wantErrSub: "parse telegram.app_id",
			},
		}

		for _, testCase := range tests {
			testCase := testCase
			t.Run(testCase.name, func(t *testing.T) {
				configPath := filepath.Join(t.TempDir(), "bot.json")
				writeConfigFile(t, configPath, testCase.fileJSON)
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

	t.Run("missing required app credentials fail", func(t *testing.T) {
		tests := []struct {
			name       string
			fileJSON   string
			wantErrSub string
		}{
			{
				name:       "app id missing",
				fileJSON:   `{"telegram":{"app_hash":"hash"}}`,
				wantErrSub: "telegram.app_id must be > 0",
			},
			{
				name:       "app hash missing",
				fileJSON:   `{"telegram":{"app_id":1}}`,
				wantErrSub: "telegram.app_hash is required",
			},
		}

		for _, testCase := range tests {
			testCase := testCase
			t.Run(testCase.name, func(t *testing.T) {
				configPath := filepath.Join(t.TempDir(), "bot.json")
				writeConfigFile(t, configPath, testCase.fileJSON)
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
