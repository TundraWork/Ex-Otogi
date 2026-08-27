package quality_test

import (
	"os"
	"os/exec"
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
	for _, fixture := range []string{"undocumented_package", "undocumented_declaration", "undocumented_method", "undocumented_field"} {
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

func cleanGoEnv() []string {
	environment := make([]string, 0, len(os.Environ())+1)
	for _, variable := range os.Environ() {
		if !strings.HasPrefix(variable, "GOROOT=") && !strings.HasPrefix(variable, "GOTOOLCHAIN=") {
			environment = append(environment, variable)
		}
	}
	return append(environment, "GOTOOLCHAIN=go1.27.0")
}
