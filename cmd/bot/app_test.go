package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	driverpkg "ex-otogi/internal/driver"
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

func mustBuiltinDriverRegistry(t *testing.T) *driverpkg.Registry {
	t.Helper()

	registry, err := driverpkg.NewBuiltinRegistry()
	if err != nil {
		t.Fatalf("new builtin registry failed: %v", err)
	}

	return registry
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
	t.Run("loads drivers and routing config", func(t *testing.T) {
		configPath := filepath.Join(t.TempDir(), "bot.json")
		writeConfigFile(t, configPath, `{
			"log_level":"warn",
			"kernel":{
				"module_hook_timeout":"7s",
				"shutdown_timeout":"15s",
				"subscription_buffer":64,
				"subscription_workers":5
			},
			"drivers":[
				{
					"name":"tg-main",
					"type":"telegram",
					"enabled":true,
					"config":{
						"app_id":123456,
						"app_hash":"sample_hash"
					}
				}
			],
			"routing":{
				"default":{
					"sources":[{"platform":"telegram","id":"tg-main"}],
					"sink":{"platform":"telegram","id":"tg-main"}
				}
			}
		}`)
		t.Setenv(envConfigFile, configPath)

		cfg, err := loadConfig(mustBuiltinDriverRegistry(t))
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
		if len(cfg.drivers) != 1 {
			t.Fatalf("drivers len = %d, want 1", len(cfg.drivers))
		}
		if cfg.drivers[0].Name != "tg-main" {
			t.Fatalf("driver name = %q, want tg-main", cfg.drivers[0].Name)
		}
		if cfg.routingDefault == nil || cfg.routingDefault.Sink == nil {
			t.Fatal("expected routing default sink")
		}
		if cfg.routingDefault.Sink.ID != "tg-main" {
			t.Fatalf("default sink id = %q, want tg-main", cfg.routingDefault.Sink.ID)
		}
	})

	t.Run("single-driver mode infers default route", func(t *testing.T) {
		configPath := filepath.Join(t.TempDir(), "bot.json")
		writeConfigFile(t, configPath, `{
			"drivers":[
				{
					"name":"tg-main",
					"type":"telegram",
					"config":{"app_id":123456,"app_hash":"sample_hash"}
				}
			]
		}`)
		t.Setenv(envConfigFile, configPath)

		cfg, err := loadConfig(mustBuiltinDriverRegistry(t))
		if err != nil {
			t.Fatalf("load config failed: %v", err)
		}
		if cfg.routingDefault == nil || cfg.routingDefault.Sink == nil {
			t.Fatal("expected inferred default sink route")
		}
		if cfg.routingDefault.Sink.ID != "tg-main" {
			t.Fatalf("default sink id = %q, want tg-main", cfg.routingDefault.Sink.ID)
		}
	})

	t.Run("loads fallback path bin/config/bot.json when no explicit path is set", func(t *testing.T) {
		workDir := t.TempDir()
		configPath := filepath.Join(workDir, "bin", "config", "bot.json")
		writeConfigFile(t, configPath, `{
			"drivers":[
				{"name":"tg-main","type":"telegram","config":{"app_id":1,"app_hash":"hash"}}
			]
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

		cfg, err := loadConfig(mustBuiltinDriverRegistry(t))
		if err != nil {
			t.Fatalf("load config failed: %v", err)
		}
		if len(cfg.drivers) != 1 || cfg.drivers[0].Name != "tg-main" {
			t.Fatalf("unexpected drivers: %+v", cfg.drivers)
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
				fileJSON:   `{"log_level":"trace","drivers":[{"name":"tg","type":"telegram","config":{"app_id":1,"app_hash":"hash"}}]}`,
				wantErrSub: "parse log_level",
			},
			{
				name:       "invalid kernel timeout",
				fileJSON:   `{"kernel":{"module_hook_timeout":"bad"},"drivers":[{"name":"tg","type":"telegram","config":{"app_id":1,"app_hash":"hash"}}]}`,
				wantErrSub: "parse kernel.module_hook_timeout",
			},
			{
				name:       "missing drivers",
				fileJSON:   `{}`,
				wantErrSub: "at least one enabled driver",
			},
			{
				name:       "legacy telegram section fails with missing drivers",
				fileJSON:   `{"telegram":{"app_id":1,"app_hash":"hash"}}`,
				wantErrSub: "at least one enabled driver",
			},
			{
				name:       "unsupported driver type",
				fileJSON:   `{"drivers":[{"name":"legacy","type":"discord","config":{"token":"x"}}]}`,
				wantErrSub: "unsupported type discord",
			},
		}

		for _, testCase := range tests {
			testCase := testCase
			t.Run(testCase.name, func(t *testing.T) {
				configPath := filepath.Join(t.TempDir(), "bot.json")
				writeConfigFile(t, configPath, testCase.fileJSON)
				t.Setenv(envConfigFile, configPath)

				_, err := loadConfig(mustBuiltinDriverRegistry(t))
				if err == nil {
					t.Fatal("expected error")
				}
				if !strings.Contains(err.Error(), testCase.wantErrSub) {
					t.Fatalf("error = %v, want substring %q", err, testCase.wantErrSub)
				}
			})
		}
	})

	t.Run("multi-driver requires default route unless all modules override", func(t *testing.T) {
		configPath := filepath.Join(t.TempDir(), "bot.json")
		writeConfigFile(t, configPath, `{
			"drivers":[
				{"name":"tg-main","type":"telegram","config":{"app_id":1,"app_hash":"hash"}},
				{"name":"tg-alt","type":"telegram","config":{"app_id":2,"app_hash":"hash2"}}
			]
		}`)
		t.Setenv(envConfigFile, configPath)

		_, err := loadConfig(mustBuiltinDriverRegistry(t))
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "routing.default is required") {
			t.Fatalf("error = %v, want routing.default error", err)
		}
	})

	t.Run("missing explicit config file fails", func(t *testing.T) {
		t.Setenv(envConfigFile, filepath.Join(t.TempDir(), "missing.json"))
		if _, err := loadConfig(mustBuiltinDriverRegistry(t)); err == nil {
			t.Fatal("expected error for missing config file")
		}
	})
}
