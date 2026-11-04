package hub

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"

	"github.com/vsem-azamat/agora/internal/agents"
	"github.com/vsem-azamat/agora/internal/avatars"
	"github.com/vsem-azamat/agora/internal/bridges"
	"github.com/vsem-azamat/agora/internal/governance"
	"github.com/vsem-azamat/agora/internal/install"
	"github.com/vsem-azamat/agora/internal/queue"
	"github.com/vsem-azamat/agora/internal/rooms"
	"github.com/vsem-azamat/agora/internal/sessions"
	"github.com/vsem-azamat/agora/internal/store"
)

// kinds lists every exported Err* kind of the packages under internal/, which the domain packages
// refuse with (store.Refuse(kind, …)). The scan in TestEveryErrorKindIsListed keeps the list complete.
var kinds = map[string]error{
	"agents.ErrTaken":   agents.ErrTaken,
	"agents.ErrUnknown": agents.ErrUnknown,

	"avatars.ErrNotFound":    avatars.ErrNotFound,
	"bridges.ErrAvatarsFull": bridges.ErrAvatarsFull,
	"bridges.ErrExists":      bridges.ErrExists,
	"bridges.ErrNotFound":    bridges.ErrNotFound,
	"governance.ErrClosed":   governance.ErrClosed,
	"governance.ErrNotFound": governance.ErrNotFound,
	"queue.ErrNotQueued":     queue.ErrNotQueued,
	"queue.ErrNotYourTurn":   queue.ErrNotYourTurn,
	"rooms.ErrArchived":      rooms.ErrArchived,
	"rooms.ErrBoardOnly":     rooms.ErrBoardOnly,
	"rooms.ErrBridged":       rooms.ErrBridged,
	"rooms.ErrExists":        rooms.ErrExists,
	"rooms.ErrNotArchived":   rooms.ErrNotArchived,
	"rooms.ErrNotFound":      rooms.ErrNotFound,
	"rooms.ErrReadOnly":      rooms.ErrReadOnly,
	"rooms.ErrUndelivered":   rooms.ErrUndelivered,
	"sessions.ErrNameTaken":  sessions.ErrNameTaken,
	"sessions.ErrRefused":    sessions.ErrRefused,
	"store.ErrInvalid":       store.ErrInvalid,
	"install.ErrNoClaude":    install.ErrNoClaude,
}

// typedKinds lists the exported error types of those packages that are refusals, with a value of each.
var typedKinds = map[string]error{
	"agents.FormerNameError": &agents.FormerNameError{},
	"queue.ForbiddenError":   &queue.ForbiddenError{},
}

// notRefusals are kinds that never reach the hub's API: the local CLI reports them itself.
var notRefusals = map[string]string{
	"install.ErrNoClaude": "`agora install` runs on the caller's machine and reports it itself",
}

// TestEveryErrorKindIsMapped fails when a kind a domain package refuses with has no Connect code in
// toConnect, which would answer a refusal with CodeInternal.
func TestEveryErrorKindIsMapped(t *testing.T) {
	for name, kind := range kinds {
		if _, skip := notRefusals[name]; skip {
			continue
		}
		err := toConnect(store.Refuse(kind, "refused"))
		if code := connect.CodeOf(err); code == connect.CodeInternal || code == connect.CodeUnknown {
			t.Errorf("%s is not mapped by toConnect: code %v", name, code)
		}
	}
	for name, kind := range typedKinds {
		if code := connect.CodeOf(toConnect(kind)); code == connect.CodeInternal || code == connect.CodeUnknown {
			t.Errorf("%s is not mapped by toConnect: code %v", name, code)
		}
	}
	if code := connect.CodeOf(toConnect(errors.New("unexpected"))); code != connect.CodeInternal {
		t.Errorf("an error of no kind: code %v, want internal", code)
	}
}

// TestEveryErrorKindIsListed reads the sources under internal/ and fails when a package exports an
// Err* variable or an error type that kinds or typedKinds does not list, or lists one that is gone.
func TestEveryErrorKindIsListed(t *testing.T) {
	var found []string
	fset := token.NewFileSet()
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
		pkg := f.Name.Name
		for _, decl := range f.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range gen.Specs {
				switch s := spec.(type) {
				case *ast.ValueSpec:
					if gen.Tok != token.VAR {
						continue
					}
					for _, id := range s.Names {
						if strings.HasPrefix(id.Name, "Err") && id.IsExported() {
							found = append(found, pkg+"."+id.Name)
						}
					}
				case *ast.TypeSpec:
					if _, isStruct := s.Type.(*ast.StructType); isStruct && s.Name.IsExported() && strings.HasSuffix(s.Name.Name, "Error") {
						found = append(found, pkg+"."+s.Name.Name)
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) == 0 {
		t.Fatal("no error kind found: the scan looks at the wrong files")
	}
	for _, name := range found {
		_, kind := kinds[name]
		_, typed := typedKinds[name]
		if !kind && !typed {
			t.Errorf("%s is not listed in kinds or typedKinds in kinds_test.go; map it in toConnect and list it", name)
		}
	}
	for name := range kinds {
		if !slices.Contains(found, name) {
			t.Errorf("%s is listed in kinds but no package exports it", name)
		}
	}
	for name := range typedKinds {
		if !slices.Contains(found, name) {
			t.Errorf("%s is listed in typedKinds but no package exports it", name)
		}
	}
	for name := range notRefusals {
		if _, ok := kinds[name]; !ok {
			t.Errorf("%s is in notRefusals but not in kinds", name)
		}
	}
}
