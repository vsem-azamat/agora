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

	"github.com/vsem-azamat/agora/internal/agents"
	"github.com/vsem-azamat/agora/internal/queue"
	"github.com/vsem-azamat/agora/internal/rooms"
)

const (
	// ToolCheckEvery limits how often tool use looks for something to tell the agent.
	ToolCheckEvery = 15 * time.Second
	// DeliverAtOnce is how many unread messages one hook adds to the agent's context.
	DeliverAtOnce = 5
	// DeliverChars is how long each delivered message may be.
	DeliverChars = 700
	// UnknownProcessTimeout ends a session whose process is unknown after this long without
	// events from it.
	UnknownProcessTimeout = 6 * time.Hour
)

// Event is something a connector reports about a session.
type Event string

const (
	Start  Event = "start"
	Prompt Event = "prompt"
	Tool   Event = "tool"
	Stop   Event = "stop"
	End    Event = "end"
)

// EndReasonClear is the end reason of a session replaced by a new conversation in the same
// process; the name and places move to the new session when it starts.
const EndReasonClear = "clear"

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
	PIDStart   int64 // process start time; 0 when unknown
	CWD        string
	Terminal   string
	StopActive bool   // the turn already continues because of an earlier block
	Reason     string // why the session ends, for End
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
	SeenAt    time.Time
	PID       int
	PIDStart  int64
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
	rooms *rooms.Rooms
	now   func() time.Time
}

// New returns Sessions over db; q is used for reminders and for releasing ended sessions'
// places, r for delivering messages; now is the clock (time.Now when nil).
func New(db *sql.DB, q *queue.Queue, r *rooms.Rooms, now func() time.Time) *Sessions {
	if now == nil {
		now = time.Now
	}
	return &Sessions{db: db, queue: q, rooms: r, now: now}
}

