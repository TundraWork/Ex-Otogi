// Command archcheck enforces the lean runtime's Go import boundaries.
package main

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const module = "github.com/TundraWork/Ex-Otogi/"

func main() {
	violations, err := inspect(".")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	for _, violation := range violations {
		fmt.Fprintln(os.Stderr, violation)
	}
	if len(violations) != 0 {
		os.Exit(1)
	}
}

func inspect(root string) ([]string, error) {
	var violations []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == ".cache" || entry.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		source := filepath.ToSlash(filepath.Dir(strings.TrimPrefix(path, "./")))
		for _, imported := range file.Imports {
			target, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				return err
			}
			if reason := forbidden(source, strings.TrimPrefix(target, module)); reason != "" && strings.HasPrefix(target, module) {
				violations = append(violations, path+": "+reason+": "+target)
			}
		}
		return nil
	})
	return violations, err
}

func forbidden(source, target string) string {
	switch {
	case strings.HasPrefix(source, "pkg/otogi") && (strings.HasPrefix(target, "internal/") || strings.HasPrefix(target, "modules/")):
		return "contracts must not import implementation packages"
	case strings.HasPrefix(source, "internal/kernel") && (strings.HasPrefix(target, "internal/adapter/") || strings.HasPrefix(target, "modules/")):
		return "kernel must not import adapters or modules"
	case strings.HasPrefix(source, "modules/") && (strings.HasPrefix(target, "internal/") || strings.HasPrefix(target, "modules/")):
		return "modules must depend only on contracts"
	default:
		return ""
	}
}
