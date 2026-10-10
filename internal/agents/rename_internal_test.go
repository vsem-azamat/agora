package agents

import (
	"context"
	"strings"
	"testing"

	"github.com/vsem-azamat/agora/internal/store"
)

// nameColumn reports whether a column, by its name, holds an agent's name.
func nameColumn(table, column string) bool {
	switch table {
	case "agents":
		return column == "name"
	case "former_names":
		return column == "agent"
	default:
		return column == "agent" || column == "author" || column == "actor" || strings.HasSuffix(column, "_by")
	}
}

// TestRenameMovesEveryNameColumn keeps the rename statements complete: every column of the
// schema that holds an agent's name has one.
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
	for _, table := range tables {
		columns, err := store.Strings(ctx, db, `SELECT name FROM pragma_table_info(?)`, table)
		if err != nil {
			t.Fatal(err)
		}
		for _, column := range columns {
			if nameColumn(table, column) && !covered[renameStatement(table, column)] {
				t.Errorf("%s.%s holds agent names but a rename leaves it", table, column)
			}
		}
	}
}

func renameStatement(table, column string) string {
	return "UPDATE " + table + " SET " + column + " = :new WHERE " + column + " = :old"
}
