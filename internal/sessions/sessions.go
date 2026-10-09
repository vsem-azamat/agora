// Package sessions keeps agent names and the agent sessions that connectors report: their
// state, the name bound to each, what the agent should be reminded of, and what an ended
// session gives back.
package sessions

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/vsem-azamat/agora/internal/queue"
)

// ToolCheckEvery limits how often tool use looks for something to tell the agent.
const ToolCheckEvery = 15 * time.Second

// Event is something a connector reports about a session.
type Event string

const (
	Start  Event = "start"
	Prompt Event = "prompt"
	Tool   Event = "tool"
	Stop   Event = "stop"
	End    Event = "end"
)

// State of a session.
type State string

const (
	Busy  State = "busy"
	Idle  State = "idle"
	Ended State = "ended"
)

// Report is one connector event.
type Report struct {
	SessionID  string
	Kind       string
	Event      Event
	PID        int
	CWD        string
	Terminal   string
	StopActive bool // the turn already continues because of an earlier block
}

// Reply is what the connector should tell the agent.
type Reply struct {
	Context     string // added to the agent's context; empty for none
	Block       bool   // for Stop: keep the turn going
	BlockReason string
}

// Session is a registered agent session.
type Session struct {
	ID        string
	Kind      string
	Agent     string
	State     State
	StateAt   time.Time
	StartedAt time.Time
	PID       int
	CWD       string
	Terminal  string
}

var (
	// ErrInvalid marks an invalid name or session identifier.
	ErrInvalid = errors.New("invalid request")
	// ErrNameTaken means the name is bound to another live session.
	ErrNameTaken = errors.New("name taken")
)

var (
	nameRE    = regexp.MustCompile(`^[a-z][a-z0-9-]{1,31}$`)
	sessionRE = regexp.MustCompile(`^[A-Za-z0-9_-]{6,80}$`)
	reserved  = map[string]bool{"all": true, "agora": true}
)

// CheckName reports why name cannot be an agent name, or nil.
func CheckName(name string) error {
	if reserved[name] {
		return fmt.Errorf("%w: %q is reserved", ErrInvalid, name)
	}
	if !nameRE.MatchString(name) {
		return fmt.Errorf("%w: name %q: 2-32 lowercase letters, digits and dashes, starting with a letter", ErrInvalid, name)
	}
	return nil
}

func checkSession(id string) error {
	if !sessionRE.MatchString(id) {
		return fmt.Errorf("%w: session id: 6-80 letters, digits, '_' and '-'", ErrInvalid)
	}
	return nil
}

// Sessions keeps names and sessions in the hub database.
type Sessions struct {
	db    *sql.DB
	queue *queue.Queue
	now   func() time.Time
}

// New returns Sessions over db; q is used for reminders and for releasing ended sessions'
// places; now is the clock (time.Now when nil).
func New(db *sql.DB, q *queue.Queue, now func() time.Time) *Sessions {
	if now == nil {
		now = time.Now
	}
	return &Sessions{db: db, queue: q, now: now}
}

