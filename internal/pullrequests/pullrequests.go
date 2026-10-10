// Package pullrequests follows the pull requests agents work on and tells each agent when CI
// on one of them turns green or red.
package pullrequests

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/vsem-azamat/agora/internal/agents"
	"github.com/vsem-azamat/agora/internal/forge"
	"github.com/vsem-azamat/agora/internal/gitinfo"
	"github.com/vsem-azamat/agora/internal/rooms"
	"github.com/vsem-azamat/agora/internal/store"
)

const (
	// LookupTimeout bounds one request to a forge.
	LookupTimeout = 30 * time.Second
	// MaxFailedNames is how many failed checks a red message names.
	MaxFailedNames = 5
	// MaxSkip is the most rounds a repository whose lookups keep failing is skipped.
	MaxSkip = 7
)

// State is the CI state of a pull request.
type State string

// CI states.
const (
	Green   State = "green"
	Red     State = "red"
	Pending State = "pending"
	// Conflict is the state of a pull request that conflicts with its base branch and has no
	// failed check: CI does not run for it.
	Conflict State = "conflict"
)

// Watcher looks up pull requests in rounds and posts CI messages.
type Watcher struct {
	db     *sql.DB
	agents *agents.Agents
	rooms  *rooms.Rooms
	forges forge.Forges
	log    *slog.Logger

	failing map[string]*failing // by repository key; used by one round at a time
}

// failing tracks the consecutive failed lookups of a repository.
type failing struct {
	times int    // consecutive failures
	skip  int    // rounds left to skip
	err   string // the last error, logged once
}

// New returns a watcher that asks forges, by host, about the repositories of active agents.
func New(db *sql.DB, a *agents.Agents, r *rooms.Rooms, forges forge.Forges, log *slog.Logger) *Watcher {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Watcher{db: db, agents: a, rooms: r, forges: forges, log: log, failing: map[string]*failing{}}
}

// member is an active agent working in a repository now.
type member struct {
	branch   string
	declared []int
}

// group is one repository: the agents working in it now, and the pull requests found earlier
// for active agents, wherever they work now.
type group struct {
	repo    forge.Repo
	current map[string]member
	found   map[string][]int
}

// Round looks up every repository active agents work in, once each, updates the pull requests
// followed for them and posts a message for every CI state that turned green, red or
// conflicting. Failures are logged once and retried with growing gaps; it returns how many
// messages it posted.
func (w *Watcher) Round(ctx context.Context) int {
	groups, err := w.groups(ctx)
	if err != nil {
		w.log.Error("pull requests: list agents", "err", err)
		return 0
	}
	posted := 0
	for _, g := range groups {
		if ctx.Err() != nil {
			return posted
		}
		key := g.repo.Key()
		f := w.failing[key]
		if f != nil && f.skip > 0 {
			f.skip--
			continue
		}
		res, err := w.lookup(ctx, g)
		if err != nil {
			if ctx.Err() != nil {
				return posted
			}
			if f == nil {
				f = &failing{}
				w.failing[key] = f
			}
			// After n failures in a row the next 2^(n-1)-1 rounds are skipped, at most MaxSkip.
			f.times++
			f.skip = min(1<<(f.times-1)-1, MaxSkip)
			if msg := err.Error(); msg != f.err {
				f.err = msg
				w.log.Error("pull requests: lookup failed; retrying with growing gaps", "repo", key, "err", err)
			} else {
				w.log.Debug("pull requests: lookup failed again", "repo", key, "times", f.times)
			}
			continue
		}
		if f != nil {
			delete(w.failing, key)
			w.log.Info("pull requests: lookup works again", "repo", key)
		}
		n, err := w.apply(ctx, g, res)
		if err != nil {
			w.log.Error("pull requests: update", "repo", g.repo.Key(), "err", err)
		}
		posted += n
	}
	return posted
}

