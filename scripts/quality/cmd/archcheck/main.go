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
	root := "."
	if len(os.Args) == 2 {
		root = os.Args[1]
	}
	violations, err := inspect(root)
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
	walkRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	classificationRoot := classificationRoot(walkRoot)
	var violations []string
	err = filepath.WalkDir(walkRoot, func(path string, entry os.DirEntry, walkErr error) error {
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
		relative, err := filepath.Rel(classificationRoot, filepath.Dir(path))
		if err != nil {
			return err
		}
		source := filepath.ToSlash(relative)
		for _, imported := range file.Imports {
			target, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				return err
			}
			reason := forbidden(source, target)
			if reason != "" {
				violations = append(violations, path+": "+reason+": "+target)
			}
		}
		return nil
	})
	return violations, err
}

func repositoryRoot(path string) string {
	current, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	if info, statErr := os.Stat(current); statErr == nil && !info.IsDir() {
		current = filepath.Dir(current)
	}
	for {
		if _, err := os.Stat(filepath.Join(current, "go.mod")); err == nil {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			return path
		}
		current = parent
	}
}

func classificationRoot(walkRoot string) string {
	repository := repositoryRoot(walkRoot)
	relative, err := filepath.Rel(repository, walkRoot)
	if err != nil || relative == "." {
		return repository
	}
	first := relative
	if separator := strings.IndexRune(first, filepath.Separator); separator >= 0 {
		first = first[:separator]
	}
	switch first {
	case "cmd", "internal", "modules", "pkg":
		return repository
	default:
		return walkRoot
	}
}

func forbidden(source, target string) string {
	source = "/" + strings.Trim(source, "/")
	internal := strings.HasPrefix(target, module)
	repositoryTarget := strings.TrimPrefix(target, module)
	switch {
	case strings.HasPrefix(source, "/pkg/otogi") && !isStandardLibrary(target):
		return "contracts must import only the standard library"
	case contractOnlySource(source) && internal && repositoryTarget != "pkg/otogi" && !sameTelegramAdapter(source, repositoryTarget):
		return "kernel, modules, and adapters may import only repository contracts"
	default:
		return ""
	}
}

func sameTelegramAdapter(source, target string) bool {
	return (source == "/internal/adapter/telegram" || strings.HasPrefix(source, "/internal/adapter/telegram/")) && strings.HasPrefix(target, "internal/adapter/telegram/")
}

func isStandardLibrary(importPath string) bool {
	first := importPath
	if slash := strings.IndexByte(first, '/'); slash >= 0 {
		first = first[:slash]
	}
	return !strings.Contains(first, ".")
}

func contractOnlySource(source string) bool {
	return source == "/internal/kernel" || strings.HasPrefix(source, "/internal/kernel/") ||
		source == "/modules" || strings.HasPrefix(source, "/modules/") ||
		source == "/internal/adapter" || strings.HasPrefix(source, "/internal/adapter/")
}