// Report records one connector event and returns what to tell the agent.
func (s *Sessions) Report(ctx context.Context, r Report) (Reply, error) {
	if err := checkSession(r.SessionID); err != nil {
		return Reply{}, err
	}
	now := s.now()
	var agent, noted string
	var checkedAt int64
	err := s.tx(ctx, func(tx *sql.Tx) error {
		if err := register(ctx, tx, r, now); err != nil {
			return err
		}
		var a sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT agent, noted, checked_at FROM sessions WHERE id = ?`, r.SessionID).
			Scan(&a, &noted, &checkedAt); err != nil {
			return err
		}
		agent = a.String
		switch r.Event {
		case Start, Prompt:
			return setState(ctx, tx, r.SessionID, Busy, now)
		case End:
			return setState(ctx, tx, r.SessionID, Ended, now)
		}
		return nil
	})
	if err != nil {
		return Reply{}, err
	}
	switch r.Event {
	case End:
		return Reply{}, s.giveBack(ctx, agent)
	case Stop:
		return s.stop(ctx, r, agent, now)
	case Tool:
		if now.Sub(time.UnixMilli(checkedAt)) < ToolCheckEvery {
			return Reply{}, nil
		}
	}
	if agent == "" {
		return Reply{}, nil
	}
	entries, err := s.queue.EntriesOf(ctx, agent)
	if err != nil {
		return Reply{}, err
	}
	sig, offered := signature(entries), hasOffer(entries)
	if _, err := s.db.ExecContext(ctx, `UPDATE sessions SET noted = ?, checked_at = ? WHERE id = ?`, sig, now.UnixMilli(), r.SessionID); err != nil {
		return Reply{}, err
	}
	if len(entries) == 0 || (r.Event != Start && sig == noted && !offered) {
		return Reply{}, nil
	}
	return Reply{Context: note(agent, entries)}, nil
}

func (s *Sessions) stop(ctx context.Context, r Report, agent string, now time.Time) (Reply, error) {
	if agent != "" && !r.StopActive {
		entries, err := s.queue.EntriesOf(ctx, agent)
		if err != nil {
			return Reply{}, err
		}
		for _, e := range entries {
			if e.State == queue.Offered {
				return Reply{Block: true, BlockReason: offerReminder(agent, entries)}, nil
			}
		}
	}
	return Reply{}, s.tx(ctx, func(tx *sql.Tx) error { return setState(ctx, tx, r.SessionID, Idle, now) })
}

// Join binds name to session (when sessionID is not empty) and registers the name. Without
// force, a name bound to another session that has not ended is refused. It reports whether the
// name is now bound to a session.
func (s *Sessions) Join(ctx context.Context, name, sessionID string, force bool) (bool, error) {
	if err := CheckName(name); err != nil {
		return false, err
	}
	if sessionID != "" {
		if err := checkSession(sessionID); err != nil {
			return false, err
		}
	}
	now := s.now()
	bound := false
	err := s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO agents (name, joined_at) VALUES (?, ?) ON CONFLICT (name) DO NOTHING`, name, now.UnixMilli()); err != nil {
			return err
		}
		if sessionID == "" {
			return nil
		}
		var holder string
		err := tx.QueryRowContext(ctx, `SELECT id FROM sessions WHERE agent = ? AND state != 'ended' AND id != ?`, name, sessionID).Scan(&holder)
		switch {
		case err == nil && !force:
			return fmt.Errorf("%w: %q belongs to a live session; pick another name, or force the claim if it is you", ErrNameTaken, name)
		case err == nil:
			if _, err := tx.ExecContext(ctx, `UPDATE sessions SET agent = NULL WHERE id = ?`, holder); err != nil {
				return err
			}
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		if err := register(ctx, tx, Report{SessionID: sessionID, Kind: "unknown"}, now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE sessions SET agent = ?, noted = '' WHERE id = ?`, name, sessionID); err != nil {
			return err
		}
		bound = true
		return nil
	})
	return bound, err
}

// Resolve returns the name bound to a session that has not ended, or "".
func (s *Sessions) Resolve(ctx context.Context, sessionID string) (string, error) {
	if checkSession(sessionID) != nil {
		return "", nil
	}
	var agent sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT agent FROM sessions WHERE id = ? AND state != 'ended'`, sessionID).Scan(&agent)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return agent.String, err
}

// List returns sessions that have not ended, newest first.
func (s *Sessions) List(ctx context.Context) ([]Session, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, kind, COALESCE(agent, ''), state, state_at, started_at, pid, cwd, terminal
		FROM sessions WHERE state != 'ended' ORDER BY started_at DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		var x Session
		var stateAt, started int64
		if err := rows.Scan(&x.ID, &x.Kind, &x.Agent, &x.State, &stateAt, &started, &x.PID, &x.CWD, &x.Terminal); err != nil {
			return nil, err
		}
		x.StateAt, x.StartedAt = time.UnixMilli(stateAt), time.UnixMilli(started)
		out = append(out, x)
	}
	return out, rows.Err()
}

// EndDead ends every session whose process alive reports as gone, gives back the places of
// their agents, and returns the ended session identifiers. Sessions without a known process
// are left alone.
func (s *Sessions) EndDead(ctx context.Context, alive func(pid int) bool) ([]string, error) {
	live, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	var ended []string
	for _, x := range live {
		if x.PID <= 0 || alive(x.PID) {
			continue
		}
		now := s.now()
		if err := s.tx(ctx, func(tx *sql.Tx) error { return setState(ctx, tx, x.ID, Ended, now) }); err != nil {
			return ended, err
		}
		if err := s.giveBack(ctx, x.Agent); err != nil {
			return ended, err
		}
		ended = append(ended, x.ID)
	}
	return ended, nil
}

// giveBack removes agent from every queue unless it is bound to a session that has not ended.
func (s *Sessions) giveBack(ctx context.Context, agent string) error {
	if agent == "" {
		return nil
	}
	var live int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE agent = ? AND state != 'ended'`, agent).Scan(&live); err != nil {
		return err
	}
	if live > 0 {
		return nil
	}
	_, err := s.queue.ReleaseAgent(ctx, agent)
	return err
}

// --- internals -------------------------------------------------------------------

func (s *Sessions) tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// register records the session if it is new and refreshes the details the report carries.
func register(ctx context.Context, tx *sql.Tx, r Report, now time.Time) error {
	kind := r.Kind
	if kind == "" {
		kind = "unknown"
	}
	t := now.UnixMilli()
	_, err := tx.ExecContext(ctx, `INSERT INTO sessions (id, kind, pid, cwd, terminal, state, state_at, started_at)
		VALUES (?, ?, ?, ?, ?, 'busy', ?, ?)
		ON CONFLICT (id) DO UPDATE SET
			kind = CASE WHEN excluded.kind != 'unknown' THEN excluded.kind ELSE kind END,
			pid = CASE WHEN excluded.pid > 0 THEN excluded.pid ELSE pid END,
			cwd = CASE WHEN excluded.cwd != '' THEN excluded.cwd ELSE cwd END,
			terminal = CASE WHEN excluded.terminal != '' THEN excluded.terminal ELSE terminal END,
			agent = CASE WHEN state = 'ended' AND EXISTS (SELECT 1 FROM sessions AS other
				WHERE other.agent = sessions.agent AND other.state != 'ended' AND other.id != sessions.id) THEN NULL ELSE agent END,
			state = CASE WHEN state = 'ended' THEN 'busy' ELSE state END`,
		r.SessionID, kind, r.PID, r.CWD, r.Terminal, t, t)
	return err
}

func setState(ctx context.Context, tx *sql.Tx, id string, st State, now time.Time) error {
	_, err := tx.ExecContext(ctx, `UPDATE sessions SET state = ?, state_at = ? WHERE id = ? AND state != ?`, st, now.UnixMilli(), id, st)
	return err
}

func hasOffer(entries []queue.Entry) bool {
	for _, e := range entries {
		if e.State == queue.Offered {
			return true
		}
	}
	return false
}

// signature identifies the agent's places without times, to tell whether they changed.
func signature(entries []queue.Entry) string {
	parts := make([]string, 0, len(entries))
	for _, e := range entries {
		parts = append(parts, fmt.Sprintf("%s:%s:%d", e.Key, e.State, e.Position))
	}
	return strings.Join(parts, " ")
}

func clock(t time.Time) string { return t.Local().Format("15:04") }

func note(agent string, entries []queue.Entry) string {
	var held, waiting, offered []string
	for _, e := range entries {
		switch e.State {
		case queue.Held:
			held = append(held, fmt.Sprintf("%s until %s", e.Key, clock(e.Expires)))
		case queue.Waiting:
			waiting = append(waiting, fmt.Sprintf("%s at position %d", e.Key, e.Position))
		case queue.Offered:
			offered = append(offered, fmt.Sprintf("%s: claim it by %s with `agora queue renew %s`, or give it up with `agora queue release %s`",
				e.Key, clock(e.Expires), e.Key, e.Key))
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Agora: you are %s.", agent)
	for _, o := range offered {
		fmt.Fprintf(&b, " It is your turn on %s.", o)
	}
	if len(held) > 0 {
		fmt.Fprintf(&b, " You hold %s; release with `agora queue release <key>` when done.", strings.Join(held, ", "))
	}
	if len(waiting) > 0 {
		fmt.Fprintf(&b, " You wait for %s.", strings.Join(waiting, ", "))
	}
	return b.String()
}

func offerReminder(agent string, entries []queue.Entry) string {
	return note(agent, entries) + " Claim or release the offered slot before you end your turn; otherwise it passes to the next agent."
}
