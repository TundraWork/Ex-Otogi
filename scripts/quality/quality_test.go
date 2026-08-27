package quality_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestExperimentalMilestoneTargetsArePresent(t *testing.T) {
	data, err := os.ReadFile("../../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	makefile := string(data)
	for _, target := range []string{"format", "fmt-check", "lint", "arch-check", "test", "test-race", "test-leak", "smoke", "quality"} {
		pattern := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(target) + `:`)
		if !pattern.MatchString(makefile) {
			t.Errorf("Makefile is missing %q target", target)
		}
	}
	if !regexp.MustCompile(`(?m)^# Experimental milestone gates`).MatchString(makefile) {
		t.Error("Makefile must label targets as experimental milestone gates")
	}
	if !regexp.MustCompile(`(?m)^quality: fmt-check lint arch-check test test-race test-leak smoke$`).MatchString(makefile) {
		t.Error("quality must run every nonmutating milestone gate")
	}
}

func TestDocumentationFixturesFailForTheirNamedReason(t *testing.T) {
	for _, fixture := range []string{"undocumented_package", "undocumented_declaration", "undocumented_method", "undocumented_field", "undocumented_embedded"} {
		t.Run(fixture, func(t *testing.T) {
			command := exec.Command("go", "run", "./scripts/quality/cmd/doccheck", "./scripts/quality/testdata/"+fixture)
			command.Dir = "../.."
			command.Env = cleanGoEnv()
			output, err := command.CombinedOutput()
			if err == nil {
				t.Fatalf("fixture unexpectedly passed: %s", output)
			}
			if !strings.Contains(string(output), fixture) {
				t.Fatalf("output %q does not identify %q", output, fixture)
			}
		})
	}
}

func TestRepresentativeLinterFixtureFails(t *testing.T) {
	command := exec.Command("./scripts/quality/lint-fixture.sh", "./testdata/linter_errors")
	command.Dir = "../.."
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("fixture unexpectedly passed: %s", output)
	}
	for _, linter := range []string{"errcheck", "errorlint"} {
		if !strings.Contains(string(output), linter) {
			t.Errorf("output %q does not contain %q", output, linter)
		}
	}
}

func TestFmtCheckPreservesFormatterFailure(t *testing.T) {
	command := exec.Command("make", "fmt-check", "GO_FILES=./scripts/quality/testdata/invalid_format/bad.go")
	command.Dir = "../.."
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("invalid Go unexpectedly passed fmt-check: %s", output)
	}
	if strings.Contains(string(output), "exit 1") && !strings.Contains(string(output), "expected") {
		t.Fatalf("fmt-check hid formatter parse failure: %s", output)
	}
}

func TestSmokePreservesCommandFailure(t *testing.T) {
	command := exec.Command("make", "smoke", "SMOKE_COMMAND=./scripts/quality/testdata/smoke_failure.sh")
	command.Dir = "../.."
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("nonzero smoke command unexpectedly passed: %s", output)
	}
}

func TestArchitectureFixturesFail(t *testing.T) {
	for _, fixture := range []string{"arch_adapter_kernel", "arch_adapter_module", "arch_adapter_sibling", "arch_adapter_prefix_sibling", "arch_contract_external"} {
		t.Run(fixture, func(t *testing.T) {
			command := exec.Command("go", "run", "./scripts/quality/cmd/archcheck", "./scripts/quality/testdata/"+fixture)
			command.Dir = "../.."
			command.Env = cleanGoEnv()
			output, err := command.CombinedOutput()
			if err == nil {
				t.Fatalf("forbidden import fixture unexpectedly passed: %s", output)
			}
			if !strings.Contains(string(output), fixture) {
				t.Fatalf("output %q does not identify %q", output, fixture)
			}
		})
	}
}

func TestLintFixtureKeepsRelativeCacheAtRepository(t *testing.T) {
	root := filepath.Join("..", "..")
	cache := filepath.Join(root, ".cache", "quality-test")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(cache) })
	binary := filepath.Join(cache, "golangci-lint-2.13.1")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\necho 'errcheck errorlint' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("./scripts/quality/lint-fixture.sh", "./testdata/linter_errors")
	command.Dir = root
	command.Env = append(cleanGoEnv(), "GOLANGCI_LINT_CACHE=.cache/quality-test")
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "errcheck errorlint") {
		t.Fatalf("repository cache binary was not reused: err=%v output=%s", err, output)
	}
}

func cleanGoEnv() []string {
	environment := make([]string, 0, len(os.Environ())+1)
	for _, variable := range os.Environ() {
		if !strings.HasPrefix(variable, "GOROOT=") && !strings.HasPrefix(variable, "GOTOOLCHAIN=") {
			environment = append(environment, variable)
		}
	}
	return append(environment, "GOTOOLCHAIN=go1.27.0")
}
