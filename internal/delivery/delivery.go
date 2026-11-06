// Package delivery owns where a message of a bridged room stands on its way out: its delivery
// state, why it did not go out, and since when it is scheduled. A new message takes the state
// Initial gives it, and moves between states only through Move, along its table of moves; a
// test fails when code elsewhere writes those columns.
package delivery

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/vsem-azamat/agora/internal/store"
)

// ErrReadOnly refuses an agent's post in a bridged room whose outbound policy is read.
var ErrReadOnly = errors.New("read-only bridge")

// State is where a message of a bridged room stands on its way out.
type State string

// Delivery states.
const (
	Scheduled State = "scheduled" // goes out once the delay passed, unless the operator cancels it
	Sending   State = "sending"   // the bridge has to send it
	Sent      State = "sent"      // the bridge sent it
	Cancelled State = "cancelled" // the operator cancelled it
	Failed    State = "failed"    // the bridge could not send it
	// Unconfirmed is a message handed to a bridge that was removed before it answered: it may have gone out.
	Unconfirmed State = "unconfirmed"
)

// moves lists, for each state, the states a message may enter it from. A new message starts
// scheduled or sending (see Initial), or without a state when it never goes out.
var moves = map[State][]State{
	Sending:     {Scheduled}, // its delay passed, or the operator sent it at once
	Sent:        {Sending},   // the bridge sent it
	Failed:      {Sending},   // the bridge could not send it
	Unconfirmed: {Sending},   // its bridge was removed before it answered
	Cancelled:   {Scheduled}, // the operator cancelled it
}

// How is how a new message of a bridged room may leave Agora.
type How int

// The ways a message may leave.
const (
	AsAgent    How = iota // under the outbound policy
	AsOperator            // at once, except under read, where it is a note
	AsNote                // never: a note
)

// Initial returns the state a new message posted as how gets in room under its bridge's policy,
// "" in a room without a bridge, and whether it is a note, which only a bridged room has. An
// agent's message in a room bridged read-only is refused.
func Initial(ctx context.Context, tx *sql.Tx, room string, how How) (State, bool, error) {
	var policy string
	err := tx.QueryRowContext(ctx, `SELECT policy FROM bridges WHERE name = ?`, room).Scan(&policy)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", false, nil
	case err != nil:
		return "", false, err
	case how == AsNote, policy == "read" && how == AsOperator:
		// what the operator writes in a read-only room stays in Agora: a note
		return "", true, nil
	case policy == "read":
		return "", false, store.Refuse(ErrReadOnly, "#%s is bridged read-only: nothing goes out, and agents post only notes there (--note)", room)
	case policy == "open" || how == AsOperator:
		return Sending, false, nil
	}
	return Scheduled, false, nil
}

// Change is one move: the messages enter To from one of From (every state moves allows into To
// when From is empty), only in Room when it is set; a message that fails keeps Reason.
type Change struct {
	To     State
	From   []State
	Room   string
	Reason string
}

// Moved is a message a Change moved.
type Moved struct {
	ID     int64
	Room   string
	Author string
}

// Move applies c to the messages ids and returns those that moved, in posting order; the others
// are not in a state c leaves, or not in its room. A message that leaves sending releases what
// was hidden as its echo, which is a message of its own unless the answer merges it.
func Move(ctx context.Context, tx *sql.Tx, ids []int64, c Change) ([]Moved, error) {
	allowed := moves[c.To]
	from := c.From
	if len(from) == 0 {
		from = allowed
	}
	for _, f := range from {
		if !slices.Contains(allowed, f) {
			return nil, fmt.Errorf("delivery state %q cannot become %q", f, c.To)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	states, err := json.Marshal(from)
	if err != nil {
		return nil, err
	}
	list, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	var why any
	if c.Reason != "" {
		why = c.Reason
	}
	rows, err := tx.QueryContext(ctx, `UPDATE messages SET delivery = :to, delivery_error = :why
		WHERE id IN (SELECT value FROM json_each(:ids)) AND delivery IN (SELECT value FROM json_each(:from))
			AND (:room = '' OR room = :room)
		RETURNING id, room, author`,
		sql.Named("to", string(c.To)), sql.Named("why", why), sql.Named("ids", string(list)),
		sql.Named("from", string(states)), sql.Named("room", c.Room))
	if err != nil {
		return nil, err
	}
	out, err := scanMoved(rows)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(out, func(a, b Moved) int { return cmp.Compare(a.ID, b.ID) })
	if len(out) == 0 || !slices.Contains(from, Sending) {
		return out, nil
	}
	moved := make([]int64, len(out))
	for i, m := range out {
		moved[i] = m.ID
	}
	if list, err = json.Marshal(moved); err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE messages SET echo_of = NULL WHERE echo_of IN (SELECT value FROM json_each(?))`, string(list))
	return out, err
}

func scanMoved(rows *sql.Rows) ([]Moved, error) {
	defer rows.Close()
	var out []Moved
	for rows.Next() {
		var m Moved
		if err := rows.Scan(&m.ID, &m.Room, &m.Author); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ScheduledBy returns the messages of room scheduled at or before due, in posting order.
func ScheduledBy(ctx context.Context, tx *sql.Tx, room string, due time.Time) ([]int64, error) {
	return ids(ctx, tx, `SELECT id FROM messages WHERE room = ? AND delivery = 'scheduled' AND scheduled_at <= ? ORDER BY id`, room, due.UnixMilli())
}

// InState returns the messages of room in state s, in posting order.
func InState(ctx context.Context, tx *sql.Tx, room string, s State) ([]int64, error) {
	return ids(ctx, tx, `SELECT id FROM messages WHERE room = ? AND delivery = ? ORDER BY id`, room, string(s))
}

// Reschedule starts the delay of the messages of room still scheduled again at now.
func Reschedule(ctx context.Context, tx *sql.Tx, room string, now time.Time) error {
	_, err := tx.ExecContext(ctx, `UPDATE messages SET scheduled_at = ? WHERE room = ? AND delivery = 'scheduled'`, now.UnixMilli(), room)
	return err
}

func ids(ctx context.Context, tx *sql.Tx, query string, args ...any) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
