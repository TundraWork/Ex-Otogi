package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
)

const modulePrefix = "ex-otogi/"

type listedPackage struct {
	ImportPath   string
	Imports      []string
	TestImports  []string
	XTestImports []string
}

func main() {
	packages, err := listPackages()
	if err != nil {
		fmt.Fprintf(os.Stderr, "arch-check: %v\n", err)
		os.Exit(1)
	}

	violations := collectViolations(packages)
	if len(violations) == 0 {
		_, _ = fmt.Fprintf(os.Stdout, "arch-check: passed\n")
		return
	}

	_, _ = fmt.Fprintf(os.Stdout, "arch-check: architecture violations:\n")
	for _, violation := range violations {
		_, _ = fmt.Fprintf(os.Stdout, "  - %s\n", violation)
	}
	os.Exit(1)
}

func listPackages() ([]listedPackage, error) {
	cmd := exec.Command("go", "list", "-json", "-test", "./...")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("go list -json -test ./...: %w", err)
	}

	decoder := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	result := make([]listedPackage, 0, 64)
	for {
		var pkg listedPackage
		if err := decoder.Decode(&pkg); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("decode go list output: %w", err)
		}
		if pkg.ImportPath == "" {
			continue
		}
		result = append(result, pkg)
	}

	return result, nil
}

func collectViolations(packages []listedPackage) []string {
	found := make(map[string]struct{})

	for _, pkg := range packages {
		imports := append([]string{}, pkg.Imports...)
		imports = append(imports, pkg.TestImports...)
		imports = append(imports, pkg.XTestImports...)

		for _, imported := range imports {
			reason := violationReason(pkg.ImportPath, imported)
			if reason == "" {
				continue
			}
			entry := fmt.Sprintf("%s -> %s (%s)", pkg.ImportPath, imported, reason)
			found[entry] = struct{}{}
		}
	}

	violations := make([]string, 0, len(found))
	for violation := range found {
		violations = append(violations, violation)
	}
	sort.Strings(violations)

	return violations
}

func violationReason(importer, imported string) string {
	if strings.HasPrefix(importer, modulePrefix+"pkg/otogi") &&
		strings.HasPrefix(imported, modulePrefix+"internal/") {
		return "pkg/otogi must not import internal/*"
	}

	if strings.HasPrefix(importer, modulePrefix+"internal/kernel") &&
		strings.HasPrefix(imported, modulePrefix+"internal/driver") {
		return "internal/kernel must not import internal/driver/*"
	}

	if strings.HasPrefix(importer, modulePrefix+"modules/") &&
		strings.HasPrefix(imported, modulePrefix+"internal/") {
		return "modules/* must not import internal/*"
	}

	if strings.HasPrefix(importer, modulePrefix+"pkg/llm") &&
		strings.HasPrefix(imported, modulePrefix+"internal/") {
		return "pkg/llm must not import internal/*"
	}

	return ""
}
