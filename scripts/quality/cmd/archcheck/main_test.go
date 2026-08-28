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

func TestInspectAcceptsRelativeRepositoryRoot(t *testing.T) {
	if _, err := inspect("."); err != nil {
		t.Fatal(err)
	}
}

func TestClassificationRootPreservesSyntheticFixtureRoot(t *testing.T) {
	fixture, err := filepath.Abs(filepath.Join("testdata", "arch_adapter_kernel"))
	if err != nil {
		t.Fatal(err)
	}
	if got := classificationRoot(fixture); got != fixture {
		t.Fatalf("classification root = %q, want %q", got, fixture)
	}
}

func TestSameTelegramAdapterAllowanceIsNarrow(t *testing.T) {
	if reason := forbidden("/internal/adapter/telegram", module+"internal/adapter/telegram/store"); reason != "" {
		t.Fatalf("adapter root rejected: %s", reason)
	}
	if reason := forbidden("/internal/adapter/telegram/updates", module+"internal/adapter/telegram/store"); reason != "" {
		t.Fatalf("same adapter rejected: %s", reason)
	}
	if reason := forbidden("/internal/adapter/telegram/updates", module+"internal/kernel"); reason == "" {
		t.Fatal("telegram adapter imported kernel")
	}
}

func TestGotdImportsAreConfinedToTelegramAdapter(t *testing.T) {
	for _, source := range []string{"/cmd/telegram-smoke", "/internal/kernel", "/modules/example", "/pkg/otogi", "/scripts/quality/cmd/archcheck"} {
		if reason := forbidden(source, "github.com/gotd/td/tg"); !strings.Contains(reason, "gotd imports are confined") {
			t.Errorf("source %q reason = %q", source, reason)
		}
	}
	for _, source := range []string{"/internal/adapter/telegram", "/internal/adapter/telegram/mapper"} {
		if reason := forbidden(source, "github.com/gotd/td/tg"); reason != "" {
			t.Errorf("source %q rejected: %s", source, reason)
		}
	}
}

func TestGotdConfinementDoesNotMatchLookalikeModules(t *testing.T) {
	if reason := forbidden("/pkg/otogi", "github.com/gotd/tdx/tg"); reason != "contracts must import only the standard library" {
		t.Fatalf("reason = %q", reason)
	}
}

func TestInspectChecksGotdImportsInTestFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "leak_test.go"), []byte("package leak_test\nimport _ \"github.com/gotd/td/tg\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	violations, err := inspect(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 1 || !strings.Contains(violations[0], "gotd imports are confined") {
		t.Fatalf("violations = %v", violations)
	}
}
