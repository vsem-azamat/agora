package sessions

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/vsem-azamat/agora/internal/agents"
	"github.com/vsem-azamat/agora/internal/rooms"
	"github.com/vsem-azamat/agora/internal/store"
)

// MaxFocus is the longest focus of a compact, in characters.
const MaxFocus = 300

// AfterCompact is how long a session is not woken by a command once its compact command ran.
const AfterCompact = 2 * time.Minute

// kindClaudeCode is the kind of sessions whose connector wakes them through a hook.
const kindClaudeCode = "claude-code"

// ErrRefused means a session cannot do what was asked now; the error says why.
var ErrRefused = errors.New("refused")

// Mechanism is how the hub does an action for a session.
type Mechanism string

// Mechanisms.
const (
	ViaHook     Mechanism = "hook"     // the session's connector does it
	ViaTerminal Mechanism = "terminal" // the hub runs its command for the session's terminal
	ViaNothing  Mechanism = "none"
)

// Commands says which terminal commands the hub was given.
type Commands struct{ Wake, Compact, Interrupt bool }

// Capabilities is how each action can be done for a session.
type Capabilities struct{ Wake, Compact, Interrupt Mechanism }

// CapabilitiesOf derives what the hub can do for a session of kind with the given terminal and
// process, from the commands it was given.
func CapabilitiesOf(c Commands, kind, terminal string, pid int) Capabilities {
	viaTerminal := func(has bool) Mechanism {
		if has && terminal != "" && pid > 0 {
			return ViaTerminal
		}
		return ViaNothing
	}
	caps := Capabilities{Wake: viaTerminal(c.Wake), Compact: viaTerminal(c.Compact), Interrupt: viaTerminal(c.Interrupt)}
	if kind == kindClaudeCode {
		caps.Wake = ViaHook
	}
	return caps
}

// missing says why a terminal action is not possible for a session.
func missing(action string, has bool, terminal string, pid int) string {
	var parts []string
	if !has {
		parts = append(parts, "the hub has no --"+action+"-command")
	}
	if terminal == "" {
		parts = append(parts, "its session reports no terminal (the connector reports one with --terminal-env)")
	}
	if pid <= 0 {
		parts = append(parts, "its process is unknown")
	}
	return strings.Join(parts, " and ")
}

// CheckFocus refuses a focus that is too long or holds a line break or another control character.
func CheckFocus(focus string) error {
	if utf8.RuneCountInString(focus) > MaxFocus {
		return store.Refuse(store.ErrInvalid, "a focus is at most %d characters", MaxFocus)
	}
	if !utf8.ValidString(focus) || strings.IndexFunc(focus, func(r rune) bool { return unicode.IsControl(r) || unicode.In(r, unicode.Zl, unicode.Zp) }) >= 0 {
		return store.Refuse(store.ErrInvalid, "a focus is text with no line breaks or other control characters")
	}
	return nil
}

// target is the live session an action acts on.
type target struct {
	ID, Terminal string
	State        State
	Turn         int64
	PID          int
	PIDStart     int64
	kind         string
}

// Target is a session an interrupt is about to act on.
type Target = target

func liveTarget(ctx context.Context, tx *sql.Tx, by, agent string) (target, error) {
	if err := agents.ExistsTx(ctx, tx, by); err != nil {
		return target{}, err
	}
	if err := agents.ExistsTx(ctx, tx, agent); err != nil {
		return target{}, err
	}
	var t target
	err := tx.QueryRowContext(ctx, `SELECT id, kind, terminal, state, turn, pid, pid_start FROM sessions WHERE agent = ? AND state != ?`, agent, Ended).
		Scan(&t.ID, &t.kind, &t.Terminal, &t.State, &t.Turn, &t.PID, &t.PIDStart)
	if errors.Is(err, sql.ErrNoRows) {
		return target{}, store.Refuse(ErrRefused, "%s has no live session", agent)
	}
	return t, err
}

