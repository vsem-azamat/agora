package store_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// domainPackages are the packages whose refusals are built with store.Refuse.
var domainPackages = []string{"agents", "governance", "queue", "rooms", "sessions", "bridges"}

// TestRefusalsAreNotWrappedWithErrorf keeps one convention: a refusal is store.Refuse(kind, …),
// so its message says only what was refused. fmt.Errorf with %w as the first verb would put the
// kind's own text in front of it.
func TestRefusalsAreNotWrappedWithErrorf(t *testing.T) {
	fset := token.NewFileSet()
	checked := 0
	for _, pkg := range domainPackages {
		files, err := filepath.Glob(filepath.Join("..", pkg, "*.go"))
		if err != nil || len(files) == 0 {
			t.Fatalf("no sources of %s: %v", pkg, err)
		}
		for _, name := range files {
			if strings.HasSuffix(name, "_test.go") {
				continue
			}
			src, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			f, err := parser.ParseFile(fset, name, src, 0)
			if err != nil {
				t.Fatal(err)
			}
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) == 0 {
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
				if err != nil {
					return true
				}
				checked++
				if strings.HasPrefix(format, "%w") {
					t.Errorf("%s: fmt.Errorf(%q, …) starts with the kind's text; use store.Refuse(kind, …)", fset.Position(call.Pos()), format)
				}
				return true
			})
		}
	}
	if checked == 0 {
		t.Fatal("no fmt.Errorf calls found: the scan looks at the wrong files")
	}
}