// groups collects the repositories of active agents that a known forge serves.
func (w *Watcher) groups(ctx context.Context) ([]*group, error) {
	list, err := w.agents.List(ctx, false)
	if err != nil {
		return nil, err
	}
	byKey := map[string]*group{}
	get := func(repo forge.Repo) *group {
		g := byKey[repo.Key()]
		if g == nil {
			g = &group{repo: repo, current: map[string]member{}, found: map[string][]int{}}
			byKey[repo.Key()] = g
		}
		return g
	}
	origins := map[string]string{} // common git dir -> origin URL, read once per round
	active := map[string]bool{}
	for _, p := range list {
		active[p.Name] = true
		if p.CWD == "" {
			continue
		}
		head := gitinfo.Read(p.CWD)
		common := head.CommonDir
		if common == "" {
			continue
		}
		branch := p.Branch
		if head.Detached {
			branch = "" // a detached checkout is matched by no branch
		}
		origin, ok := origins[common]
		if !ok {
			origin = gitinfo.Origin(common)
			origins[common] = origin
		}
		repo, ok := forge.ParseRemote(origin)
		if !ok || w.forges[repo.Host] == nil {
			continue
		}
		get(repo).current[p.Name] = member{branch: branch, declared: p.PRs}
	}
	rows, err := w.db.QueryContext(ctx, `SELECT agent, repo, number FROM pull_requests WHERE found = 1 ORDER BY agent, repo, number`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var agent, key string
		var n int
		if err := rows.Scan(&agent, &key, &n); err != nil {
			return nil, err
		}
		repo := forge.RepoFromKey(key)
		if !active[agent] || w.forges[repo.Host] == nil {
			continue
		}
		g := get(repo)
		g.found[agent] = append(g.found[agent], n)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]*group, 0, len(byKey))
	for _, k := range slices.Sorted(maps.Keys(byKey)) {
		out = append(out, byKey[k])
	}
	return out, nil
}

// lookup asks the repository's forge once, outside any transaction, with a timeout.
func (w *Watcher) lookup(ctx context.Context, g *group) (forge.Result, error) {
	var q forge.Query
	for _, m := range g.current {
		if m.branch != "" {
			q.Branches = append(q.Branches, m.branch)
		}
		q.Numbers = append(q.Numbers, m.declared...)
	}
	for _, ns := range g.found {
		q.Numbers = append(q.Numbers, ns...)
	}
	slices.Sort(q.Branches)
	q.Branches = slices.Compact(q.Branches)
	slices.Sort(q.Numbers)
	q.Numbers = slices.Compact(q.Numbers)
	ctx, cancel := context.WithTimeout(ctx, LookupTimeout)
	defer cancel()
	return w.forges[g.repo.Host].Lookup(ctx, g.repo, q)
}

