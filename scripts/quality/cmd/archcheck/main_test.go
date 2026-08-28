package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInspectSubtreeRetainsRepositoryPathClassification(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module github.com/TundraWork/Ex-Otogi\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "internal", "adapter", "telegram", "updates")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	bad := []byte("package updates\nimport _ \"github.com/TundraWork/Ex-Otogi/internal/kernel\"\n")
	if err := os.WriteFile(filepath.Join(dir, "bad.go"), bad, 0o600); err != nil {
		t.Fatal(err)
	}
	violations, err := inspect(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 1 || !strings.Contains(violations[0], "may import only repository contracts") {
		t.Fatalf("violations=%v", violations)
	}
}

func TestSameTelegramAdapterAllowanceIsNarrow(t *testing.T) {
	if reason := forbidden("/internal/adapter/telegram/updates", module+"internal/adapter/telegram/store"); reason != "" {
		t.Fatalf("same adapter rejected: %s", reason)
	}
	if reason := forbidden("/internal/adapter/telegram/updates", module+"internal/kernel"); reason == "" {
		t.Fatal("telegram adapter imported kernel")
	}
}
