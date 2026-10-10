// Package agents keeps agent profiles: what each agent works on and where, whether it is
// active, and finding the owner of a pull request, branch or directory.
package agents

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/vsem-azamat/agora/internal/gitinfo"
	"github.com/vsem-azamat/agora/internal/store"
)

const (
	// StaleAfter is how long an agent without a live session stays active after its last update.
	StaleAfter = 6 * time.Hour
	// Working is the status of an agent that just joined.
	Working = "working"
	// Left is the status of an agent that left.
	Left = "left"
	// Offline is the session state of an agent without a live session.
	Offline = "offline"
)

// ErrUnknown means the name never joined.
var ErrUnknown = errors.New("unknown agent")

// Profile is what an agent publishes about its work.
type Profile struct {
	Name         string
	Kind         string
	Project      string
	Task         string
	Status       string
	CWD          string
	Branch       string
	PRs          []int // declared
	FoundPRs     []int // found from the branch by the pull request watcher
	About        string
	Icon         string       // a sigil from Icons, or ""
	Pigment      string       // a colour from Pigments, or ""
	Formerly     []FormerName // newest first
	JoinedAt     time.Time
	UpdatedAt    time.Time
	SessionState string         // busy, idle or offline
	CI           map[int]string // the CI state last reported per followed pull request
	Active       bool
}

// Update changes the fields that are not nil; AddPRs and DropPRs edit the pull requests.
type Update struct {
	Kind, Project, Task, Status, CWD, About, Icon, Pigment *string
	AddPRs, DropPRs                                        []int
}

// Agents keeps profiles in the hub database.
type Agents struct {
	db  *sql.DB
	now func() time.Time
}

// New returns Agents over db; now is the clock (time.Now when nil).
func New(db *sql.DB, now func() time.Time) *Agents {
	if now == nil {
		now = time.Now
	}
	return &Agents{db: db, now: now}
}

// Update applies u to the profile of name and returns the result.
func (a *Agents) Update(ctx context.Context, name string, u Update) (Profile, error) {
	if u.Status != nil && (strings.TrimSpace(*u.Status) == "" || len(*u.Status) > 32) {
		return Profile{}, fmt.Errorf("%w: status must be 1-32 characters", store.ErrInvalid)
	}
	if u.Status != nil && strings.TrimSpace(*u.Status) == Left {
		return Profile{}, fmt.Errorf("%w: to leave, use `agora leave`, which also gives up your queue places", store.ErrInvalid)
	}
	if u.CWD != nil && !filepath.IsAbs(*u.CWD) {
		return Profile{}, fmt.Errorf("%w: directory must be an absolute path", store.ErrInvalid)
	}
	if err := checkChoice("sigil", u.Icon, Icons); err != nil {
		return Profile{}, err
	}
	if err := checkChoice("pigment", u.Pigment, Pigments); err != nil {
		return Profile{}, err
	}
	for _, n := range append(slices.Clone(u.AddPRs), u.DropPRs...) {
		if n <= 0 {
			return Profile{}, fmt.Errorf("%w: pull request numbers are positive", store.ErrInvalid)
		}
	}
	now := a.now()
	err := store.InTx(ctx, a.db, func(tx *sql.Tx) error {
		p, err := load(ctx, tx, name)
		if err != nil {
			return err
		}
		set := func(dst *string, v *string) {
			if v != nil {
				*dst = strings.TrimSpace(*v)
			}
		}
		set(&p.Kind, u.Kind)
		set(&p.Project, u.Project)
		set(&p.Task, u.Task)
		set(&p.Status, u.Status)
		set(&p.About, u.About)
		set(&p.Icon, u.Icon)
		set(&p.Pigment, u.Pigment)
		if u.CWD != nil {
			dir := resolve(*u.CWD)
			p.Branch = nextBranch(p.Branch, p.CWD, dir)
			p.CWD = dir
		}
		prs := map[int]bool{}
		for _, n := range p.PRs {
			prs[n] = true
		}
		for _, n := range u.AddPRs {
			prs[n] = true
		}
		for _, n := range u.DropPRs {
			delete(prs, n)
		}
		p.PRs = sortedKeys(prs)
		_, err = tx.ExecContext(ctx, `UPDATE agents SET kind = ?, project = ?, task = ?, status = ?, cwd = ?, branch = ?, prs = ?, about = ?, icon = ?, pigment = ?,
			updated_at = ? WHERE name = ?`, p.Kind, p.Project, p.Task, p.Status, p.CWD, p.Branch, joinPRs(p.PRs), p.About, p.Icon, p.Pigment, now.UnixMilli(), name)
		return err
	})
	if err != nil {
		return Profile{}, err
	}
	return a.Get(ctx, name)
}