// apply stores which pull requests each agent of the group follows and posts CI messages, in
// one transaction.
func (w *Watcher) apply(ctx context.Context, g *group, res forge.Result) (int, error) {
	open, gone := split(res)
	follow := following(g, res.DefaultBranch, open)
	key := g.repo.Key()
	var notes []string
	err := store.InTx(ctx, w.db, func(tx *sql.Tx) error {
		for _, agent := range slices.Sorted(maps.Keys(follow)) {
			n, err := w.sync(ctx, tx, g, agent, follow[agent], open, gone)
			if err != nil {
				return err
			}
			notes = append(notes, n...)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	if len(notes) > 0 {
		w.log.Info("CI messages posted", "repo", key, "messages", notes)
	}
	return len(notes), nil
}

// split sorts the forge's reply into open pull requests and the numbers that are closed,
// merged or do not exist.
func split(res forge.Result) (open map[int]forge.PR, gone map[int]bool) {
	open, gone = map[int]forge.PR{}, map[int]bool{}
	for _, p := range res.PRs {
		if p.Open {
			open[p.Number] = p
		} else {
			gone[p.Number] = true
		}
	}
	for _, n := range res.NotFound {
		gone[n] = true
	}
	return open, gone
}

// following works out the open pull requests each agent of the group follows: agent ->
// number -> found from the branch (false: followed because declared). Every agent of the group
// has an entry, so the pull requests it no longer follows are removed even if none is open.
func following(g *group, defaultBranch string, open map[int]forge.PR) map[string]map[int]bool {
	follow := map[string]map[int]bool{}
	mark := func(agent string, n int, found bool) {
		follow[agent][n] = follow[agent][n] || found
	}
	for agent, ns := range g.found {
		follow[agent] = map[int]bool{}
		for _, n := range ns {
			if _, ok := open[n]; ok {
				mark(agent, n, true)
			}
		}
	}
	for agent, m := range g.current {
		if follow[agent] == nil {
			follow[agent] = map[int]bool{}
		}
		if m.branch != "" && m.branch != defaultBranch {
			for _, p := range open {
				if !p.Fork && p.Branch == m.branch {
					mark(agent, p.Number, true)
				}
			}
		}
		for _, n := range m.declared {
			if _, ok := open[n]; ok {
				mark(agent, n, false)
			}
		}
	}
	return follow
}

// followed is a stored pull request row of one agent in one repository.
type followed struct {
	found    bool
	reported string // the state and head commit last reported, "green <sha>"
}

// sync stores the pull requests agent follows in the group's repository, drops the ones it no
// longer follows, and posts a message for each CI state that turned green, red or conflicting.
// It returns a note per message.
func (w *Watcher) sync(ctx context.Context, tx *sql.Tx, g *group, agent string, nums map[int]bool, open map[int]forge.PR, gone map[int]bool) ([]string, error) {
	key := g.repo.Key()
	stored, err := storedPRs(ctx, tx, agent, key)
	if err != nil {
		return nil, err
	}
	m, current := g.current[agent]
	for n, r := range stored {
		if _, ok := nums[n]; ok {
			continue
		}
		// Not followed this round: drop it when the forge says it is closed or gone, or when
		// it was followed only as a declared pull request the agent no longer declares. A
		// pull request the reply says nothing about keeps its row and what was reported.
		if gone[n] || (!r.found && current && !slices.Contains(m.declared, n)) {
			if _, err := tx.ExecContext(ctx, `DELETE FROM pull_requests WHERE agent = ? AND repo = ? AND number = ?`, agent, key, n); err != nil {
				return nil, err
			}
		}
	}
	if current {
		// declared pull requests are followed only in the repository the agent works in now
		if _, err := tx.ExecContext(ctx, `DELETE FROM pull_requests WHERE agent = ? AND repo != ? AND found = 0`, agent, key); err != nil {
			return nil, err
		}
	}
	var notes []string
	for _, n := range slices.Sorted(maps.Keys(nums)) {
		pr := open[n]
		state, failed := CI(pr)
		report := stored[n].reported
		var body string
		if state == Green || state == Red || state == Conflict {
			if k := string(state) + " " + pr.Head; k != report {
				report = k
				body = Message(agent, n, state, failed, pr.Merge == forge.Conflicting)
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO pull_requests (agent, repo, number, found, reported) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT (agent, repo, number) DO UPDATE SET found = excluded.found, reported = excluded.reported`,
			agent, key, n, nums[n], report); err != nil {
			return nil, err
		}
		if body == "" {
			continue
		}
		room, err := rooms.NoticeRoomTx(ctx, tx, agent)
		if err != nil {
			return nil, err
		}
		if _, err := w.rooms.PostTx(ctx, tx, rooms.Board, room, body, 0); err != nil {
			return nil, err
		}
		notes = append(notes, fmt.Sprintf("%s #%d %s", agent, n, state))
	}
	return notes, nil
}

// storedPRs returns the pull requests stored for agent in the repository with key, by number.
func storedPRs(ctx context.Context, tx *sql.Tx, agent, key string) (map[int]followed, error) {
	rows, err := tx.QueryContext(ctx, `SELECT number, found, reported FROM pull_requests WHERE agent = ? AND repo = ?`, agent, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close() // before the next statement: the database has one connection
	out := map[int]followed{}
	for rows.Next() {
		var n int
		var r followed
		if err := rows.Scan(&n, &r.found, &r.reported); err != nil {
			return nil, err
		}
		out[n] = r
	}
	return out, rows.Err()
}

// CI returns the CI state of an open pull request and the names of its failed checks:
//
//   - "" for a draft, or when no check other than cancelled ones finished;
//   - Red when any check failed, also while others run and whatever the merge state;
//   - Conflict when the pull request conflicts with its base branch, since CI does not run
//     for it then;
//   - Pending while the merge state is unknown, a check is unfinished, or the forge knows
//     more checks than it listed;
//   - Green otherwise, when at least one check succeeded.
func CI(pr forge.PR) (State, []string) {
	if pr.Draft {
		return "", nil
	}
	var failed []string
	var unfinished, succeeded bool
	for _, c := range pr.Checks {
		switch c.Outcome {
		case forge.Failed:
			if !slices.Contains(failed, c.Name) {
				failed = append(failed, c.Name)
			}
		case forge.Unfinished:
			unfinished = true
		case forge.Succeeded:
			succeeded = true
		}
	}
	switch {
	case len(failed) > 0:
		return Red, failed
	case pr.Merge == forge.Conflicting:
		return Conflict, nil
	case unfinished || pr.Incomplete || pr.Merge == forge.MergeUnknown:
		return Pending, nil
	case succeeded:
		return Green, nil
	}
	return "", nil
}

// Message is the text the board posts when pull request n of agent turns green, red or
// conflicting.
func Message(agent string, n int, state State, failed []string, conflicts bool) string {
	switch state {
	case Green:
		return fmt.Sprintf("@%s CI is green on #%d.", agent, n)
	case Conflict:
		return fmt.Sprintf("@%s #%d conflicts with its base; CI did not run.", agent, n)
	}
	names := strings.Join(failed[:min(len(failed), MaxFailedNames)], ", ")
	if more := len(failed) - MaxFailedNames; more > 0 {
		names += fmt.Sprintf(" and %d more", more)
	}
	text := fmt.Sprintf("@%s CI failed on #%d: %s.", agent, n, names)
	if conflicts {
		text += " It also conflicts with its base."
	}
	return text
}
