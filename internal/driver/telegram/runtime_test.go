package telegram

import (
	"path/filepath"
	"testing"
	"time"
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
	if cfg.downloadTimeout != defaultMediaDownloadTimeout {
		t.Fatalf("download timeout = %s, want %s", cfg.downloadTimeout, defaultMediaDownloadTimeout)
	}
	if cfg.downloadThreads != defaultMediaDownloadThreads {
		t.Fatalf("download threads = %d, want %d", cfg.downloadThreads, defaultMediaDownloadThreads)
	}
	if cfg.attachmentCacheEntries != defaultMediaLocatorCacheEntries {
		t.Fatalf("attachment cache entries = %d, want %d", cfg.attachmentCacheEntries, defaultMediaLocatorCacheEntries)
	}
}

func TestParseRuntimeConfigDownloadSettings(t *testing.T) {
	t.Parallel()

	cfg, err := parseRuntimeConfig([]byte(`{
		"app_id":1,
		"app_hash":"hash",
		"download_timeout":"45s",
		"download_threads":8,
		"download_verify":true,
		"attachment_cache_entries":512
	}`))
	if err != nil {
		t.Fatalf("parse runtime config failed: %v", err)
	}
	if cfg.downloadTimeout != 45*time.Second {
		t.Fatalf("download timeout = %s, want 45s", cfg.downloadTimeout)
	}
	if cfg.downloadThreads != 8 {
		t.Fatalf("download threads = %d, want 8", cfg.downloadThreads)
	}
	if !cfg.downloadVerify {
		t.Fatal("download verify = false, want true")
	}
	if cfg.attachmentCacheEntries != 512 {
		t.Fatalf("attachment cache entries = %d, want 512", cfg.attachmentCacheEntries)
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
