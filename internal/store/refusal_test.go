package store_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestRefusalsAreNotWrappedWithErrorf keeps one convention: a refusal is store.Refuse(kind, …), so
// its message says only what was refused. fmt.Errorf whose format starts with %w and whose first
// argument is a kind (an Err* identifier, such as store.ErrInvalid) would put the kind's own text in
// front of it. Wrapping another error with context stays legal. The scan reads the sources from
// disk, so a build overlay does not reach it.
func TestRefusalsAreNotWrappedWithErrorf(t *testing.T) {
	fset := token.NewFileSet()
	checked := 0
	err := filepath.WalkDir("..", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return err
		}
		src, err := os.ReadFile(name) //nolint:gosec // a test reading the repository's own sources
		if err != nil {
			return err
		}
		f, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) < 2 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Errorf" {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); !ok || id.Name != "fmt" {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			format, err := strconv.Unquote(lit.Value)
			if err != nil || !strings.HasPrefix(format, "%w") {
				return true
			}
			checked++
			if isKind(call.Args[1]) {
				t.Errorf("%s: fmt.Errorf(%q, %s, …) starts with the kind's text; use store.Refuse(kind, …)", fset.Position(call.Pos()), format, types.ExprString(call.Args[1]))
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked == 0 {
		t.Fatal("no fmt.Errorf with a leading %w found: the scan looks at the wrong files")
	}
}

// isKind reports whether e names an error kind: an Err* identifier or a package's Err* variable.
func isKind(e ast.Expr) bool {
	switch e := e.(type) {
	case *ast.Ident:
		return strings.HasPrefix(e.Name, "Err")
	case *ast.SelectorExpr:
		return strings.HasPrefix(e.Sel.Name, "Err")
	}
	return false
}
