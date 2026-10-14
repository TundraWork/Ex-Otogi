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
	for _, target := range []string{"format", "fmt-check", "lint", "arch-check", "test", "test-race", "test-fuzz-smoke", "test-corpus", "test-leak", "telegram-smoke", "smoke", "quality"} {
		pattern := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(target) + `:`)
		if !pattern.MatchString(makefile) {
			t.Errorf("Makefile is missing %q target", target)
		}
	}
	if !regexp.MustCompile(`(?m)^# Experimental milestone gates`).MatchString(makefile) {
		t.Error("Makefile must label targets as experimental milestone gates")
	}
	quality := regexp.MustCompile(`(?m)^quality: ([^\n]+)$`).FindStringSubmatch(makefile)
	if len(quality) != 2 {
		t.Fatal("quality target is missing")
	}
	for _, target := range []string{"fmt-check", "lint", "arch-check", "test", "test-race", "test-fuzz-smoke", "test-corpus", "test-leak", "smoke"} {
		if !strings.Contains(" "+quality[1]+" ", " "+target+" ") {
			t.Errorf("quality does not include %q", target)
		}
	}
	if strings.Contains(" "+quality[1]+" ", " telegram-smoke ") {
		t.Error("quality must not contact Telegram")
	}
	fuzzGates := []*regexp.Regexp{
		regexp.MustCompile(`(?m)^\t\$\(GO\) test -run '\^\$\$' -fuzz '\^FuzzConvertEntitiesUTF16\$\$' -fuzztime=\$\(TELEGRAM_FUZZTIME\) \./internal/adapter/telegram/mapper$`),
		regexp.MustCompile(`(?m)^\t\$\(GO\) test -run '\^\$\$' -fuzz '\^FuzzDecodeEventInput\$\$' -fuzztime=\$\(TELEGRAM_FUZZTIME\) \./internal/adapter/telegram$`),
	}
	for _, fuzzGate := range fuzzGates {
		if !fuzzGate.MatchString(makefile) {
			t.Error("Telegram fuzz gate must run both bounded adapter fuzz targets with the repository Go wrapper")
		}
	}
	if !strings.Contains(makefile, "TELEGRAM_FUZZTIME ?= 1x") {
		t.Error("Telegram fuzz gate must have a deterministic default bound")
	}
	if !strings.Contains(makefile, "$(GO) test ./pkg/otogi -run 'Corpus'") {
		t.Error("corpus gate must run deterministic cross-protocol corpus tests")
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

func TestTelegramSmokeInjectsCredentialsOnlyIntoFinalProcess(t *testing.T) {
	for _, test := range []struct {
		name     string
		exitCode string
		wantErr  bool
	}{
		{name: "success", exitCode: "0"},
		{name: "failure", exitCode: "3", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			record := t.TempDir()
			fakeGo := filepath.Join(t.TempDir(), "go")
			script := "#!/bin/sh\nset -eu\nenv > \"$RECORD_DIR/build.env\"\nprintf '%s\\n' \"$@\" > \"$RECORD_DIR/build.args\"\n" +
				"[ \"$1\" = build ] && [ \"$2\" = -o ]\n" +
				"printf '#!/bin/sh\\nenv > \"$RECORD_DIR/run.env\"\\nexit %s\\n' \"$SMOKE_EXIT\" > \"$3\"\nchmod 0700 \"$3\"\n"
			if err := os.WriteFile(fakeGo, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			command := exec.Command("make", "--no-print-directory", "GO="+fakeGo, "telegram-smoke")
			command.Dir = "../.."
			command.Env = append(os.Environ(),
				"RECORD_DIR="+record, "SMOKE_EXIT="+test.exitCode,
				"OTOGI_TELEGRAM_APP_ID=12345", "OTOGI_TELEGRAM_APP_HASH=secret-app-hash", "OTOGI_TELEGRAM_SESSION_PATH=/private/session.json")
			output, err := command.CombinedOutput()
			if (err != nil) != test.wantErr {
				t.Fatalf("make telegram-smoke error = %v, want error %v; output: %s", err, test.wantErr, output)
			}
			if strings.Contains(string(output), "secret-app-hash") {
				t.Fatalf("make output exposed app hash: %s", output)
			}

			args := strings.Fields(readFile(t, filepath.Join(record, "build.args")))
			if len(args) != 4 || args[3] != "./cmd/telegram-smoke" {
				t.Fatalf("build arguments = %q, want build -o <binary> ./cmd/telegram-smoke", args)
			}
			if _, err := os.Stat(args[2]); !os.IsNotExist(err) {
				t.Errorf("built smoke binary %s was not removed: %v", args[2], err)
			}
			buildEnv := readFile(t, filepath.Join(record, "build.env"))
			runEnv := readFile(t, filepath.Join(record, "run.env"))
			for _, variable := range []string{"OTOGI_TELEGRAM_APP_ID=", "OTOGI_TELEGRAM_APP_HASH=", "OTOGI_TELEGRAM_SESSION_PATH="} {
				if regexp.MustCompile(`(?m)^` + variable).MatchString(buildEnv) {
					t.Errorf("build tool environment contains %s", variable)
				}
				if !regexp.MustCompile(`(?m)^` + variable).MatchString(runEnv) {
					t.Errorf("smoke process environment is missing %s", variable)
				}
			}
		})
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestArchitectureFixturesFail(t *testing.T) {
	for _, fixture := range []string{"arch_adapter_kernel", "arch_adapter_module", "arch_adapter_sibling", "arch_adapter_prefix_sibling", "arch_contract_external", "arch_local_internal", "arch_local_pkg"} {
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
