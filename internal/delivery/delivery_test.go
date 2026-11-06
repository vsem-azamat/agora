package delivery_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/vsem-azamat/agora/internal/bridges"
	"github.com/vsem-azamat/agora/internal/delivery"
	"github.com/vsem-azamat/agora/internal/queue"
	"github.com/vsem-azamat/agora/internal/rooms"
	"github.com/vsem-azamat/agora/internal/sessions"
	"github.com/vsem-azamat/agora/internal/sqlscan"
	"github.com/vsem-azamat/agora/internal/store"
)

var ctx = context.Background()

// A statement that writes rows of messages, and an assignment to a column delivery owns in it:
// the assignments of a SET list follow SET or a comma (or form a parenthesised list after SET),
// and the conditions of a WHERE or CASE follow neither. REPLACE INTO messages writes them all.
var (
	writesMessages = regexp.MustCompile("(?i)\\b(?:UPDATE(?:\\s+OR\\s+\\w+)?|INSERT(?:\\s+OR\\s+\\w+)?\\s+INTO)\\s+(?:\\w+\\.)?[\"`\\[]?messages\\b")
	replaces       = regexp.MustCompile("(?i)\\bREPLACE\\s+INTO\\s+(?:\\w+\\.)?[\"`\\[]?messages\\b")
	setsDelivery   = regexp.MustCompile("(?i)(?:(?:\\bSET|,)\\s*[\"`\\[]?(?:delivery|delivery_error|scheduled_at)[\"`\\]]?\\s*=|\\bSET\\s*\\([^)]*\\b(?:delivery|delivery_error|scheduled_at)\\b[^)]*\\)\\s*=)")
)

// setsTheDelivery reports whether the statement sql assigns a message's delivery, delivery_error
// or scheduled_at.
func setsTheDelivery(sql string) bool {
	return replaces.MatchString(sql) || writesMessages.MatchString(sql) && setsDelivery.MatchString(sql)
}

// TestOnlyDeliveryWritesTheDelivery fails when code outside this package changes where a message
// stands on its way out, so that every change goes through Move and its table of moves. The state a
// new message is stored with comes from Initial.
func TestOnlyDeliveryWritesTheDelivery(t *testing.T) {
	t.Parallel()
	stmts, err := sqlscan.Statements(filepath.Join(".."))
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, s := range stmts {
		if !setsTheDelivery(s.SQL) {
			continue
		}
		if filepath.Base(filepath.Dir(s.Path)) == "delivery" {
			found++
			continue
		}
		t.Errorf("%s changes a message's delivery outside the delivery package: %q", s.Path, strings.Join(strings.Fields(s.SQL), " "))
	}
	if found < 2 {
		t.Fatalf("found %d updates of the delivery in the delivery package, want Move's and Reschedule's: the scan is broken", found)
	}
}

func TestTheScanSeesEveryWayToSetTheDelivery(t *testing.T) {
	t.Parallel()
	for _, sql := range []string{
		"UPDATE messages SET delivery = 'sending' WHERE room = ?",
		"UPDATE messages SET echo_of = NULL, delivery_error = ? WHERE id = ?",
		"update messages set scheduled_at = ?",
		"UPDATE OR IGNORE messages SET delivery = ?",
		"UPDATE main.messages SET delivery = ?",
		`UPDATE "messages" SET "delivery" = ?`,
		"UPDATE messages SET (delivery, delivery_error) = (?, ?)",
		"INSERT INTO messages (id) VALUES (?) ON CONFLICT (id) DO UPDATE SET delivery = 'sent'",
		"REPLACE INTO messages (id, delivery) VALUES (?, ?)",
	} {
		if !setsTheDelivery(sql) {
			t.Errorf("missed %q", sql)
		}
	}
	for _, sql := range []string{
		"UPDATE messages SET echo_of = NULL WHERE echo_of = ? AND delivery = 'sending'",
		"UPDATE messages SET sent_out = ? WHERE id = ?",
		"UPDATE bridges SET delivery = ?",
		"UPDATE messages SET ext_id = ? WHERE id IN (SELECT id FROM messages WHERE delivery = 'sent')",
	} {
		if setsTheDelivery(sql) {
			t.Errorf("flagged %q", sql)
		}
	}
}

// newBoard returns a board where builder joined and bridged example-chat and example-other.
func newBoard(t *testing.T) (*sql.DB, *rooms.Rooms) {
	t.Helper()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := rooms.New(db, nil)
	b := bridges.New(db, r, nil)
	if _, err := sessions.New(db, queue.New(db, nil), r, nil).Join(ctx, "builder", "", false); err != nil {
		t.Fatal(err)
	}
	for _, room := range []string{"example-chat", "example-other"} {
		if _, err := b.Add(ctx, "builder", room, "example-bridge", "", nil); err != nil {
			t.Fatal(err)
		}
	}
	return db, r
}