// Report records one connector event and returns what to tell the agent.
func (s *Sessions) Report(ctx context.Context, r Report) (Reply, error) {
	if err := checkSession(r.SessionID); err != nil {
		return Reply{}, err
	}
	if r.Event == End && r.Reason == EndReasonClear {
		return Reply{}, nil // the next start in the same process takes over the name and places
	}
	now := s.now()
	var agent string
	err := s.tx(ctx, func(tx *sql.Tx) error {
		if err := register(ctx, tx, r, now); err != nil {
			return err
		}
		if r.Event == Start {
			if err := takeOver(ctx, tx, r, now); err != nil {
				return err
			}
		}
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(agent, '') FROM sessions WHERE id = ?`, r.SessionID).Scan(&agent); err != nil {
			return err
		}
		if agent != "" && r.Event != End {
			if err := agents.ReturnTx(ctx, tx, agent, now); err != nil {
				return err
			}
			if err := agents.FollowTx(ctx, tx, agent, r.CWD, now); err != nil {
				return err
			}
		}
		switch r.Event {
		case Start, Prompt:
			if _, err := tx.ExecContext(ctx, `UPDATE sessions SET turn = turn + 1 WHERE id = ?`, r.SessionID); err != nil {
				return err
			}
			return setState(ctx, tx, r.SessionID, Busy, now)
		case End:
			return s.endTx(ctx, tx, r.SessionID, now)
		}
		return nil
	})
	if err != nil || r.Event == End {
		return Reply{}, err
	}
	if r.Event == Stop {
		return s.stop(ctx, r, agent, now)
	}
	if agent == "" {
		if r.Event == Start {
			return Reply{Context: invitation(r.CWD)}, nil
		}
		return Reply{}, nil
	}
	entries, lost, show, checked, err := s.remind(ctx, r, agent, now)
	if err != nil || !checked {
		return Reply{}, err
	}
	var text string
	switch {
	case r.Event == Start:
		if text, err = s.reminder(ctx, agent, entries, lost); err != nil {
			return Reply{}, err
		}
	case show:
		text = note(agent, entries, lost)
	}
	news, err := s.deliver(ctx, agent)
	if err != nil {
		return Reply{}, err
	}
	return Reply{Context: strings.TrimSpace(text + "\n\n" + news)}, nil
}

// deliver returns the agent's oldest unread messages as context text and marks the ones shown
// as read.
func (s *Sessions) deliver(ctx context.Context, agent string) (string, error) {
	if s.rooms == nil {
		return "", nil
	}
	msgs, total, err := s.rooms.Take(ctx, agent, false, DeliverAtOnce)
	if err != nil || len(msgs) == 0 {
		return "", err
	}
	lines := []string{fmt.Sprintf("Agora: new board messages for you (%s). Act on what is addressed to you "+
		"(reply: agora post <room> '...' --reply <id>); otherwise note them and carry on.", agent)}
	for _, m := range msgs {
		lines = append(lines, rooms.Format(m, DeliverChars))
	}
	if more := total - len(msgs); more > 0 {
		lines = append(lines, fmt.Sprintf("... %d more: run `agora unread`.", more))
	}
	return strings.Join(lines, "\n"), nil
}

// remind reports the agent's places, the places it lost since the last note, and whether a
// note about them is due: when they changed since the last note, when a slot is offered, or at
// session start. The check is recorded with a compare-and-set, so concurrent hooks of one
// session see checked once.
func (s *Sessions) remind(ctx context.Context, r Report, agent string, now time.Time) (entries []queue.Entry, lost []string, show, checked bool, err error) {
	entries, err = s.queue.EntriesOf(ctx, agent)
	if err != nil {
		return nil, nil, false, false, err
	}
	sig := signature(entries)
	err = s.tx(ctx, func(tx *sql.Tx) error {
		var noted string
		var checkedAt int64
		if err := tx.QueryRowContext(ctx, `SELECT noted, checked_at FROM sessions WHERE id = ?`, r.SessionID).Scan(&noted, &checkedAt); err != nil {
			return err
		}
		if r.Event == Tool && now.Sub(time.UnixMilli(checkedAt)) < ToolCheckEvery {
			return nil
		}
		res, err := tx.ExecContext(ctx, `UPDATE sessions SET noted = ?, checked_at = ? WHERE id = ? AND noted = ? AND checked_at = ?`,
			sig, now.UnixMilli(), r.SessionID, noted, checkedAt)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil // a concurrent hook of this session got there first
		}
		checked = true
		lost = lostKeys(noted, entries)
		if len(entries) == 0 && len(lost) == 0 {
			return nil
		}
		show = r.Event == Start || sig != noted || hasOffer(entries)
		return nil
	})
	return entries, lost, show, checked, err
}

func (s *Sessions) stop(ctx context.Context, r Report, agent string, now time.Time) (Reply, error) {
	if agent != "" && !r.StopActive {
		text, err := s.waiting(ctx, agent, "before you end your turn")
		if err != nil {
			return Reply{}, err
		}
		if text != "" {
			return Reply{Block: true, BlockReason: text}, nil
		}
	}
	return Reply{}, s.tx(ctx, func(tx *sql.Tx) error { return setState(ctx, tx, r.SessionID, Idle, now) })
}

// waiting returns the text for what needs the agent now (unread messages addressed to it,
// which it marks read, and offered slots), or "" when nothing does. when says when the agent
// should act, e.g. "before you end your turn".
func (s *Sessions) waiting(ctx context.Context, agent, when string) (string, error) {
	var parts []string
	if s.rooms != nil {
		msgs, total, err := s.rooms.Take(ctx, agent, true, DeliverAtOnce)
		if err != nil {
			return "", err
		}
		if len(msgs) > 0 {
			lines := []string{fmt.Sprintf("Agora: %s, answer what is addressed to you (%s), "+
				"even with \"not me\" or \"later\": agora post <room> '...' --reply <id>.", when, agent)}
			for _, m := range msgs {
				lines = append(lines, rooms.Format(m, DeliverChars))
			}
			if more := total - len(msgs); more > 0 {
				lines = append(lines, fmt.Sprintf("... %d more addressed to you: run `agora unread`.", more))
			}
			parts = append(parts, strings.Join(lines, "\n"))
		}
	}
	entries, err := s.queue.EntriesOf(ctx, agent)
	if err != nil {
		return "", err
	}
	if hasOffer(entries) {
		parts = append(parts, note(agent, entries, nil)+
			" Claim or release the offered slot now; otherwise it passes to the next agent.")
	}
	return strings.Join(parts, "\n\n"), nil
}

// Turn returns the session's turn count, which grows with every prompt and start.
func (s *Sessions) Turn(ctx context.Context, sessionID string) (int64, error) {
	var turn int64
	err := s.db.QueryRowContext(ctx, `SELECT turn FROM sessions WHERE id = ?`, sessionID).Scan(&turn)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("%w: unknown session", ErrInvalid)
	}
	return turn, err
}

// Wake is what a connector wait delivers: the text, the addressed messages it shows (marked
// read only once the wake is delivered) and a key of what it woke for.
type Wake struct {
	Text     string
	Messages []rooms.Message
	Key      string
	Turn     int64
}

// CheckWake decides a connector's wait for a session that started at turn: done with no wake
// when the session ended, a new turn began or the session lost its agent; done with a wake when
// the session is idle and something new needs its agent (an addressed message, or an offered slot
// it was not already woken for); otherwise keep waiting. Nothing is consumed until ConfirmWake.
func (s *Sessions) CheckWake(ctx context.Context, sessionID string, turn int64) (w *Wake, done bool, err error) {
	var state, agent, wokenFor string
	var now int64
	err = s.db.QueryRowContext(ctx, `SELECT state, COALESCE(agent, ''), turn, woken_for FROM sessions WHERE id = ?`, sessionID).
		Scan(&state, &agent, &now, &wokenFor)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, true, nil
	case err != nil:
		return nil, false, err
	case state == string(Ended) || now != turn:
		return nil, true, nil
	case state != string(Idle):
		return nil, false, nil
	case agent == "":
		return nil, true, nil
	}
	var msgs []rooms.Message
	if s.rooms != nil {
		if msgs, _, err = s.rooms.Unread(ctx, agent, true, DeliverAtOnce); err != nil {
			return nil, false, err
		}
	}
	entries, err := s.queue.EntriesOf(ctx, agent)
	if err != nil {
		return nil, false, err
	}
	key := offerKey(entries)
	if len(msgs) == 0 && (key == "" || key == wokenFor) {
		return nil, false, nil
	}
	return &Wake{Text: wakeText(agent, msgs, entries), Messages: msgs, Key: key, Turn: turn}, true, nil
}

// ConfirmWake records a delivered wake: its messages are read, the session is busy with a new
// turn (the woken agent works), and the offers it woke for are remembered so they do not wake
// the agent again.
func (s *Sessions) ConfirmWake(ctx context.Context, sessionID string, w *Wake) error {
	if s.rooms != nil && len(w.Messages) > 0 {
		var agent string
		if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(agent, '') FROM sessions WHERE id = ?`, sessionID).Scan(&agent); err != nil {
			return err
		}
		if err := s.rooms.MarkEach(ctx, agent, w.Messages); err != nil {
			return err
		}
	}
	now := s.now().UnixMilli()
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET state = 'busy', state_at = ?, turn = turn + 1, woken_for = ?, woken_at = ?
		WHERE id = ? AND turn = ? AND state = 'idle'`, now, w.Key, now, sessionID, w.Turn)
	return err
}

// Now is the clock Sessions uses.
func (s *Sessions) Now() time.Time { return s.now() }

func offerKey(entries []queue.Entry) string {
	var keys []string
	for _, e := range entries {
		if e.State == queue.Offered {
			keys = append(keys, "o"+e.Key)
		}
	}
	return strings.Join(keys, " ")
}

func wakeText(agent string, msgs []rooms.Message, entries []queue.Entry) string {
	var parts []string
	if len(msgs) > 0 {
		lines := []string{fmt.Sprintf("Agora: you were woken; answer what is addressed to you (%s), "+
			"even with \"not me\" or \"later\": agora post <room> '...' --reply <id>.", agent)}
		for _, m := range msgs {
			lines = append(lines, rooms.Format(m, DeliverChars))
		}
		parts = append(parts, strings.Join(lines, "\n"))
	}
	if hasOffer(entries) {
		parts = append(parts, note(agent, entries, nil)+" Claim or release the offered slot now; otherwise it passes to the next agent.")
	}
	return strings.Join(parts, "\n\n")
}

// Pending describes what would wake a session's agent without consuming it: a key that
// changes when something new arrives (the newest addressed message and the offered slots) and
// a short text for a terminal prompt. key is "" when nothing needs the agent.
func (s *Sessions) Pending(ctx context.Context, agent string) (key, text string, err error) {
	var parts, keys []string
	if s.rooms != nil {
		msgs, _, err := s.rooms.Unread(ctx, agent, true, 0)
		if err != nil {
			return "", "", err
		}
		if len(msgs) > 0 {
			from := map[string]bool{}
			var senders []string
			for _, m := range msgs {
				who := fmt.Sprintf("%s in #%s", m.Author, m.Room)
				if !from[who] {
					from[who] = true
					senders = append(senders, who)
				}
			}
			keys = append(keys, fmt.Sprintf("m%d", msgs[len(msgs)-1].ID))
			parts = append(parts, fmt.Sprintf("%d board message(s) addressed to you (%s)", len(msgs), strings.Join(senders, ", ")))
		}
	}
	entries, err := s.queue.EntriesOf(ctx, agent)
	if err != nil {
		return "", "", err
	}
	for _, e := range entries {
		if e.State == queue.Offered {
			keys = append(keys, "o"+e.Key)
			parts = append(parts, "your turn on "+e.Key)
		}
	}
	if len(keys) == 0 {
		return "", "", nil
	}
	return strings.Join(keys, " "), fmt.Sprintf("Agora: %s. Read them with agora unread and agora queue ls, act if needed, then carry on or end your turn.",
		strings.Join(parts, "; ")), nil
}

// CommandWake is an idle session that a wake command may wake.
type CommandWake struct {
	SessionID, Agent, Terminal, WokenFor string
	WokenAt                              time.Time
	PID                                  int
	PIDStart                             int64
}

// IdleWithTerminal returns sessions with a bound agent, a known terminal and a known process
// that have been idle for at least settle (so a connector that is about to wait gets there
// first).
func (s *Sessions) IdleWithTerminal(ctx context.Context, settle time.Duration) ([]CommandWake, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, agent, terminal, woken_for, woken_at, pid, pid_start FROM sessions
		WHERE state = 'idle' AND agent IS NOT NULL AND terminal != '' AND pid > 0 AND state_at <= ? ORDER BY id`,
		s.now().Add(-settle).UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CommandWake
	for rows.Next() {
		var c CommandWake
		var at int64
		if err := rows.Scan(&c.SessionID, &c.Agent, &c.Terminal, &c.WokenFor, &at, &c.PID, &c.PIDStart); err != nil {
			return nil, err
		}
		c.WokenAt = time.UnixMilli(at)
		out = append(out, c)
	}
	return out, rows.Err()
}

