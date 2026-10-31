package main

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "otogi.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadConfigDefaultsAndExpansion(t *testing.T) {
	t.Setenv("TEST_APP_HASH", "secret")
	cfg, err := loadConfig(writeConfig(t, `
data_dir: /srv/otogi
telegram:
  - account: main
    app_id: 1
    app_hash: ${TEST_APP_HASH}   # ${UNSET_IN_COMMENT} is not expanded
modules:
  echo:
`))
	if err != nil {
		t.Fatal(err)
	}
	a := cfg.Telegram[0]
	if a.AppHash != "secret" || a.Session != "/srv/otogi/telegram/main/session.json" || a.Database != "/srv/otogi/telegram/main/state.sqlite" {
		t.Fatalf("account = %+v", a)
	}
	if cfg.ShutdownTimeout != 30*time.Second || cfg.Log.Level != "info" || cfg.Log.Format != "json" {
		t.Fatalf("defaults = %+v", cfg)
	}
	if _, err := buildModules(cfg.Modules, moduleEnv{}); err != nil {
		t.Fatal(err)
	}
}

func TestEnvironmentValuesTakeTheirFieldType(t *testing.T) {
	t.Setenv("TEST_APP_ID", "12345")
	cfg, err := loadConfig(writeConfig(t, "telegram:\n  - account: a\n    app_id: ${TEST_APP_ID}\n    app_hash: \"${TEST_APP_ID}\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Telegram[0].AppID != 12345 || cfg.Telegram[0].AppHash != "12345" {
		t.Fatalf("account = %+v", cfg.Telegram[0])
	}
}

func TestTypeErrorsReportTheOriginalLine(t *testing.T) {
	_, err := loadConfig(writeConfig(t, "# comment\n\n\nshutdown_timeout: soon\n"))
	if err == nil || !strings.Contains(err.Error(), "line 4") {
		t.Fatalf("error = %v, want line 4", err)
	}
}

func TestLoadConfigRejections(t *testing.T) {
	tests := map[string]struct{ body, want string }{
		"unknown key":        {"telegrma: []\n", "field telegrma not found"},
		"unset variable":     {"telegram:\n  - account: a\n    app_id: 1\n    app_hash: ${OTOGI_TEST_UNSET}\n", "OTOGI_TEST_UNSET"},
		"duplicate":          {"telegram:\n  - {account: a, app_id: 1, app_hash: h}\n  - {account: a, app_id: 1, app_hash: h}\n", "used twice"},
		"missing app hash":   {"telegram:\n  - {account: a, app_id: 1}\n", "app_hash"},
		"unknown module":     {"modules:\n  ehco: {}\n", "unknown module"},
		"bad level":          {"log: {level: loud}\n", "log.level"},
		"bad duration":       {"shutdown_timeout: soon\n", "line 1"},
		"bad operator":       {"operators: [\"123\"]\n", "<platform>:<actor id>"},
		"gemini without key": {"llm:\n  gemini: {base_url: https://g.example}\n", "llm.gemini.api_key"},
		"gemini bad url":     {"llm:\n  gemini: {api_key: k, base_url: g.example/v1}\n", "llm.gemini.base_url"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := loadConfig(writeConfig(t, test.body))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("loadConfig error = %v, want it to mention %q", err, test.want)
			}
		})
	}
}

func TestModuleSectionsAreStrict(t *testing.T) {
	cfg, err := loadConfig(writeConfig(t, "modules:\n  echo: {prefix: hi}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := buildModules(cfg.Modules, moduleEnv{}); err == nil || !strings.Contains(err.Error(), "prefix") {
		t.Fatalf("buildModules error = %v, want unknown field prefix", err)
	}
}

func TestLLMProviders(t *testing.T) {
	t.Setenv("TEST_GEMINI_KEY", "k")
	cfg, err := loadConfig(writeConfig(t, "llm:\n  gemini:\n    api_key: ${TEST_GEMINI_KEY}\n    base_url: https://gateway.example/gemini\n"))
	if err != nil {
		t.Fatal(err)
	}
	providers, err := buildProviders(cfg.LLM, otogi.NopRecorder{}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	lookup := providerLookup(providers)
	if p, err := lookup("gemini"); p == nil || err != nil {
		t.Fatalf("gemini: %v, %v", p, err)
	}
	if _, err := lookup("openai"); !errors.Is(err, otogi.ErrInvalid) {
		t.Fatalf("openai: %v, want ErrInvalid", err)
	}
}
