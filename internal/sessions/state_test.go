package sessions

import (
	"context"
	"database/sql"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vsem-azamat/agora/internal/store"
)

// A statement that writes rows of sessions, and an assignment to state or state_at in it: the
// assignments of a SET list follow SET or a comma (or form a parenthesised list after SET), and
// the conditions of a WHERE or CASE follow neither. REPLACE INTO sessions always writes the state.
var (
	writesSessions = regexp.MustCompile("(?i)\\b(?:UPDATE(?:\\s+OR\\s+\\w+)?|INSERT(?:\\s+OR\\s+\\w+)?\\s+INTO)\\s+(?:\\w+\\.)?[\"`\\[]?sessions\\b")
	replaces       = regexp.MustCompile("(?i)\\bREPLACE\\s+INTO\\s+(?:\\w+\\.)?[\"`\\[]?sessions\\b")
	setsState      = regexp.MustCompile("(?i)(?:(?:\\bSET|,)\\s*[\"`\\[]?state(?:_at)?[\"`\\]]?\\s*=|\\bSET\\s*\\([^)]*\\bstate(?:_at)?\\b[^)]*\\)\\s*=)")
)

// setsTheState reports whether the statement sql assigns a session's state or state_at.
func setsTheState(sql string) bool {
	return replaces.MatchString(sql) || writesSessions.MatchString(sql) && setsState.MatchString(sql)
}

// sqlOf is the text of e when it is a string literal, a name of a string (a local constant or
// variable through the declaration it resolves to, else a top-level one of the package in strs),
// or a + chain of them; any other operand stands as ?.
func sqlOf(e ast.Expr, strs map[string]string) (string, bool) { return sqlAt(e, strs, 0) }

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

// TestOnlyTransitionWritesTheState fails when code outside state.go sets a session's state or
// state_at, so that every change of state goes through transition and its table of moves.
func TestOnlyTransitionWritesTheState(t *testing.T) {
	t.Parallel()
	pkgs, err := goFiles(filepath.Join(".."))
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for dir, files := range pkgs {
		strs := map[string]string{} // the package's top-level string constants and variables
		for range 3 {               // a few rounds, for ones defined from others in other files
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
								if s, ok := sqlOf(v.Values[i], strs); ok {
									strs[name.Name] = s
								}
							}
						}
					}
				}
			}
		}
		for _, f := range files {
			ast.Inspect(f.file, func(n ast.Node) bool {
				e, ok := n.(ast.Expr)
				if _, name := n.(*ast.Ident); !ok || name {
					return true // a name is checked where its value is written
				}
				sql, ok := sqlOf(e, strs)
				if !ok || !setsTheState(sql) {
					return true
				}
				if filepath.Base(f.path) == "state.go" && filepath.Base(dir) == "sessions" {
					found++
					return false
				}
				t.Errorf("%s sets a session's state outside transition: %q", f.path, strings.Join(strings.Fields(sql), " "))
				return false // once per statement, not again for its parts
			})
		}
	}
	if found == 0 {
		t.Fatal("found no update of the state in state.go: the scan is broken")
	}
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

func TestTheScanSeesEveryWayToSetTheState(t *testing.T) {
	t.Parallel()
	for _, sql := range []string{
		"UPDATE sessions SET state = ?, state_at = ? WHERE id = ?",
		"UPDATE sessions SET agent = NULL, state = ? WHERE id = ?",
		"update sessions set turn = turn + 1,state_at = ?",
		"INSERT INTO sessions (id) VALUES (?) ON CONFLICT (id) DO UPDATE SET agent = CASE WHEN EXISTS (SELECT 1 FROM sessions WHERE id = 1) THEN NULL END, state = 'busy'",
		"UPDATE OR IGNORE sessions SET state = ? WHERE id = ?",
		"UPDATE main.sessions SET state = ?",
		`UPDATE "sessions" SET "state" = ?`,
		"UPDATE sessions SET (state, state_at) = (?, ?)",
		"REPLACE INTO sessions (id, state) VALUES (?, ?)",
	} {
		if !setsTheState(sql) {
			t.Errorf("missed %q", sql)
		}
	}
	for _, sql := range []string{
		"UPDATE sessions SET agent = NULL WHERE id = ? AND state = ?",
		"UPDATE session_controls SET state = ? WHERE id = ?",
		"UPDATE sessions SET woken_for = ?, woken_at = ? WHERE id = ?",
		"INSERT INTO sessions (id, state, state_at) VALUES (?, ?, ?) ON CONFLICT (id) DO UPDATE SET agent = CASE WHEN state = 'ended' THEN NULL ELSE agent END",
		"UPDATE sessions SET agent = ? WHERE id IN (SELECT id FROM sessions WHERE state = ?)",
	} {
		if setsTheState(sql) {
			t.Errorf("flagged %q", sql)
		}
	}
}

