package sessions

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"time"
)

// A session is created busy (a report or a join from a session the hub did not know) and moves
// between its states only through transition, which alone writes state and state_at; a test
// enforces that. moves lists, for each state, the states a session may enter it from.
var moves = map[State][]State{
	Busy:  {Idle, Ended}, // a prompt or a wake starts a turn; a report from an ended session brings it back
	Idle:  {Busy},        // the turn ended, failed or was interrupted, or the session waits at its prompt
	Ended: {Busy, Idle},  // the session ended, its process is gone, or another session took over its process
}

// created is the state a session row starts in.
const created = Busy

// change is one transition: the session enters to from one of from (every state in moves[to]
// when from is empty), at turn only when atTurn is set, and starts a new turn when newTurn is set.
type change struct {
	to      State
	from    []State
	atTurn  bool
	turn    int64
	newTurn bool
}

// transition applies c to the session id and reports whether it moved. A session already in
// c.to, or in a state c does not leave, or at another turn, does not move, and keeps its state_at.
func transition(ctx context.Context, tx *sql.Tx, id string, c change, now time.Time) (bool, error) {
	allowed := moves[c.to]
	from := c.from
	if len(from) == 0 {
		from = allowed
	}
	for _, f := range from {
		if !slices.Contains(allowed, f) {
			return false, fmt.Errorf("session state %s cannot become %s", f, c.to)
		}
	}
	states, err := json.Marshal(from)
	if err != nil {
		return false, err
	}
	var turn any // NULL: at any turn
	if c.atTurn {
		turn = c.turn
	}
	step := 0
	if c.newTurn {
		step = 1
	}
	res, err := tx.ExecContext(ctx, `UPDATE sessions SET state = :to, state_at = :at, turn = turn + :step
		WHERE id = :id AND state IN (SELECT value FROM json_each(:from)) AND (:turn IS NULL OR turn = :turn)`,
		sql.Named("to", c.to), sql.Named("at", now.UnixMilli()), sql.Named("step", step),
		sql.Named("id", id), sql.Named("from", string(states)), sql.Named("turn", turn))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// enter moves the session id to state to from any state moves allows.
func enter(ctx context.Context, tx *sql.Tx, id string, to State, now time.Time) error {
	_, err := transition(ctx, tx, id, change{to: to}, now)
	return err
}