// RegisterTx registers name if it has not joined before, inside the caller's transaction; its
// reading starts after the newest message.
func RegisterTx(ctx context.Context, tx *sql.Tx, name string, now time.Time) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO agents (name, joined_at, read_from) VALUES (?, ?, (SELECT COALESCE(MAX(id), 0) FROM messages))
		ON CONFLICT (name) DO NOTHING`, name, now.UnixMilli())
	return err
}

// ExistsTx returns ErrUnknown, saying how to join, when name has not joined.
func ExistsTx(ctx context.Context, tx *sql.Tx, name string) error {
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM agents WHERE name = ?`, name).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	owner, err := formerOwner(ctx, tx, name)
	switch {
	case err != nil:
		return err
	case owner != "":
		return nowCalled(ErrUnknown, name, owner)
	}
	return unknown(name)
}

func unknown(name string) error {
	return fmt.Errorf("%w: %q has not joined; run `agora join %s` first", ErrUnknown, name, name)
}

// MarkLeftTx marks name as left inside the caller's transaction.
func MarkLeftTx(ctx context.Context, tx *sql.Tx, name string, now time.Time) error {
	_, err := tx.ExecContext(ctx, `UPDATE agents SET status = ?, updated_at = ? WHERE name = ?`, Left, now.UnixMilli(), name)
	return err
}

// ReturnTx marks a left agent as working again, inside the caller's transaction: a session
// still bound to it is active once more, as after a resume.
func ReturnTx(ctx context.Context, tx *sql.Tx, name string, now time.Time) error {
	_, err := tx.ExecContext(ctx, `UPDATE agents SET status = ?, updated_at = ? WHERE name = ? AND status = ?`, Working, now.UnixMilli(), name, Left)
	return err
}

// FollowTx moves the profile of name to the directory its session reports, inside the
// caller's transaction, unless the profile's directory lies inside it (a worktree the agent
// set). The update time changes only when the directory or branch changed.
func FollowTx(ctx context.Context, tx *sql.Tx, name, sessionDir string, now time.Time) error {
	if sessionDir == "" || !filepath.IsAbs(sessionDir) {
		return nil
	}
	var cwd, branch string
	if err := tx.QueryRowContext(ctx, `SELECT cwd, branch FROM agents WHERE name = ?`, name).Scan(&cwd, &branch); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	dir := resolve(sessionDir)
	if cwd != "" && cwd != dir && within(cwd, dir) {
		dir = cwd
	}
	next := nextBranch(branch, cwd, dir)
	if dir == cwd && next == branch {
		return nil
	}
	_, err := tx.ExecContext(ctx, `UPDATE agents SET cwd = ?, branch = ?, updated_at = ? WHERE name = ?`, dir, next, now.UnixMilli(), name)
	return err
}

// Get returns the profile of name.
func (a *Agents) Get(ctx context.Context, name string) (Profile, error) {
	p, err := scan(a.db.QueryRowContext(ctx, profileQuery+` WHERE a.name = ?`, sessionEnded, sessionBusy, Offline, name))
	if errors.Is(err, sql.ErrNoRows) {
		return p, unknown(name)
	}
	p.Active = active(p, a.now())
	return p, err
}

// The session states profileQuery reads from the sessions table; the sessions package, which
// owns them, imports this one.
const sessionEnded, sessionBusy = "ended", "busy"

// profileQuery selects full profiles; it takes sessionEnded, sessionBusy and Offline as its
// first arguments.
const profileQuery = `SELECT a.name, a.kind, a.project, a.task, a.status, a.cwd, a.branch, a.prs, a.about, a.icon, a.pigment, a.joined_at, a.updated_at,
	COALESCE((SELECT s.state FROM sessions AS s WHERE s.agent = a.name AND s.state != ?
		ORDER BY CASE s.state WHEN ? THEN 0 ELSE 1 END LIMIT 1), ?),
	COALESCE((SELECT group_concat(p.number, ' ') FROM pull_requests AS p WHERE p.agent = a.name AND p.found = 1), ''),
	` + ciColumn + `,
	` + formerlyColumn + `
	FROM agents AS a`

// active reports whether p counts as active at now: not left, and with a live session or
// updated within StaleAfter.
func active(p Profile, now time.Time) bool {
	return p.Status != Left && (p.SessionState != Offline || now.Sub(p.UpdatedAt) < StaleAfter)
}

// List returns profiles in name order: active agents, or all agents when all is true.
func (a *Agents) List(ctx context.Context, all bool) ([]Profile, error) {
	rows, err := a.db.QueryContext(ctx, profileQuery+` ORDER BY a.name`, sessionEnded, sessionBusy, Offline)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	now := a.now()
	var out []Profile
	for rows.Next() {
		p, err := scan(rows)
		if err != nil {
			return nil, err
		}
		p.Active = active(p, now)
		if all || p.Active {
			out = append(out, p)
		}
	}
	return out, rows.Err()
}

