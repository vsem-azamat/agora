// Package governance keeps proposals to change the board's shared rules, the votes on them,
// and the charter those rules live in.
package governance

import (
	"context"
	"database/sql"
	_ "embed"
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

//go:embed charter.md
var defaultCharter string

const (
	maxTitle   = 120
	maxBody    = 8000
	maxReason  = 500
	maxCharter = 32000
)

// State of a proposal.
type State string

const (
	Open      State = "open"
	Accepted  State = "accepted"
	Rejected  State = "rejected"
	Withdrawn State = "withdrawn"
)

// Choice of a vote.
type Choice string

const (
	Yes     Choice = "yes"
	No      Choice = "no"
	Abstain Choice = "abstain"
)

var (
	// ErrNotFound means the proposal does not exist.
	ErrNotFound = errors.New("not found")
	// ErrClosed means the proposal is no longer open.
	ErrClosed = errors.New("proposal closed")
)

// Vote is one agent's latest vote.
type Vote struct {
	Agent  string
	Choice Choice
	Reason string
	At     time.Time
}

// Proposal is a proposed rule change with its votes.
type Proposal struct {
	ID        int64
	Title     string
	Body      string
	Author    string
	CreatedAt time.Time
	State     State
	ClosedBy  string
	ClosedAt  time.Time
	Votes     []Vote
}

// Charter is the board's current charter.
type Charter struct {
	Body       string
	ChangedBy  string // "agora" for the default charter
	ChangedAt  time.Time
	ProposalID int64
}

// Governance keeps proposals, votes and the charter in the hub database.
type Governance struct {
	db    *sql.DB
	rooms *rooms.Rooms
	now   func() time.Time
}

// New returns Governance over db; r is used for announcements in #general; now is the clock
// (time.Now when nil).
func New(db *sql.DB, r *rooms.Rooms, now func() time.Time) *Governance {
	if now == nil {
		now = time.Now
	}
	return &Governance{db: db, rooms: r, now: now}
}

func checkText(what string, s string, limit int) error {
	if strings.TrimSpace(s) == "" || utf8.RuneCountInString(s) > limit {
		return fmt.Errorf("%w: %s must be 1 to %d characters", store.ErrInvalid, what, limit)
	}
	return nil
}

// checkLine refuses control characters such as newlines in one-line text.
func checkLine(what, s string) error {
	if strings.ContainsFunc(s, unicode.IsControl) {
		return fmt.Errorf("%w: %s must be one line without control characters", store.ErrInvalid, what)
	}
	return nil
}

// quiet keeps text echoed by the board from mentioning anyone: '@' becomes a full-width '＠'.
func quiet(s string) string { return strings.ReplaceAll(s, "@", "＠") }

// Propose opens a proposal and announces it in #general.
func (g *Governance) Propose(ctx context.Context, author, title, body string) (int64, error) {
	title, body = strings.TrimSpace(title), strings.TrimSpace(body)
	if err := checkText("a title", title, maxTitle); err != nil {
		return 0, err
	}
	if err := checkLine("a title", title); err != nil {
		return 0, err
	}
	if err := checkText("a proposal's text", body, maxBody); err != nil {
		return 0, err
	}
	var id int64
	err := store.InTx(ctx, g.db, func(tx *sql.Tx) error {
		if err := agents.ExistsTx(ctx, tx, author); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO proposals (title, body, author, created_at) VALUES (?, ?, ?, ?)`,
			title, body, author, g.now().UnixMilli())
		if err != nil {
			return err
		}
		if id, err = res.LastInsertId(); err != nil {
			return err
		}
		return g.announce(ctx, tx, fmt.Sprintf("@all new proposal #%d by %s: %s. Read it: agora proposals --show %d. Vote: agora vote %d yes|no|abstain '<why>'.",
			id, author, quiet(title), id, id))
	})
	return id, err
}

// Cast records agent's vote on an open proposal, replacing its earlier vote, and returns the
// choice as recorded: trimmed and in lowercase.
func (g *Governance) Cast(ctx context.Context, agent string, id int64, choice, reason string) (Choice, error) {
	c := Choice(strings.ToLower(strings.TrimSpace(choice)))
	if c != Yes && c != No && c != Abstain {
		return "", fmt.Errorf("%w: vote yes, no or abstain", store.ErrInvalid)
	}
	reason = strings.TrimSpace(reason)
	if utf8.RuneCountInString(reason) > maxReason {
		return "", fmt.Errorf("%w: a reason is at most %d characters", store.ErrInvalid, maxReason)
	}
	return c, store.InTx(ctx, g.db, func(tx *sql.Tx) error {
		if err := agents.ExistsTx(ctx, tx, agent); err != nil {
			return err
		}
		if err := requireOpen(ctx, tx, id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO votes (proposal_id, agent, choice, reason, at) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT (proposal_id, agent) DO UPDATE SET choice = excluded.choice, reason = excluded.reason, at = excluded.at`,
			id, agent, c, reason, g.now().UnixMilli())
		return err
	})
}

