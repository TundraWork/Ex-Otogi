package telegram

import (
	"path/filepath"
	"testing"
)

func TestParseRuntimeConfig(t *testing.T) {
	t.Parallel()

	_, err := parseRuntimeConfig([]byte(`{"app_id":1,"app_hash":"hash","publish_timeout":"bad"}`))
	if err == nil {
		t.Fatal("expected parse error")
	}

	cfg, err := parseRuntimeConfig([]byte(`{"app_id":1,"app_hash":"hash"}`))
	if err != nil {
		t.Fatalf("parse runtime config failed: %v", err)
	}
	if cfg.appID != 1 {
		t.Fatalf("app id = %d, want 1", cfg.appID)
	}
	if cfg.appHash != "hash" {
		t.Fatalf("app hash = %q, want hash", cfg.appHash)
	}
}

func TestNewGotdSessionStorage(t *testing.T) {
	t.Parallel()

	sessionPath := filepath.Join(t.TempDir(), "nested", "telegram", "session.json")
	storage, err := newGotdSessionStorage(sessionPath)
	if err != nil {
		t.Fatalf("new gotd session storage failed: %v", err)
	}
	if !filepath.IsAbs(storage.Path) {
		t.Fatalf("session path = %q, want absolute", storage.Path)
	}
	if _, err := newGotdSessionStorage("   "); err == nil {
		t.Fatal("expected empty path error")
	}
}