// post stores a message of builder in room and puts it in state s ("" for none).
func post(t *testing.T, db *sql.DB, r *rooms.Rooms, room string, s delivery.State) int64 {
	t.Helper()
	id, err := r.Post(ctx, "builder", room, "hello", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE messages SET delivery = NULLIF(?, '') WHERE id = ?`, string(s), id); err != nil {
		t.Fatal(err)
	}
	return id
}

func stateOf(t *testing.T, db *sql.DB, id int64) (delivery.State, string) {
	t.Helper()
	var s, why string
	if err := db.QueryRowContext(ctx, `SELECT delivery, COALESCE(delivery_error, '') FROM messages WHERE id = ?`, id).Scan(&s, &why); err != nil {
		t.Fatal(err)
	}
	return delivery.State(s), why
}

func move(db *sql.DB, ids []int64, c delivery.Change) ([]delivery.Moved, error) {
	var moved []delivery.Moved
	err := store.InTx(ctx, db, func(tx *sql.Tx) error {
		var err error
		moved, err = delivery.Move(ctx, tx, ids, c)
		return err
	})
	return moved, err
}

var states = []delivery.State{delivery.Scheduled, delivery.Sending, delivery.Sent, delivery.Cancelled, delivery.Failed, delivery.Unconfirmed}

func TestMoveFollowsOnlyTheTable(t *testing.T) {
	t.Parallel()
	db, r := newBoard(t)
	allowed := map[[2]delivery.State]bool{ // written out, so a change of the table shows here
		{delivery.Scheduled, delivery.Sending}:   true,
		{delivery.Scheduled, delivery.Cancelled}: true,
		{delivery.Sending, delivery.Sent}:        true,
		{delivery.Sending, delivery.Failed}:      true,
		{delivery.Sending, delivery.Unconfirmed}: true,
	}
	for _, from := range states {
		for _, to := range states {
			id := post(t, db, r, "example-chat", from)
			moved, err := move(db, []int64{id}, delivery.Change{To: to, From: []delivery.State{from}})
			got, _ := stateOf(t, db, id)
			if allowed[[2]delivery.State{from, to}] {
				if err != nil || len(moved) != 1 || got != to {
					t.Errorf("%s → %s: moved %v, err %v, now %s", from, to, moved, err, got)
				}
			} else if err == nil || got != from {
				t.Errorf("%s → %s was allowed: now %s", from, to, got)
			}
			// without From, a move leaves every state the table allows and no other
			id = post(t, db, r, "example-chat", from)
			moved, err = move(db, []int64{id}, delivery.Change{To: to})
			if err != nil || len(moved) == 1 != allowed[[2]delivery.State{from, to}] {
				t.Errorf("%s → %s by the table: moved %v, err %v", from, to, moved, err)
			}
		}
	}
}

func TestMoveTakesOnlyItsRoomAndState(t *testing.T) {
	t.Parallel()
	db, r := newBoard(t)
	a := post(t, db, r, "example-chat", delivery.Sending)
	scheduled := post(t, db, r, "example-chat", delivery.Scheduled)
	other := post(t, db, r, "example-other", delivery.Sending)
	b := post(t, db, r, "example-chat", delivery.Sending)
	moved, err := move(db, []int64{b, other, scheduled, a}, delivery.Change{To: delivery.Failed, Room: "example-chat", Reason: "the chat is gone"})
	if err != nil {
		t.Fatal(err)
	}
	want := []delivery.Moved{{ID: a, Room: "example-chat", Author: "builder"}, {ID: b, Room: "example-chat", Author: "builder"}}
	if !slices.Equal(moved, want) {
		t.Fatalf("moved %+v, want %+v", moved, want)
	}
	for id, w := range map[int64][2]string{
		a: {"failed", "the chat is gone"}, b: {"failed", "the chat is gone"},
		other: {"sending", ""}, scheduled: {"scheduled", ""},
	} {
		if s, why := stateOf(t, db, id); string(s) != w[0] || why != w[1] {
			t.Errorf("message %d is %s (%q), want %s (%q)", id, s, why, w[0], w[1])
		}
	}
}

// hiddenMessages returns the messages hidden as an echo, in posting order.
func hiddenMessages(t *testing.T, db *sql.DB) []int64 {
	t.Helper()
	rows, err := db.QueryContext(ctx, `SELECT id FROM messages WHERE echo_of IS NOT NULL ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var hidden []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		hidden = append(hidden, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return hidden
}

func TestLeavingSendingShowsTheEchoHiddenForIt(t *testing.T) {
	t.Parallel()
	for _, to := range []delivery.State{delivery.Sent, delivery.Failed, delivery.Unconfirmed} {
		db, r := newBoard(t)
		sending := post(t, db, r, "example-chat", delivery.Sending)
		other := post(t, db, r, "example-chat", delivery.Sending)
		echo, echoOfOther := post(t, db, r, "example-chat", ""), post(t, db, r, "example-chat", "")
		if _, err := db.ExecContext(ctx, `UPDATE messages SET echo_of = CASE id WHEN ? THEN ? ELSE ? END WHERE id IN (?, ?)`,
			echo, sending, other, echo, echoOfOther); err != nil {
			t.Fatal(err)
		}
		if _, err := move(db, []int64{sending}, delivery.Change{To: to}); err != nil {
			t.Fatal(err)
		}
		if hidden := hiddenMessages(t, db); !slices.Equal(hidden, []int64{echoOfOther}) {
			t.Errorf("after %s the hidden messages are %v, want only %d", to, hidden, echoOfOther)
		}
	}
}