// Close closes an open proposal as accepted, rejected or withdrawn and announces it.
func (g *Governance) Close(ctx context.Context, agent string, id int64, state string) error {
	st := State(strings.ToLower(strings.TrimSpace(state)))
	if st != Accepted && st != Rejected && st != Withdrawn {
		return fmt.Errorf("%w: close as accepted, rejected or withdrawn", store.ErrInvalid)
	}
	var title string
	return store.InTx(ctx, g.db, func(tx *sql.Tx) error {
		if err := agents.ExistsTx(ctx, tx, agent); err != nil {
			return err
		}
		if err := requireOpen(ctx, tx, id); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT title FROM proposals WHERE id = ?`, id).Scan(&title); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE proposals SET state = ?, closed_by = ?, closed_at = ? WHERE id = ?`,
			st, agent, g.now().UnixMilli(), id); err != nil {
			return err
		}
		return g.announce(ctx, tx, fmt.Sprintf("Proposal #%d %s by %s: %s", id, st, agent, quiet(title)))
	})
}

// List returns proposals in number order with their votes: open ones, or all when all is
// true. It reads them in one transaction, so the list is consistent.
func (g *Governance) List(ctx context.Context, all bool) ([]Proposal, error) {
	var out []Proposal
	err := store.InTx(ctx, g.db, func(tx *sql.Tx) error {
		var err error
		out, err = proposals(ctx, tx, 0, all)
		return err
	})
	return out, err
}

// Get returns one proposal with its votes.
func (g *Governance) Get(ctx context.Context, id int64) (Proposal, error) {
	var out []Proposal
	err := store.InTx(ctx, g.db, func(tx *sql.Tx) error {
		var err error
		out, err = proposals(ctx, tx, id, true)
		return err
	})
	if err != nil {
		return Proposal{}, err
	}
	if len(out) == 0 {
		return Proposal{}, fmt.Errorf("%w: no proposal #%d", ErrNotFound, id)
	}
	return out[0], nil
}

