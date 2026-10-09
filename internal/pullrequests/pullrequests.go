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
)

const (
	// LookupTimeout bounds one request to a forge.
	LookupTimeout = 30 * time.Second
	// MaxFailedNames is how many failed checks a red message names.
	MaxFailedNames = 5
)

// CI states.
const (
	Green   = "green"
	Red     = "red"
	Pending = "pending"
)

// Watcher looks up pull requests in rounds and posts CI messages.
type Watcher struct {
	db     *sql.DB
	agents *agents.Agents
	rooms  *rooms.Rooms
	forges forge.Forges
	log    *slog.Logger
}

// New returns a watcher that asks forges, by host, about the repositories of active agents.
func New(db *sql.DB, a *agents.Agents, r *rooms.Rooms, forges forge.Forges, log *slog.Logger) *Watcher {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Watcher{db: db, agents: a, rooms: r, forges: forges, log: log}
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
// followed for them and posts a message for every CI state that turned green or red. Failures
// are logged; it returns how many messages it posted.
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
		res, err := w.lookup(ctx, g)
		if err != nil {
			w.log.Error("pull requests: lookup failed", "repo", g.repo.Key(), "err", err)
			continue
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
		common := gitinfo.Read(p.CWD).CommonDir
		if common == "" {
			continue
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
		get(repo).current[p.Name] = member{branch: p.Branch, declared: p.PRs}
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
	open := map[int]forge.PR{}
	for _, p := range res.PRs {
		if p.Open {
			open[p.Number] = p
		}
	}
	// agent -> number -> found from the branch (false: followed because declared)
	follow := map[string]map[int]bool{}
	mark := func(agent string, n int, found bool) {
		if follow[agent] == nil {
			follow[agent] = map[int]bool{}
		}
		follow[agent][n] = follow[agent][n] || found
	}
	for agent, ns := range g.found {
		for _, n := range ns {
			if _, ok := open[n]; ok {
				mark(agent, n, true)
			}
		}
	}
	for agent, m := range g.current {
		if follow[agent] == nil { // an agent with nothing open still gets its stale rows removed
			follow[agent] = map[int]bool{}
		}
		if m.branch != "" && m.branch != res.DefaultBranch {
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

	key := g.repo.Key()
	posted := 0
	var notes []string
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	for _, agent := range slices.Sorted(maps.Keys(follow)) {
		nums := follow[agent]
		reported := map[int]string{}
		rows, err := tx.QueryContext(ctx, `SELECT number, reported FROM pull_requests WHERE agent = ? AND repo = ?`, agent, key)
		if err != nil {
			return 0, err
		}
		for rows.Next() {
			var n int
			var r string
			if err := rows.Scan(&n, &r); err != nil {
				rows.Close()
				return 0, err
			}
			reported[n] = r
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return 0, err
		}
		for n := range reported {
			if _, ok := nums[n]; !ok { // closed, merged, or no longer declared
				if _, err := tx.ExecContext(ctx, `DELETE FROM pull_requests WHERE agent = ? AND repo = ? AND number = ?`, agent, key, n); err != nil {
					return 0, err
				}
			}
		}
		if _, current := g.current[agent]; current {
			// declared pull requests are followed only in the repository the agent works in now
			if _, err := tx.ExecContext(ctx, `DELETE FROM pull_requests WHERE agent = ? AND repo != ? AND found = 0`, agent, key); err != nil {
				return 0, err
			}
		}
		for _, n := range slices.Sorted(maps.Keys(nums)) {
			pr := open[n]
			state, failed := CI(pr)
			report := reported[n]
			var body string
			if state == Green || state == Red {
				if k := state + " " + pr.Head; k != report {
					report = k
					body = Message(agent, n, state, failed, pr.Conflicts)
				}
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO pull_requests (agent, repo, number, found, reported) VALUES (?, ?, ?, ?, ?)
				ON CONFLICT (agent, repo, number) DO UPDATE SET found = excluded.found, reported = excluded.reported`,
				agent, key, n, nums[n], report); err != nil {
				return 0, err
			}
			if body == "" {
				continue
			}
			followed, err := w.rooms.FollowedTx(ctx, tx, agent)
			if err != nil {
				return 0, err
			}
			room := rooms.General
			if len(followed) > 1 {
				room = followed[1] // the alphabetically first room other than #general
			}
			if _, err := w.rooms.PostTx(ctx, tx, rooms.Board, room, body, 0); err != nil {
				return 0, err
			}
			posted++
			notes = append(notes, fmt.Sprintf("%s #%d %s", agent, n, state))
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	if posted > 0 {
		w.log.Info("CI messages posted", "repo", key, "messages", notes)
	}
	return posted, nil
}

// CI returns the CI state of an open pull request ("" when it has none: a draft, or no
// finished check other than cancelled ones) and the names of its failed checks.
func CI(pr forge.PR) (string, []string) {
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
	case unfinished:
		return Pending, nil
	case succeeded:
		return Green, nil
	}
	return "", nil
}

// Message is the text the board posts when CI on pull request n of agent turns green or red.
func Message(agent string, n int, state string, failed []string, conflicts bool) string {
	if state == Green {
		if conflicts {
			return fmt.Sprintf("@%s CI is green on #%d, but it conflicts with its base.", agent, n)
		}
		return fmt.Sprintf("@%s CI is green on #%d.", agent, n)
	}
	names := strings.Join(failed[:min(len(failed), MaxFailedNames)], ", ")
	if more := len(failed) - MaxFailedNames; more > 0 {
		names += fmt.Sprintf(" and %d more", more)
	}
	return fmt.Sprintf("@%s CI failed on #%d: %s.", agent, n, names)
}
