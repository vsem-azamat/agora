package agents

import (
	"context"
	"testing"

	"github.com/vsem-azamat/agora/internal/store"
)

// textColumns classifies every TEXT column of the schema: true when it holds an agent's current
// name, so a rename must move it; false when it holds anything else (former_names.name holds
// the names an agent gave up, which stay).
var textColumns = map[string]map[string]bool{
	"resources":       {"key": false},
	"entries":         {"key": false, "agent": true, "note": false, "state": false},
	"removals":        {"key": false, "agent": true, "actor": true},
	"agents":          {"name": true, "kind": false, "project": false, "task": false, "status": false, "cwd": false, "branch": false, "prs": false, "about": false, "icon": false, "pigment": false},
	"sessions":        {"id": false, "kind": false, "agent": true, "cwd": false, "terminal": false, "state": false, "noted": false, "woken_for": false, "wake_result": false},
	"rooms":           {"name": false, "purpose": false, "created_by": true},
	"messages":        {"room": false, "author": true, "body": false},
	"mentions":        {"agent": true},
	"subscriptions":   {"agent": true, "room": false},
	"read_positions":  {"agent": true, "room": false},
	"read_marks":      {"agent": true},
	"proposals":       {"title": false, "body": false, "author": true, "state": false, "closed_by": true},
	"votes":           {"agent": true, "choice": false, "reason": false},
	"charter_changes": {"changed_by": true},
	"charter":         {"body": false, "changed_by": true},
	"pull_requests":   {"agent": true, "repo": false, "ci_state": false, "ci_head": false},
	"web_token":       {"token": false},
	"former_names":    {"name": false, "agent": true},
}

// TestRenameMovesEveryNameColumn keeps the rename statements complete: every TEXT column of
// the schema is classified above, and every column that holds an agent's name has a rename
// statement.
func TestRenameMovesEveryNameColumn(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tables, err := store.Strings(ctx, db, `SELECT name FROM sqlite_schema WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatal(err)
	}
	covered := map[string]bool{}
	for _, stmt := range renames {
		covered[stmt] = true
	}
	seen := 0
	for _, table := range tables {
		columns, err := store.Strings(ctx, db, `SELECT name FROM pragma_table_info(?) WHERE upper(type) = 'TEXT'`, table)
		if err != nil {
			t.Fatal(err)
		}
		for _, column := range columns {
			holdsName, ok := textColumns[table][column]
			switch {
			case !ok:
				t.Errorf("%s.%s is not classified: say whether it holds an agent's name", table, column)
			case holdsName && !covered[renameStatement(table, column)]:
				t.Errorf("%s.%s holds agent names but a rename leaves it", table, column)
			}
			seen++
		}
	}
	classified := 0
	for _, columns := range textColumns {
		classified += len(columns)
	}
	if classified != seen {
		t.Errorf("%d columns classified, %d in the schema: drop columns that no longer exist", classified, seen)
	}
	if len(covered) != len(renames) {
		t.Error("a rename statement is listed twice")
	}
}

func renameStatement(table, column string) string {
	return "UPDATE " + table + " SET " + column + " = :new WHERE " + column + " = :old"
}
