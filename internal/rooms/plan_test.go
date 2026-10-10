package rooms

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"github.com/vsem-azamat/agora/internal/store"
)

// TestUnreadQueryReachesMessagesThroughIndexes keeps the unread query from scanning the
// messages table, whatever the modes of the rooms followed: the planner must start from the
// candidates and look each message up by its id.
func TestUnreadQueryReachesMessagesThroughIndexes(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		`INSERT INTO agents (name, joined_at) VALUES ('reader', 0)`,
		`INSERT INTO rooms (name, purpose, created_by, created_at) VALUES ('room-1', 'x', 'reader', 0), ('room-2', 'x', 'reader', 0)`,
		`INSERT INTO subscriptions (agent, room, mode) VALUES ('reader', 'room-1', 'mentions'), ('reader', 'room-2', 'wake')`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 1000 {
		if _, err := db.ExecContext(ctx, `INSERT INTO messages (room, author, body, at) VALUES (?, 'reader', 'x', 0)`, fmt.Sprintf("room-%d", i%2+1)); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := db.QueryContext(ctx, `EXPLAIN QUERY PLAN `+unreadQuery, sql.Named("a", "reader"), sql.Named("general", General))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, step := range plan {
		if strings.HasPrefix(step, "SCAN m") || strings.HasPrefix(step, "SCAN messages") {
			t.Fatalf("the unread query scans messages (%q):\n%s", step, strings.Join(plan, "\n"))
		}
	}
}

// TestPendingCountUsesItsIndex keeps listing rooms from reading every message of a room to count
// the pending ones: the count must come from the partial index messages_pending alone.
func TestPendingCountUsesItsIndex(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `EXPLAIN QUERY PLAN `+listQuery)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(plan, "\n"), "SEARCH messages USING COVERING INDEX messages_pending (room=?)") {
		t.Fatalf("the pending count does not search messages_pending:\n%s", strings.Join(plan, "\n"))
	}
}