// RecordWake stores how a command wake went; only a successful wake remembers what it was for,
// so a failed one is retried after the gap.
func (s *Sessions) RecordWake(ctx context.Context, sessionID, key, result string, ok bool) error {
	if ok {
		_, err := s.db.ExecContext(ctx, `UPDATE sessions SET woken_for = ?, woken_at = ?, wake_result = ? WHERE id = ?`,
			key, s.now().UnixMilli(), result, sessionID)
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET woken_at = ?, wake_result = ? WHERE id = ?`, s.now().UnixMilli(), result, sessionID)
	return err
}

// Join registers name and binds it to sessionID when that session has not ended; without
// force, a name bound to another live session is refused. It reports whether the name is now
// bound to the session.
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
		if _, err := tx.ExecContext(ctx, `INSERT INTO agents (name, joined_at, read_from) VALUES (?, ?, (SELECT COALESCE(MAX(id), 0) FROM messages))
			ON CONFLICT (name) DO NOTHING`, name, now.UnixMilli()); err != nil {
			return err
		}
		if sessionID == "" {
			return nil
		}
		var state string
		switch err := tx.QueryRowContext(ctx, `SELECT state FROM sessions WHERE id = ?`, sessionID).Scan(&state); {
		case errors.Is(err, sql.ErrNoRows):
			t := now.UnixMilli()
			if _, err := tx.ExecContext(ctx, `INSERT INTO sessions (id, kind, state, state_at, started_at, seen_at) VALUES (?, 'unknown', 'busy', ?, ?, ?)`,
				sessionID, t, t, t); err != nil {
				return err
			}
		case err != nil:
			return err
		case state == string(Ended):
			return nil // an ended session is not brought back; the name is only registered
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
	rows, err := s.db.QueryContext(ctx, `SELECT id, kind, COALESCE(agent, ''), state, state_at, started_at, seen_at, pid, pid_start, cwd, terminal
		FROM sessions WHERE state != 'ended' ORDER BY started_at DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		var x Session
		var stateAt, started, seen int64
		if err := rows.Scan(&x.ID, &x.Kind, &x.Agent, &x.State, &stateAt, &started, &seen, &x.PID, &x.PIDStart, &x.CWD, &x.Terminal); err != nil {
			return nil, err
		}
		x.StateAt, x.StartedAt, x.SeenAt = time.UnixMilli(stateAt), time.UnixMilli(started), time.UnixMilli(seen)
		out = append(out, x)
	}
	return out, rows.Err()
}