// Who finds agents by pull request (`57` or `#57`, declared or found), name, exact branch or a part of a branch
// longer than two characters; with path set, query is a path and matches agents working in it,
// in a directory inside it, or in a directory that contains it (the owner of a file or folder).
func (a *Agents) Who(ctx context.Context, query string, path, all bool) ([]Profile, error) {
	list, err := a.List(ctx, all)
	if err != nil {
		return nil, err
	}
	q := strings.TrimSpace(query)
	pr, prErr := strconv.Atoi(strings.TrimPrefix(q, "#"))
	var out []Profile
	for _, p := range list {
		var hit bool
		switch {
		case path:
			dir := resolve(q)
			hit = p.CWD != "" && (within(p.CWD, dir) || within(dir, p.CWD))
		case prErr == nil:
			hit = slices.Contains(p.PRs, pr) || slices.Contains(p.FoundPRs, pr)
		default:
			hit = q == p.Name || formerly(p, q) || (q != "" && q == p.Branch) || (len(q) > 2 && strings.Contains(p.Branch, q))
		}
		if hit {
			out = append(out, p)
		}
	}
	return out, nil
}

// --- internals ---------------------------------------------------------------------

// formerly reports whether name is one of p's former names.
func formerly(p Profile, name string) bool {
	return slices.ContainsFunc(p.Formerly, func(f FormerName) bool { return f.Name == name })
}

type scanner interface{ Scan(...any) error }

// ciColumn lists the CI states last reported for agent a's pull requests as "57:green 58:red".
const ciColumn = `COALESCE((SELECT group_concat(p.number || ':' || p.ci_state, ' ')
	FROM pull_requests AS p WHERE p.agent = a.name AND p.ci_state != ''), '')`

func parseCI(s string) map[int]string {
	var out map[int]string
	for _, f := range strings.Fields(s) {
		num, state, ok := strings.Cut(f, ":")
		n, err := strconv.Atoi(num)
		if !ok || err != nil || state == "" {
			continue
		}
		if out == nil {
			out = map[int]string{}
		}
		out[n] = state
	}
	return out
}

func scan(r scanner) (Profile, error) {
	var p Profile
	var prs, found, ci, former string
	var joined, updated int64
	err := r.Scan(&p.Name, &p.Kind, &p.Project, &p.Task, &p.Status, &p.CWD, &p.Branch, &prs, &p.About, &p.Icon, &p.Pigment, &joined, &updated,
		&p.SessionState, &found, &ci, &former)
	p.CI = parseCI(ci)
	p.Formerly = parseFormerly(former)
	p.PRs = parsePRs(prs)
	if f := parsePRs(found); len(f) > 0 {
		slices.Sort(f)
		p.FoundPRs = slices.Compact(f) // the same number in two repositories is listed once
	}
	p.JoinedAt, p.UpdatedAt = time.UnixMilli(joined), time.UnixMilli(updated)
	return p, err
}

func load(ctx context.Context, tx *sql.Tx, name string) (Profile, error) {
	p, err := scan(tx.QueryRowContext(ctx, `SELECT name, kind, project, task, status, cwd, branch, prs, about, icon, pigment, joined_at, updated_at, ?, '',
		`+ciColumn+`, ''
		FROM agents AS a WHERE name = ?`, Offline, name))
	if errors.Is(err, sql.ErrNoRows) {
		return p, unknown(name)
	}
	return p, err
}

// nextBranch returns the branch for an agent moving from prevDir to dir: the branch checked out
// in dir, except that a detached commit in the same checkout keeps the previous branch.
func nextBranch(prevBranch, prevDir, dir string) string {
	h := gitinfo.Read(dir)
	if h.Detached && prevBranch != "" && prevDir != "" && h.GitDir == gitinfo.Read(prevDir).GitDir {
		return prevBranch
	}
	return h.Branch
}

// resolve cleans dir and follows symlinks when it exists, so the same directory always has the
// same name.
func resolve(dir string) string {
	d := filepath.Clean(dir)
	if r, err := filepath.EvalSymlinks(d); err == nil {
		return r
	}
	return d
}

// within reports whether path is dir or lies inside it.
func within(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../") && !filepath.IsAbs(rel)
}

func parsePRs(s string) []int {
	var out []int
	for _, f := range strings.Fields(s) {
		if n, err := strconv.Atoi(f); err == nil {
			out = append(out, n)
		}
	}
	return out
}

func joinPRs(prs []int) string {
	parts := make([]string, len(prs))
	for i, n := range prs {
		parts[i] = strconv.Itoa(n)
	}
	return strings.Join(parts, " ")
}

func sortedKeys(m map[int]bool) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
