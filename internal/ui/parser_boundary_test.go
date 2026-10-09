package ui

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

func TestArchitecture_UIUsesASTWithoutParsing(t *testing.T) {
	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		assertNoUIParserAccess(t, path, file)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func assertNoUIParserAccess(t *testing.T, path string, file *ast.File) {
	t.Helper()
	for _, spec := range file.Imports {
		if spec.Path.Value != `"github.com/roomscript/coderoom/internal/promptlang"` {
			continue
		}
		alias := "promptlang"
		if spec.Name != nil {
			alias = spec.Name.Name
		}
		if alias == "." {
			t.Errorf("%s uses a dot import that hides promptlang access", path)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "Parse" {
				return true
			}
			if name, ok := selector.X.(*ast.Ident); ok && name.Name == alias {
				t.Errorf("%s accesses promptlang.Parse; parsing belongs to the interpreter", path)
			}
			return true
		})
	}
}
