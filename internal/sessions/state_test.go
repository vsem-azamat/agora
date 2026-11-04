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
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vsem-azamat/agora/internal/store"
)

// A statement that writes rows of sessions (an update, or an upsert into it), and an assignment
// to state or state_at in it: the assignments of a SET list follow SET or a comma, and the
// conditions of a WHERE or CASE follow neither.
var (
	writesSessions = regexp.MustCompile(`(?i)\b(?:UPDATE|INSERT\s+INTO)\s+sessions\b`)
	setsState      = regexp.MustCompile(`(?i)(?:\bSET|,)\s*state(?:_at)?\s*=`)
)

// setsTheState reports whether the statement sql assigns a session's state or state_at.
func setsTheState(sql string) bool {
	return writesSessions.MatchString(sql) && setsState.MatchString(sql)
}

// TestOnlyTransitionWritesTheState fails when code outside state.go sets a session's state or
// state_at, so that every change of state goes through transition and its table of moves.
func TestOnlyTransitionWritesTheState(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..")
	found := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			sql, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			if !setsTheState(sql) {
				return true
			}
			if filepath.Base(path) == "state.go" && filepath.Base(filepath.Dir(path)) == "sessions" {
				found++
				return true
			}
			t.Errorf("%s sets a session's state outside transition: %q", path, strings.Join(strings.Fields(sql), " "))
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if found == 0 {
		t.Fatal("found no update of the state in state.go: the scan is broken")
	}
}

func TestTheScanSeesEveryWayToSetTheState(t *testing.T) {
	t.Parallel()
	for _, sql := range []string{
		"UPDATE sessions SET state = ?, state_at = ? WHERE id = ?",
		"UPDATE sessions SET agent = NULL, state = ? WHERE id = ?",
		"update sessions set turn = turn + 1,state_at = ?",
		"INSERT INTO sessions (id) VALUES (?) ON CONFLICT (id) DO UPDATE SET agent = CASE WHEN EXISTS (SELECT 1 FROM sessions WHERE id = 1) THEN NULL END, state = 'busy'",
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
	states := []State{Busy, Idle, Ended}
	for _, from := range states {
		for _, to := range states {
			if from == to {
				continue
			}
			legal := slices.Contains(moves[to], from)
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
