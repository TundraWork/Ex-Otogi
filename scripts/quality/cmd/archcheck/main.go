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
		relative, err := filepath.Rel(root, filepath.Dir(path))
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
	return strings.HasPrefix(source, "/internal/adapter/telegram/") && strings.HasPrefix(target, "internal/adapter/telegram/")
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
