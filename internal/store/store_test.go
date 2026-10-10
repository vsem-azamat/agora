package store

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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
		if _, err := db.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO agents (name, joined_at) VALUES ('gone', 1), ('here', 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO sessions (id, kind, agent, state, state_at, started_at, seen_at) VALUES
		('session-gone', 'claude-code', NULL, 'ended', 1, 1, 1),
		('session-here', 'claude-code', 'here', 'busy', 1, 1, 1)`); err != nil {
		t.Fatal(err)
	}
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

// TestCIColumnsMigrationSplitsReported applies the migrations before it, stores a reported CI
// state, then opens the database so the migration moves it into its own columns.
func TestCIColumnsMigrationSplitsReported(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "agora.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	applyBefore(t, db, "0009_ci_columns.sql")
	if _, err := db.ExecContext(ctx, `INSERT INTO agents (name, joined_at) VALUES ('builder', 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO pull_requests (agent, repo, number, found, reported) VALUES
		('builder', 'github.com/example-org/example-app', 57, 1, 'green a1b2'),
		('builder', 'github.com/example-org/example-app', 58, 0, ''),
		('builder', 'github.com/example-org/example-app', 59, 1, 'red c3d4'),
		('builder', 'github.com/example-org/example-app', 60, 1, 'conflict e5f6'),
		('builder', 'github.com/example-org/example-app', 61, 1, 'green ')`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	db, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for n, want := range map[int][2]string{57: {"green", "a1b2"}, 58: {"", ""}, 59: {"red", "c3d4"}, 60: {"conflict", "e5f6"}, 61: {"green", ""}} {
		var state, head string
		if err := db.QueryRowContext(ctx, `SELECT ci_state, ci_head FROM pull_requests WHERE number = ?`, n).Scan(&state, &head); err != nil || [2]string{state, head} != want {
			t.Errorf("#%d: %q %q err %v, want %q", n, state, head, err, want)
		}
	}
}

// applyBefore applies the migrations before the one named stop to db, as an older Agora did.
func applyBefore(t *testing.T, db *sql.DB, stop string) {
	t.Helper()
	ctx := context.Background()
	names, _ := fs.Glob(migrations, "migrations/*.sql")
	for i, name := range names {
		if strings.HasSuffix(name, stop) {
			return
		}
		body, _ := migrations.ReadFile(name)
		if _, err := db.ExecContext(ctx, string(body)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
			t.Fatal(err)
		}
	}
	t.Fatalf("no migration %s", stop)
}

// TestNamesMigrationAddsSigilsAndFormerNames applies the migrations before it to a database
// with an agent, then opens it: the agent has no sigil or pigment, and former names can be
// recorded for it.
func TestNamesMigrationAddsSigilsAndFormerNames(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "agora.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	applyBefore(t, db, "0010_names.sql")
	if _, err := db.ExecContext(ctx, `INSERT INTO agents (name, joined_at) VALUES ('builder', 1)`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	db, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var icon, pigment string
	if err := db.QueryRowContext(ctx, `SELECT icon, pigment FROM agents WHERE name = 'builder'`).Scan(&icon, &pigment); err != nil || icon != "" || pigment != "" {
		t.Errorf("icon %q pigment %q err %v", icon, pigment, err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO former_names (name, agent, renamed_at) VALUES ('fixer', 'builder', 2)`); err != nil {
		t.Errorf("recording a former name: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO former_names (name, agent, renamed_at) VALUES ('ghost', 'nobody', 2)`); err == nil {
		t.Error("a former name of no agent was recorded")
	}
}

// TestSubscriptionModesMigrationKeepsSubscriptions applies the migrations before it to a
// database with a subscription, then opens it: the subscription follows with the mode all, and
// an unknown mode is refused.
func TestSubscriptionModesMigrationKeepsSubscriptions(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "agora.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	applyBefore(t, db, "0011_subscription_modes.sql")
	for _, q := range []string{
		`INSERT INTO agents (name, joined_at) VALUES ('builder', 1)`,
		`INSERT INTO rooms (name, purpose, created_by, created_at) VALUES ('example-app', 'work', 'builder', 1)`,
		`INSERT INTO subscriptions (agent, room) VALUES ('builder', 'example-app')`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	db, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var mode string
	if err := db.QueryRowContext(ctx, `SELECT mode FROM subscriptions WHERE agent = 'builder' AND room = 'example-app'`).Scan(&mode); err != nil || mode != "all" {
		t.Errorf("mode %q err %v", mode, err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE subscriptions SET mode = 'loud'`); err == nil {
		t.Error("an unknown mode was stored")
	}
}

func TestDatabaseFilesAreOwnerOnly(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "agora.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, `INSERT INTO agents (name, joined_at) VALUES ('builder', 1)`); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if mode := info.Mode().Perm(); mode != 0o600 {
			t.Errorf("%s: mode %o, want 600", filepath.Base(p), mode)
		}
	}
}

func TestExistingDatabaseFilesBecomeOwnerOnly(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "agora.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	files := []string{path, path + "-wal", path + "-shm"}
	for _, p := range files { // as left by an older agora; an empty WAL and shared-memory file are valid
		f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		f.Close()
		if err := os.Chmod(p, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	db, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, p := range files {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if mode := info.Mode().Perm(); mode != 0o600 {
			t.Errorf("%s: mode %o, want 600", filepath.Base(p), mode)
		}
	}
}

func TestNewerSchemaIsRefused(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "agora.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	names, _ := fs.Glob(migrations, "migrations/*.sql")
	if _, err := db.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", len(names)+1)); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if db, err := Open(ctx, path); err == nil || !strings.Contains(err.Error(), "newer") {
		if db != nil {
			db.Close()
		}
		t.Fatalf("err = %v, want a refusal of the newer schema", err)
	}
}