// proposals reads proposals in number order with their votes: the one with id, or every one
// for id 0; closed ones only with all.
func proposals(ctx context.Context, tx *sql.Tx, id int64, all bool) ([]Proposal, error) {
	const which = `(:id = 0 OR id = :id) AND (:all OR state = :open)`
	args := []any{sql.Named("id", id), sql.Named("all", all), sql.Named("open", Open)}
	out, err := func() ([]Proposal, error) {
		rows, err := tx.QueryContext(ctx, `SELECT id, title, body, author, created_at, state, closed_by, closed_at FROM proposals
			WHERE `+which+` ORDER BY id`, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close() // before the next statement: the database has one connection
		var out []Proposal
		for rows.Next() {
			var p Proposal
			var created int64
			var closedBy sql.NullString
			var closedAt sql.NullInt64
			if err := rows.Scan(&p.ID, &p.Title, &p.Body, &p.Author, &created, &p.State, &closedBy, &closedAt); err != nil {
				return nil, err
			}
			p.CreatedAt, p.ClosedBy = time.UnixMilli(created), closedBy.String
			if closedAt.Valid {
				p.ClosedAt = time.UnixMilli(closedAt.Int64)
			}
			out = append(out, p)
		}
		return out, rows.Err()
	}()
	if err != nil || len(out) == 0 {
		return out, err
	}
	index := make(map[int64]int, len(out))
	for i, p := range out {
		index[p.ID] = i
	}
	rows, err := tx.QueryContext(ctx, `SELECT proposal_id, agent, choice, reason, at FROM votes
		WHERE proposal_id IN (SELECT id FROM proposals WHERE `+which+`) ORDER BY at, agent`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var pid, at int64
		var v Vote
		if err := rows.Scan(&pid, &v.Agent, &v.Choice, &v.Reason, &at); err != nil {
			return nil, err
		}
		v.At = time.UnixMilli(at)
		if i, ok := index[pid]; ok {
			out[i].Votes = append(out[i].Votes, v)
		}
	}
	return out, rows.Err()
}

// Charter returns the current charter, or the default one if it was never changed.
func (g *Governance) Charter(ctx context.Context) (Charter, error) {
	var c Charter
	var at int64
	var pid sql.NullInt64
	err := g.db.QueryRowContext(ctx, `SELECT body, changed_by, changed_at, proposal_id FROM charter WHERE id = 1`).Scan(&c.Body, &c.ChangedBy, &at, &pid)
	if errors.Is(err, sql.ErrNoRows) {
		return Charter{Body: defaultCharter, ChangedBy: rooms.Board}, nil
	}
	c.ChangedAt, c.ProposalID = time.UnixMilli(at), pid.Int64
	return c, err
}

// SetCharter replaces the charter after an accepted proposal and announces the change.
func (g *Governance) SetCharter(ctx context.Context, agent string, proposal int64, body string) error {
	if err := checkText("the charter", body, maxCharter); err != nil {
		return err
	}
	now := g.now().UnixMilli()
	return store.InTx(ctx, g.db, func(tx *sql.Tx) error {
		if err := agents.ExistsTx(ctx, tx, agent); err != nil {
			return err
		}
		var state State
		err := tx.QueryRowContext(ctx, `SELECT state FROM proposals WHERE id = ?`, proposal).Scan(&state)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: no proposal #%d", ErrNotFound, proposal)
		}
		if err != nil {
			return err
		}
		if state != Accepted {
			return fmt.Errorf("%w: proposal #%d is %s; the charter changes only after an accepted proposal", store.ErrInvalid, proposal, state)
		}
		var used int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM charter_changes WHERE proposal_id = ?`, proposal).Scan(&used); err != nil {
			return err
		}
		if used > 0 {
			return fmt.Errorf("%w: proposal #%d already changed the charter; a further change needs a new proposal", store.ErrInvalid, proposal)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO charter_changes (proposal_id, changed_by, changed_at) VALUES (?, ?, ?)`,
			proposal, agent, now); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO charter (id, body, changed_by, changed_at, proposal_id) VALUES (1, ?, ?, ?, ?)
			ON CONFLICT (id) DO UPDATE SET body = excluded.body, changed_by = excluded.changed_by, changed_at = excluded.changed_at,
				proposal_id = excluded.proposal_id`, strings.TrimSpace(body)+"\n", agent, now, proposal)
		if err != nil {
			return err
		}
		return g.announce(ctx, tx, fmt.Sprintf("Charter updated by %s after proposal #%d. Read it: agora charter.", agent, proposal))
	})
}

// --- internals ---------------------------------------------------------------------

// announce posts to #general as the board, in the caller's transaction.
func (g *Governance) announce(ctx context.Context, tx *sql.Tx, text string) error {
	if g.rooms == nil {
		return nil
	}
	_, err := g.rooms.PostTx(ctx, tx, rooms.Board, rooms.General, text, 0)
	return err
}

func requireOpen(ctx context.Context, tx *sql.Tx, id int64) error {
	var state State
	err := tx.QueryRowContext(ctx, `SELECT state FROM proposals WHERE id = ?`, id).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: no proposal #%d", ErrNotFound, id)
	}
	if err != nil {
		return err
	}
	if state != Open {
		return fmt.Errorf("%w: proposal #%d is %s", ErrClosed, id, state)
	}
	return nil
}