// Compact queues a compaction of agent's conversation asked for by by, with an optional focus,
// and announces it. It is refused when the session cannot compact or already has one queued.
func (s *Sessions) Compact(ctx context.Context, c Commands, by, agent, focus string) (Mechanism, error) {
	if err := CheckFocus(focus); err != nil {
		return "", err
	}
	now := s.now()
	err := store.InTx(ctx, s.db, func(tx *sql.Tx) error {
		t, err := liveTarget(ctx, tx, by, agent)
		if err != nil {
			return err
		}
		if CapabilitiesOf(c, t.kind, t.Terminal, t.PID).Compact == ViaNothing {
			return store.Refuse(ErrRefused, "%s cannot compact: %s", agent, missing("compact", c.Compact, t.Terminal, t.PID))
		}
		var first string
		switch err := tx.QueryRowContext(ctx, `SELECT requested_by FROM session_controls WHERE session_id = ? AND action = 'compact' AND state IN ('queued', 'running')`, t.ID).Scan(&first); {
		case err == nil:
			return store.Refuse(ErrRefused, "a compact of %s is already queued by %s", agent, first)
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO session_controls (session_id, agent, action, focus, requested_by, requested_at, state)
			VALUES (?, ?, 'compact', ?, ?, ?, 'queued')`, t.ID, agent, focus, by, now.UnixMilli()); err != nil {
			return err
		}
		text := fmt.Sprintf("%s queued a compact of %s; it runs when %s is idle", by, agent, agent)
		if focus != "" {
			text += fmt.Sprintf(" (focus: %s)", focus)
		}
		return s.announce(ctx, tx, agent, text)
	})
	if err != nil {
		return "", err
	}
	return ViaTerminal, nil
}

// PrepareInterrupt checks that by may interrupt agent now: its session can, and is busy with a
// running process. It changes nothing; the hub runs the command, then calls FinishInterrupt.
func (s *Sessions) PrepareInterrupt(ctx context.Context, c Commands, by, agent string) (Target, error) {
	var t target
	err := store.InTx(ctx, s.db, func(tx *sql.Tx) error {
		var err error
		if t, err = liveTarget(ctx, tx, by, agent); err != nil {
			return err
		}
		if CapabilitiesOf(c, t.kind, t.Terminal, t.PID).Interrupt == ViaNothing {
			return store.Refuse(ErrRefused, "%s cannot be interrupted: %s", agent, missing("interrupt", c.Interrupt, t.Terminal, t.PID))
		}
		if t.State != Busy {
			return store.Refuse(ErrRefused, "%s is idle; there is no turn to interrupt", agent)
		}
		return nil
	})
	return t, err
}

// FinishInterrupt records an interrupt whose command succeeded: the session is idle, since an
// interrupted turn runs no end hook (unless a new turn began meanwhile), the target is to be
// told, and the board announces it.
func (s *Sessions) FinishInterrupt(ctx context.Context, by, agent string, t Target) error {
	now := s.now().UnixMilli()
	return store.InTx(ctx, s.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE sessions SET state = ?, state_at = ? WHERE id = ? AND state = ? AND turn = ?`,
			Idle, now, t.ID, Busy, t.Turn); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO session_controls (session_id, agent, action, requested_by, requested_at, state, ran_at)
			VALUES (?, ?, 'interrupt', ?, ?, 'done', ?)`, t.ID, agent, by, now, now); err != nil {
			return err
		}
		return s.announce(ctx, tx, agent, fmt.Sprintf("%s interrupted %s", by, agent))
	})
}

// announce posts text as the board where it tells agent about its own work.
func (s *Sessions) announce(ctx context.Context, tx *sql.Tx, agent, text string) error {
	if s.rooms == nil {
		return nil
	}
	room, err := rooms.NoticeRoomTx(ctx, tx, agent)
	if err != nil {
		return err
	}
	_, err = s.rooms.PostTx(ctx, tx, rooms.Board, room, text, 0)
	return err
}

// QueuedCompact is a compact whose command the hub runs now.
type QueuedCompact struct {
	ID               int64
	Agent, By, Focus string
	Terminal         string
	PID              int
	PIDStart         int64
}

// Text is what the compact command types.
func (q QueuedCompact) Text() string {
	if q.Focus == "" {
		return "/compact"
	}
	return "/compact " + q.Focus
}

// DropEndedCompacts drops the queued compacts whose session ended or no longer belongs to the
// agent they were asked for (it left, or the name moved to another session), and announces each.
func (s *Sessions) DropEndedCompacts(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT c.id FROM session_controls c JOIN sessions s ON s.id = c.session_id
		WHERE c.action = 'compact' AND c.state = 'queued' AND (s.state = ? OR s.agent IS NOT c.agent)`, Ended)
	if err != nil {
		return err
	}
	ids, err := scanIDs(rows)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := s.settleCompact(ctx, id, "queued", "dropped", "its session ended or is no longer that agent's",
			"the compact of %s that %s queued was dropped: its session ended or is no longer %[1]s's"); err != nil {
			return err
		}
	}
	return nil
}

func scanIDs(rows *sql.Rows) ([]int64, error) {
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// settleCompact moves a compact from state from to state to and announces it as format says,
// with the target and the requester.
func (s *Sessions) settleCompact(ctx context.Context, id int64, from, to, result, format string, extra ...any) error {
	return store.InTx(ctx, s.db, func(tx *sql.Tx) error {
		var agent, by string
		err := tx.QueryRowContext(ctx, `SELECT agent, requested_by FROM session_controls WHERE id = ?`, id).Scan(&agent, &by)
		if err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `UPDATE session_controls SET state = ?, result = ? WHERE id = ? AND state = ?`, to, result, id, from)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil
		}
		return s.announce(ctx, tx, agent, fmt.Sprintf(format, append([]any{agent, by}, extra...)...))
	})
}