func TestTransitionMovesOnlyAlongTheTable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	at := time.UnixMilli(1000)
	// the table in docs/architecture/sessions.md
	legalMoves := map[[2]State]bool{{Idle, Busy}: true, {Ended, Busy}: true, {Busy, Idle}: true, {Busy, Ended}: true, {Idle, Ended}: true}
	states := []State{Busy, Idle, Ended}
	for _, from := range states {
		for _, to := range states {
			if from == to {
				continue
			}
			legal := legalMoves[[2]State{from, to}]
			err := store.InTx(ctx, db, func(tx *sql.Tx) error {
				if _, err := tx.ExecContext(ctx, `DELETE FROM sessions`); err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO sessions (id, kind, state, state_at, started_at, seen_at) VALUES ('s', 'k', ?, 1, 1, 1)`, from); err != nil {
					return err
				}
				moved, err := transition(ctx, tx, "s", change{to: to, from: []State{from}}, at)
				if legal != (err == nil) {
					t.Errorf("%s to %s: err %v, want legal %v", from, to, err, legal)
				}
				if legal && !moved {
					t.Errorf("%s to %s did not move", from, to)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestTransitionKeepsTheTimeOfAStateItDoesNotLeave(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, `INSERT INTO sessions (id, kind, state, state_at, started_at, seen_at, turn) VALUES ('s', 'k', 'busy', 1, 1, 1, 3)`); err != nil {
		t.Fatal(err)
	}
	for _, c := range []change{
		{to: Busy},                      // already busy
		{to: Busy, from: []State{Idle}}, // not idle
		{to: Idle, from: []State{Busy}, atTurn: true, turn: 2},       // another turn
		{to: Ended, from: []State{Idle}, atTurn: true, turn: 3},      // not idle
		{to: Busy, from: []State{Ended}, newTurn: true},              // not ended
		{to: Idle, from: []State{Busy}, atTurn: true, turn: 4},       // another turn
		{to: Busy, from: []State{Idle}, atTurn: true, newTurn: true}, // turn 0, not idle
	} {
		err := store.InTx(ctx, db, func(tx *sql.Tx) error {
			moved, err := transition(ctx, tx, "s", c, time.UnixMilli(5000))
			if moved {
				t.Errorf("%+v moved", c)
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	var state string
	var stateAt, turn int64
	if err := db.QueryRowContext(ctx, `SELECT state, state_at, turn FROM sessions WHERE id = 's'`).Scan(&state, &stateAt, &turn); err != nil {
		t.Fatal(err)
	}
	if state != "busy" || stateAt != 1 || turn != 3 {
		t.Fatalf("state %s at %d, turn %d", state, stateAt, turn)
	}
}

func TestAStopAfterTheEndLeavesTheSessionEnded(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// the session ended between the stop's report and its turn becoming idle
	if _, err := db.ExecContext(ctx, `INSERT INTO sessions (id, kind, state, state_at, started_at, seen_at) VALUES ('s', 'k', 'ended', 1, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	s := &Sessions{db: db, now: func() time.Time { return time.UnixMilli(5000) }}
	if _, err := s.stop(ctx, Report{SessionID: "s", Event: Stop}, "", s.now()); err != nil {
		t.Fatal(err)
	}
	var state string
	var stateAt int64
	if err := db.QueryRowContext(ctx, `SELECT state, state_at FROM sessions WHERE id = 's'`).Scan(&state, &stateAt); err != nil {
		t.Fatal(err)
	}
	if state != "ended" || stateAt != 1 {
		t.Fatalf("state %s at %d, want ended at 1", state, stateAt)
	}
}
