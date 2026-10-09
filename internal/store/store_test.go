package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
)

// TestProfilesMigrationMarksDepartedAgentsLeft applies the first two migrations, adds an agent
// whose sessions have all ended and one with a live session, then opens the database so the
// profiles migration runs.
func TestProfilesMigrationMarksDepartedAgentsLeft(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "agora.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"migrations/0001_resources.sql", "migrations/0002_sessions.sql"} {
		body, _ := migrations.ReadFile(name)
		if _, err := db.ExecContext(ctx, string(body)); err != nil {
			t.Fatal(err)
		}
		db.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", i+1))
	}
	db.ExecContext(ctx, `INSERT INTO agents (name, joined_at) VALUES ('gone', 1), ('here', 1)`)
	db.ExecContext(ctx, `INSERT INTO sessions (id, kind, agent, state, state_at, started_at, seen_at) VALUES
		('session-gone', 'claude-code', NULL, 'ended', 1, 1, 1),
		('session-here', 'claude-code', 'here', 'busy', 1, 1, 1)`)
	db.Close()

	db, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for name, want := range map[string]string{"gone": "left", "here": "working"} {
		var status string
		if err := db.QueryRowContext(ctx, `SELECT status FROM agents WHERE name = ?`, name).Scan(&status); err != nil || status != want {
			t.Errorf("%s: status %q err %v, want %q", name, status, err, want)
		}
	}
}