// CompactCandidates returns the queued compacts whose session has been idle for settle, was not
// woken within settle, still belongs to the agent they were asked for, and whose process runs, as
// alive reports. The hub claims each (ClaimCompact) once it holds the session's terminal.
func (s *Sessions) CompactCandidates(ctx context.Context, settle time.Duration, alive func(pid int, start int64) bool) ([]QueuedCompact, error) {
	list, err := s.idleCompacts(ctx, s.now().Add(-settle))
	if err != nil {
		return nil, err
	}
	var out []QueuedCompact
	for _, f := range list {
		if f.PID > 0 && f.Terminal != "" && alive(f.PID, f.PIDStart) { // else the sweep ends the session and the compact is dropped
			out = append(out, f)
		}
	}
	return out, nil
}

// ClaimCompact marks the compact as running when it still may run: queued, its session idle
// for settle and not woken within settle, and still the agent's. It reports whether it did, so
// no round runs a request twice.
func (s *Sessions) ClaimCompact(ctx context.Context, id int64, settle time.Duration) (bool, error) {
	now := s.now()
	since := now.Add(-settle).UnixMilli()
	res, err := s.db.ExecContext(ctx, `UPDATE session_controls SET state = 'running', ran_at = ?
		WHERE id = ? AND state = 'queued' AND EXISTS (SELECT 1 FROM sessions s WHERE s.id = session_controls.session_id
			AND s.state = ? AND s.state_at <= ? AND s.woken_at <= ? AND s.agent = session_controls.agent)`, now.UnixMilli(), id, Idle, since, since)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// idleCompacts returns the queued compacts whose session has been idle since idleSince.
func (s *Sessions) idleCompacts(ctx context.Context, idleSince time.Time) ([]QueuedCompact, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT c.id, c.agent, c.requested_by, c.focus, s.terminal, s.pid, s.pid_start
		FROM session_controls c JOIN sessions s ON s.id = c.session_id
		WHERE c.action = 'compact' AND c.state = 'queued' AND s.state = ? AND s.state_at <= ? AND s.woken_at <= ? AND s.agent = c.agent ORDER BY c.id`,
		Idle, idleSince.UnixMilli(), idleSince.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []QueuedCompact
	for rows.Next() {
		var f QueuedCompact
		if err := rows.Scan(&f.ID, &f.Agent, &f.By, &f.Focus, &f.Terminal, &f.PID, &f.PIDStart); err != nil {
			return nil, err
		}
		list = append(list, f)
	}
	return list, rows.Err()
}

// FinishCompact records how a compact command went and announces it.
func (s *Sessions) FinishCompact(ctx context.Context, id int64, ok bool, result string) error {
	if ok {
		return s.settleCompact(ctx, id, "running", "done", result, "%[2]s compacted %[1]s")
	}
	return s.settleCompact(ctx, id, "running", "failed", result, "the compact of %s that %s queued failed: %s", result)
}

// FailRunningCompacts fails the compacts a stopped hub left running, as it cannot tell how they
// went.
func (s *Sessions) FailRunningCompacts(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM session_controls WHERE action = 'compact' AND state = 'running'`)
	if err != nil {
		return err
	}
	ids, err := scanIDs(rows)
	if err != nil {
		return err
	}
	for _, id := range ids {
		const why = "the hub stopped while it ran"
		if err := s.settleCompact(ctx, id, "running", "failed", why, "the compact of %s that %s queued failed: %s", why); err != nil {
			return err
		}
	}
	return nil
}

// controlNotes returns what to tell the session's agent about compactions and interrupts it has
// not been told about, once.
func (s *Sessions) controlNotes(ctx context.Context, sessionID string) (string, error) {
	var notes []string
	err := store.InTx(ctx, s.db, func(tx *sql.Tx) error {
		list, err := untoldNotes(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		for _, n := range list {
			res, err := tx.ExecContext(ctx, `UPDATE session_controls SET told = 1 WHERE id = ? AND told = 0`, n.id)
			if err != nil {
				return err
			}
			if c, _ := res.RowsAffected(); c == 0 {
				continue // a concurrent hook of this session told it
			}
			switch {
			case n.action == "interrupt":
				notes = append(notes, fmt.Sprintf("Agora: %s interrupted your turn.", n.by))
			case n.focus != "":
				notes = append(notes, fmt.Sprintf("Agora: %s compacted your conversation, with the focus: %s", n.by, n.focus))
			default:
				notes = append(notes, fmt.Sprintf("Agora: %s compacted your conversation.", n.by))
			}
		}
		return nil
	})
	return strings.Join(notes, "\n"), err
}

type controlNote struct {
	id                int64
	action, focus, by string
}

// untoldNotes returns the finished controls of the session whose agent was not told yet.
func untoldNotes(ctx context.Context, tx *sql.Tx, sessionID string) ([]controlNote, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, action, focus, requested_by FROM session_controls
		WHERE session_id = ? AND state = 'done' AND told = 0 AND agent = (SELECT COALESCE(agent, '') FROM sessions WHERE id = ?) ORDER BY id`, sessionID, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []controlNote
	for rows.Next() {
		var n controlNote
		if err := rows.Scan(&n.id, &n.action, &n.focus, &n.by); err != nil {
			return nil, err
		}
		list = append(list, n)
	}
	return list, rows.Err()
}
