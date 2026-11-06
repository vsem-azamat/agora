// Package sqlscan finds the SQL text written in the Go code of a tree, for tests that hold one
// package alone responsible for writing some columns. It is imported only by tests.
package sqlscan

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
)

// Statement is a string the scan found: a literal, a name of one, or a + chain of them.
type Statement struct {
	Path string // the file it is written in
	SQL  string // its text, with ? for an operand that is not a string the scan can read
}

// Statements returns every string expression of the non-test Go files under root, once each:
// literals, names of strings (a local constant or variable through the declaration it resolves
// to, else a top-level one of its package) and + chains of them, each read whole rather than
// again for its parts. A name is read where its value is written, not where it is used.
func Statements(root string) ([]Statement, error) {
	pkgs, err := goFiles(root)
	if err != nil {
		return nil, err
	}
	var out []Statement
	for _, files := range pkgs {
		strs := topLevel(files)
		for _, f := range files {
			ast.Inspect(f.file, func(n ast.Node) bool {
				e, ok := n.(ast.Expr)
				if _, name := n.(*ast.Ident); !ok || name {
					return true
				}
				sql, ok := sqlAt(e, strs, 0)
				if !ok {
					return true
				}
				out = append(out, Statement{Path: f.path, SQL: sql})
				return false
			})
		}
	}
	return out, nil
}

// topLevel is the package's top-level string constants and variables, by name.
func topLevel(files []goFile) map[string]string {
	strs := map[string]string{}
	for range 3 { // a few rounds, for ones defined from others in other files
		for _, f := range files {
			for _, d := range f.file.Decls {
				g, ok := d.(*ast.GenDecl)
				if !ok {
					continue
				}
				for _, spec := range g.Specs {
					v, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for i, name := range v.Names {
						if i < len(v.Values) {
							if s, ok := sqlAt(v.Values[i], strs, 0); ok {
								strs[name.Name] = s
							}
						}
					}
				}
			}
		}
	}
	return strs
}

func sqlAt(e ast.Expr, strs map[string]string, depth int) (string, bool) {
	if depth > 20 {
		return "", false
	}
	depth++
	switch e := e.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return "", false
		}
		v, err := strconv.Unquote(e.Value)
		return v, err == nil
	case *ast.Ident:
		if v, ok := valueOf(e); ok {
			return sqlAt(v, strs, depth)
		}
		v, ok := strs[e.Name]
		return v, ok
	case *ast.ParenExpr:
		return sqlAt(e.X, strs, depth)
	case *ast.BinaryExpr:
		if e.Op != token.ADD {
			return "", false
		}
		x, okx := sqlAt(e.X, strs, depth)
		y, oky := sqlAt(e.Y, strs, depth)
		if !okx && !oky {
			return "", false
		}
		if !okx {
			x = "?"
		}
		if !oky {
			y = "?"
		}
		return x + y, true
	}
	return "", false
}

// valueOf is the expression the name id was declared with in its file, when it is declared
// with one (const, var or :=).
func valueOf(id *ast.Ident) (ast.Expr, bool) {
	if id.Obj == nil {
		return nil, false
	}
	switch d := id.Obj.Decl.(type) {
	case *ast.ValueSpec:
		for i, name := range d.Names {
			if name.Name == id.Name && i < len(d.Values) {
				return d.Values[i], true
			}
		}
	case *ast.AssignStmt:
		for i, l := range d.Lhs {
			if l, ok := l.(*ast.Ident); ok && l.Name == id.Name && i < len(d.Rhs) && len(d.Lhs) == len(d.Rhs) {
				return d.Rhs[i], true
			}
		}
	}
	return nil, false
}

type goFile struct {
	path string
	file *ast.File
}

// goFiles parses the non-test Go files under root, by directory.
func goFiles(root string) (map[string][]goFile, error) {
	out := map[string][]goFile{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0) // with object resolution, for valueOf
		if err != nil {
			return err
		}
		out[filepath.Dir(path)] = append(out[filepath.Dir(path)], goFile{path, f})
		return nil
	})
	return out, err
}
