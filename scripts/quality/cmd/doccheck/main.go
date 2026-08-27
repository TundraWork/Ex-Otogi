// Command doccheck enforces repository Go documentation conventions.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type finding struct {
	position token.Position
	message  string
}

func main() {
	paths := os.Args[1:]
	if len(paths) == 0 {
		paths = []string{"cmd", "internal", "modules", "pkg"}
	}
	findings, err := inspect(paths)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	for _, item := range findings {
		fmt.Fprintf(os.Stderr, "%s: %s\n", item.position, item.message)
	}
	if len(findings) != 0 {
		os.Exit(1)
	}
}

func inspect(paths []string) ([]finding, error) {
	set := token.NewFileSet()
	packages := make(map[string][]*ast.File)
	for _, root := range paths {
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				if entry.Name() == "testdata" && path != root {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, err := parser.ParseFile(set, path, nil, parser.ParseComments)
			if err != nil {
				return err
			}
			key := filepath.Dir(path) + "\x00" + file.Name.Name
			packages[key] = append(packages[key], file)
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("inspect %s: %w", root, err)
		}
	}

	var findings []finding
	for _, files := range packages {
		if !hasPackageComment(files) {
			file := files[0]
			findings = append(findings, finding{set.Position(file.Package), "package " + file.Name.Name + " has no package comment"})
		}
		for _, file := range files {
			findings = append(findings, inspectFile(set, file)...)
		}
	}
	sort.Slice(findings, func(i, j int) bool {
		left, right := findings[i].position, findings[j].position
		if left.Filename != right.Filename {
			return left.Filename < right.Filename
		}
		return left.Offset < right.Offset
	})
	return findings, nil
}

func hasPackageComment(files []*ast.File) bool {
	for _, file := range files {
		for _, comment := range file.Comments {
			text := strings.TrimSpace(comment.Text())
			if strings.HasPrefix(text, "Package "+file.Name.Name+" ") || (file.Name.Name == "main" && strings.HasPrefix(text, "Command ")) {
				return true
			}
		}
	}
	return false
}

func inspectFile(set *token.FileSet, file *ast.File) []finding {
	var findings []finding
	for _, declaration := range file.Decls {
		switch node := declaration.(type) {
		case *ast.FuncDecl:
			if node.Name.IsExported() && exportedReceiver(node.Recv) && !startsWithName(node.Doc, node.Name.Name) {
				findings = append(findings, finding{set.Position(node.Name.Pos()), "exported declaration " + node.Name.Name + " has no behavioral Godoc beginning with its name"})
			}
		case *ast.GenDecl:
			for _, specification := range node.Specs {
				typeSpec, ok := specification.(*ast.TypeSpec)
				if !ok || !typeSpec.Name.IsExported() {
					continue
				}
				doc := typeSpec.Doc
				if doc == nil {
					doc = node.Doc
				}
				if !startsWithName(doc, typeSpec.Name.Name) {
					findings = append(findings, finding{set.Position(typeSpec.Name.Pos()), "exported declaration " + typeSpec.Name.Name + " has no behavioral Godoc beginning with its name"})
				}
				findings = append(findings, inspectType(set, typeSpec.Type)...)
			}
		}
	}
	return findings
}

func inspectType(set *token.FileSet, expression ast.Expr) []finding {
	var fields *ast.FieldList
	switch node := expression.(type) {
	case *ast.StructType:
		fields = node.Fields
	case *ast.InterfaceType:
		fields = node.Methods
	default:
		return nil
	}
	var findings []finding
	for _, field := range fields.List {
		names := field.Names
		if len(names) == 0 {
			if name := embeddedName(field.Type); name != nil {
				names = []*ast.Ident{name}
			}
		}
		for _, name := range names {
			if name.IsExported() && !startsWithName(field.Doc, name.Name) {
				findings = append(findings, finding{set.Position(name.Pos()), "exported field or interface method " + name.Name + " has no Godoc beginning with its name"})
			}
		}
	}
	return findings
}

func embeddedName(expression ast.Expr) *ast.Ident {
	switch node := expression.(type) {
	case *ast.Ident:
		return node
	case *ast.SelectorExpr:
		return node.Sel
	case *ast.StarExpr:
		return embeddedName(node.X)
	case *ast.IndexExpr:
		return embeddedName(node.X)
	case *ast.IndexListExpr:
		return embeddedName(node.X)
	case *ast.ParenExpr:
		return embeddedName(node.X)
	default:
		return nil
	}
}

func exportedReceiver(receiver *ast.FieldList) bool {
	if receiver == nil {
		return true
	}
	expression := receiver.List[0].Type
	if pointer, ok := expression.(*ast.StarExpr); ok {
		expression = pointer.X
	}
	name, ok := expression.(*ast.Ident)
	return ok && name.IsExported()
}

func startsWithName(comment *ast.CommentGroup, name string) bool {
	return comment != nil && strings.HasPrefix(strings.TrimSpace(comment.Text()), name+" ")
}