// EndDead ends sessions whose process is gone, as alive reports for a pid and its start time,
// and sessions with an unknown process that sent nothing for UnknownProcessTimeout. Each end
// gives back the agent's places in the same transaction. It returns the ended identifiers.
func (s *Sessions) EndDead(ctx context.Context, alive func(pid int, start int64) bool) ([]string, error) {
	live, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	now := s.now()
	var ended []string
	for _, x := range live {
		switch {
		case x.PID > 0 && alive(x.PID, x.PIDStart):
			continue
		case x.PID <= 0 && now.Sub(x.SeenAt) < UnknownProcessTimeout:
			continue
		}
		done := false
		err := s.tx(ctx, func(tx *sql.Tx) error {
			// only if nothing re-registered the session with another process meanwhile
			var same int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE id = ? AND state != 'ended' AND pid = ? AND pid_start = ? AND seen_at = ?`,
				x.ID, x.PID, x.PIDStart, x.SeenAt.UnixMilli()).Scan(&same); err != nil || same == 0 {
				return err
			}
			done = true
			return s.endTx(ctx, tx, x.ID, now)
		})
		if err != nil {
			return ended, err
		}
		if done {
			ended = append(ended, x.ID)
		}
	}
	return ended, nil
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

// endTx ends the session and, unless its agent is bound to another live session, removes the
// agent from every resource queue, all in tx.
func (s *Sessions) endTx(ctx context.Context, tx *sql.Tx, id string, now time.Time) error {
	var agent string
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(agent, '') FROM sessions WHERE id = ?`, id).Scan(&agent); err != nil {
		return err
	}
	if err := setState(ctx, tx, id, Ended, now); err != nil {
		return err
	}
	if agent == "" {
		return nil
	}
	var live int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE agent = ? AND state != 'ended'`, agent).Scan(&live); err != nil {
		return err
	}
	if live > 0 {
		return nil
	}
	if err := agents.MarkLeftTx(ctx, tx, agent, now); err != nil {
		return err
	}
	_, err := s.queue.ReleaseAgentTx(ctx, tx, agent)
	return err
}

// register records the session if it is new and refreshes what the report carries. A report
// from an ended session brings it back; its name stays only if no live session took it.
func register(ctx context.Context, tx *sql.Tx, r Report, now time.Time) error {
	kind := r.Kind
	if kind == "" {
		kind = "unknown"
	}
	t := now.UnixMilli()
	_, err := tx.ExecContext(ctx, `INSERT INTO sessions (id, kind, pid, pid_start, cwd, terminal, state, state_at, started_at, seen_at)
		VALUES (?, ?, ?, ?, ?, ?, 'busy', ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET
			kind = CASE WHEN excluded.kind != 'unknown' THEN excluded.kind ELSE kind END,
			pid = CASE WHEN excluded.pid > 0 THEN excluded.pid ELSE pid END,
			pid_start = CASE WHEN excluded.pid > 0 THEN excluded.pid_start ELSE pid_start END,
			cwd = CASE WHEN excluded.cwd != '' THEN excluded.cwd ELSE cwd END,
			terminal = CASE WHEN excluded.terminal != '' THEN excluded.terminal ELSE terminal END,
			seen_at = excluded.seen_at,
			agent = CASE WHEN state = 'ended' AND EXISTS (SELECT 1 FROM sessions AS other
				WHERE other.agent = sessions.agent AND other.state != 'ended' AND other.id != sessions.id) THEN NULL ELSE agent END,
			state = CASE WHEN state = 'ended' THEN 'busy' ELSE state END`,
		r.SessionID, kind, r.PID, r.PIDStart, r.CWD, r.Terminal, t, t, t)
	return err
}

// takeOver moves the name of a live session in the same process (same pid and start time) to
// the starting session, and ends the old one without giving back places: the agent tool started
// a new conversation in the process it already ran, as on /clear.
func takeOver(ctx context.Context, tx *sql.Tx, r Report, now time.Time) error {
	if r.PID <= 0 {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT id, COALESCE(agent, '') FROM sessions
		WHERE pid = ? AND pid_start = ? AND state != 'ended' AND id != ? ORDER BY started_at DESC`, r.PID, r.PIDStart, r.SessionID)
	if err != nil {
		return err
	}
	type old struct{ id, agent string }
	var olds []old
	for rows.Next() {
		var o old
		if err := rows.Scan(&o.id, &o.agent); err != nil {
			rows.Close()
			return err
		}
		olds = append(olds, o)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, o := range olds {
		if _, err := tx.ExecContext(ctx, `UPDATE sessions SET agent = NULL, state = 'ended', state_at = ? WHERE id = ?`, now.UnixMilli(), o.id); err != nil {
			return err
		}
		if o.agent == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE sessions SET agent = ?, noted = '' WHERE id = ? AND agent IS NULL`, o.agent, r.SessionID); err != nil {
			return err
		}
	}
	return nil
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

// lostKeys returns the resources in the previous signature that the agent no longer has.
func lostKeys(prev string, entries []queue.Entry) []string {
	now := map[string]bool{}
	for _, e := range entries {
		now[e.Key] = true
	}
	var lost []string
	for _, part := range strings.Fields(prev) {
		key := part[:strings.Index(part, ":")]
		if !now[key] {
			lost = append(lost, key)
		}
	}
	return lost
}

func clock(t time.Time) string { return t.Local().Format("15:04") }

func note(agent string, entries []queue.Entry, lost []string) string {
	return strings.TrimSpace(fmt.Sprintf("Agora: you are %s. %s", agent, places(entries, lost)))
}

// places describes the agent's places in queues and the ones it lost, or "" for none.
func places(entries []queue.Entry, lost []string) string {
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
	var parts []string
	for _, o := range offered {
		parts = append(parts, fmt.Sprintf("It is your turn on %s.", o))
	}
	if len(held) > 0 {
		parts = append(parts, fmt.Sprintf("You hold %s; release with `agora queue release <key>` when done.", strings.Join(held, ", ")))
	}
	if len(waiting) > 0 {
		parts = append(parts, fmt.Sprintf("You wait for %s.", strings.Join(waiting, ", ")))
	}
	if len(lost) > 0 {
		parts = append(parts, fmt.Sprintf("You no longer hold or wait for %s (lease ended, turn missed or removed).", strings.Join(lost, ", ")))
	}
	return strings.Join(parts, " ")
}
