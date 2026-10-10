package sessions

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/vsem-azamat/agora/internal/queue"
	"github.com/vsem-azamat/agora/internal/rooms"
	"github.com/vsem-azamat/agora/internal/store"
)

// Turn returns the session's turn count, which grows with every prompt and start.
func (s *Sessions) Turn(ctx context.Context, sessionID string) (int64, error) {
	var turn int64
	err := s.db.QueryRowContext(ctx, `SELECT turn FROM sessions WHERE id = ?`, sessionID).Scan(&turn)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("%w: unknown session", store.ErrInvalid)
	}
	return turn, err
}

// Wake is what a connector wait delivers: the text, the messages that wake the agent it shows (marked
// read only once the wake is delivered) and a key of what it woke for.
type Wake struct {
	Text     string
	Messages []rooms.Message
	Key      string
	Turn     int64
}

// CheckWake decides a connector's wait for a session that started at turn: done with no wake
// when the session ended, a new turn began or the session lost its agent; done with a wake when
// the session is idle and something new needs its agent (a message that wakes it, or an offered slot
// it was not already woken for); otherwise keep waiting. Nothing is consumed until ConfirmWake.
func (s *Sessions) CheckWake(ctx context.Context, sessionID string, turn int64) (w *Wake, done bool, err error) {
	var state, agent, wokenFor string
	var current int64
	err = s.db.QueryRowContext(ctx, `SELECT state, COALESCE(agent, ''), turn, woken_for FROM sessions WHERE id = ?`, sessionID).
		Scan(&state, &agent, &current, &wokenFor)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, true, nil
	case err != nil:
		return nil, false, err
	case state == string(Ended) || current != turn:
		return nil, true, nil
	case state != string(Idle):
		return nil, false, nil
	case agent == "":
		return nil, true, nil
	}
	var msgs []rooms.Message
	if s.rooms != nil {
		if msgs, _, err = s.rooms.Unread(ctx, agent, rooms.Waking, DeliverAtOnce); err != nil {
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
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET state = ?, state_at = ?, turn = turn + 1, woken_for = ?, woken_at = ?
		WHERE id = ? AND turn = ? AND state = ?`, Busy, now, w.Key, now, sessionID, w.Turn, Idle)
	return err
}

// Now is the clock Sessions uses.
func (s *Sessions) Now() time.Time { return s.now() }

// offerKey identifies the offered slots, to tell whether the agent was woken for them.
func offerKey(entries []queue.Entry) string {
	var keys []string
	for _, e := range offered(entries) {
		keys = append(keys, "o"+e.Key)
	}
	return strings.Join(keys, " ")
}

// Pending describes what would wake a session's agent without consuming it: a key that
// changes when something new arrives (the newest message that wakes it and the offered slots) and
// a short text for a terminal prompt. key is "" when nothing needs the agent.
func (s *Sessions) Pending(ctx context.Context, agent string) (key, text string, err error) {
	var parts, keys []string
	if s.rooms != nil {
		msgs, _, err := s.rooms.Unread(ctx, agent, rooms.Waking, 0)
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
			what := "addressed to you"
			if !allAddressed(msgs) {
				what = "for you"
			}
			parts = append(parts, fmt.Sprintf("%d board message(s) %s (%s)", len(msgs), what, strings.Join(senders, ", ")))
		}
	}
	entries, err := s.queue.EntriesOf(ctx, agent)
	if err != nil {
		return "", "", err
	}
	for _, e := range offered(entries) {
		keys = append(keys, "o"+e.Key)
		parts = append(parts, "your turn on "+e.Key)
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
		WHERE state = ? AND agent IS NOT NULL AND terminal != '' AND pid > 0 AND state_at <= ? ORDER BY id`,
		Idle, s.now().Add(-settle).UnixMilli())
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
