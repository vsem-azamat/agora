package pullrequests_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vsem-azamat/agora/internal/agents"
	"github.com/vsem-azamat/agora/internal/forge"
	"github.com/vsem-azamat/agora/internal/pullrequests"
	"github.com/vsem-azamat/agora/internal/queue"
	"github.com/vsem-azamat/agora/internal/rooms"
	"github.com/vsem-azamat/agora/internal/sessions"
	"github.com/vsem-azamat/agora/internal/store"
)

var ctx = context.Background()

const app = "github.com/example-org/example-app"

// fakeForge answers lookups from a table of pull requests per repository.
type fakeForge struct {
	mu      sync.Mutex
	def     map[string]string // repository key -> default branch ("main" when unset)
	prs     map[string][]forge.PR
	fail    map[string]error
	calls   map[string]int
	queries map[string]forge.Query
}

func newFake() *fakeForge {
	return &fakeForge{def: map[string]string{}, prs: map[string][]forge.PR{}, fail: map[string]error{}, calls: map[string]int{}, queries: map[string]forge.Query{}}
}

func (f *fakeForge) Lookup(_ context.Context, repo forge.Repo, q forge.Query) (forge.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := repo.Key()
	f.calls[k]++
	f.queries[k] = q
	if err := f.fail[k]; err != nil {
		return forge.Result{}, err
	}
	res := forge.Result{DefaultBranch: f.def[k]}
	if res.DefaultBranch == "" {
		res.DefaultBranch = "main"
	}
	for _, p := range f.prs[k] {
		if (p.Open && slices.Contains(q.Branches, p.Branch)) || slices.Contains(q.Numbers, p.Number) {
			res.PRs = append(res.PRs, p)
		}
	}
	return res, nil
}

// set replaces pull request n of repo (adding it when new).
func (f *fakeForge) set(repo string, p forge.PR) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, x := range f.prs[repo] {
		if x.Number == p.Number {
			f.prs[repo][i] = p
			return
		}
	}
	f.prs[repo] = append(f.prs[repo], p)
}

type env struct {
	db     *sql.DB
	a      *agents.Agents
	s      *sessions.Sessions
	r      *rooms.Rooms
	fake   *fakeForge
	w      *pullrequests.Watcher
	log    *bytes.Buffer
	forges forge.Forges
}

func newEnv(t *testing.T) *env {
	t.Helper()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	now := func() time.Time { return time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC) }
	q := queue.New(db, now)
	r := rooms.New(db, now)
	e := &env{db: db, a: agents.New(db, q, now), s: sessions.New(db, q, r, now), r: r, fake: newFake(), log: &bytes.Buffer{}}
	e.forges = forge.Forges{"github.com": e.fake}
	e.restart()
	return e
}

// restart builds a new watcher over the same database, as a restarted hub would.
func (e *env) restart() {
	e.w = pullrequests.New(e.db, e.a, e.r, e.forges, slog.New(slog.NewTextHandler(e.log, nil)))
}

func write(t *testing.T, p, body string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// repo makes a checkout on main with the given origin ("" for none).
func repo(t *testing.T, origin string) string {
	t.Helper()
	dir := t.TempDir()
	write(t, filepath.Join(dir, ".git", "HEAD"), "ref: refs/heads/main\n")
	config := "[core]\n\tbare = false\n"
	if origin != "" {
		config += "[remote \"origin\"]\n\turl = " + origin + "\n"
	}
	write(t, filepath.Join(dir, ".git", "config"), config)
	return dir
}

// worktree adds a worktree of dir on branch and returns its path.
func worktree(t *testing.T, dir, name, branch string) string {
	t.Helper()
	wt := filepath.Join(dir, ".worktrees", name)
	gitdir := filepath.Join(dir, ".git", "worktrees", name)
	write(t, filepath.Join(gitdir, "HEAD"), "ref: refs/heads/"+branch+"\n")
	write(t, filepath.Join(gitdir, "commondir"), "../..\n")
	write(t, filepath.Join(wt, ".git"), "gitdir: "+gitdir+"\n")
	return wt
}

func checkout(t *testing.T, dir, branch string) {
	t.Helper()
	write(t, filepath.Join(dir, ".git", "HEAD"), "ref: refs/heads/"+branch+"\n")
}

// agent joins name working in dir.
func (e *env) agent(t *testing.T, name, dir string, declared ...int) {
	t.Helper()
	if _, err := e.s.Join(ctx, name, "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := e.a.Update(ctx, name, agents.Update{CWD: &dir, AddPRs: declared}); err != nil {
		t.Fatal(err)
	}
}

func (e *env) profile(t *testing.T, name string) agents.Profile {
	t.Helper()
	p, err := e.a.Get(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// board returns the bodies the board posted, as "room: body".
func (e *env) board(t *testing.T) []string {
	t.Helper()
	rows, err := e.db.QueryContext(ctx, `SELECT room, body FROM messages WHERE author = 'agora' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var room, body string
		rows.Scan(&room, &body)
		out = append(out, room+": "+body)
	}
	return out
}

func green(n int, branch, head string) forge.PR {
	return forge.PR{Number: n, Branch: branch, Head: head, Open: true, Checks: []forge.Check{{Name: "lint", Outcome: forge.Succeeded}, {Name: "test", Outcome: forge.Succeeded}}}
}

func TestPullRequestIsFoundFromTheAgentsBranch(t *testing.T) {
	e := newEnv(t)
	dir := repo(t, "git@github.com:example-org/example-app.git")
	wt := worktree(t, dir, "login-fix", "fix/login-timeout")
	e.agent(t, "builder", wt)
	before := e.profile(t, "builder").UpdatedAt
	e.fake.set(app, forge.PR{Number: 57, Branch: "fix/login-timeout", Head: "a1", Open: true})
	e.fake.set(app, forge.PR{Number: 58, Branch: "fix/login-timeout", Head: "b1", Open: true, Fork: true})
	e.w.Round(ctx)

	p := e.profile(t, "builder")
	if !slices.Equal(p.FoundPRs, []int{57}) || len(p.PRs) != 0 {
		t.Fatalf("found %v, declared %v", p.FoundPRs, p.PRs)
	}
	if !p.UpdatedAt.Equal(before) {
		t.Fatalf("lookup counted as activity: %v -> %v", before, p.UpdatedAt)
	}
	who, _ := e.a.Who(ctx, "#57", false, false)
	if len(who) != 1 || who[0].Name != "builder" {
		t.Fatalf("who #57: %+v", who)
	}
	if who, _ := e.a.Who(ctx, "58", false, false); len(who) != 0 {
		t.Fatalf("fork pull request found: %+v", who)
	}
}

func TestOneLookupPerRepository(t *testing.T) {
	e := newEnv(t)
	dir := repo(t, "https://github.com/example-org/example-app.git")
	e.agent(t, "alpha", worktree(t, dir, "a", "feat/a"))
	e.agent(t, "beta", worktree(t, dir, "b", "feat/b"))
	e.agent(t, "gamma", dir)
	clone := repo(t, "git@github.com:example-org/example-app")
	checkout(t, clone, "feat/c")
	e.agent(t, "delta", clone)
	e.w.Round(ctx)
	if e.fake.calls[app] != 1 || len(e.fake.calls) != 1 {
		t.Fatalf("calls %v", e.fake.calls)
	}
	got := slices.Sorted(slices.Values(e.fake.queries[app].Branches))
	if !slices.Equal(got, []string{"feat/a", "feat/b", "feat/c", "main"}) {
		t.Fatalf("branches %v", got)
	}
}

func TestTheDefaultBranchOwnsNoPullRequest(t *testing.T) {
	e := newEnv(t)
	dir := repo(t, "git@github.com:example-org/example-app.git")
	checkout(t, dir, "dev")
	e.agent(t, "builder", dir)
	e.fake.def[app] = "dev"
	e.fake.set(app, green(60, "dev", "c1"))
	e.w.Round(ctx)
	if p := e.profile(t, "builder"); len(p.FoundPRs) != 0 {
		t.Fatalf("found %v", p.FoundPRs)
	}
	if b := e.board(t); len(b) != 0 {
		t.Fatalf("messages %v", b)
	}
}

func TestFoundPullRequestStaysWhileOpenAndIsDroppedWhenMerged(t *testing.T) {
	e := newEnv(t)
	dir := repo(t, "git@github.com:example-org/example-app.git")
	wt := worktree(t, dir, "work", "fix/login-timeout")
	e.agent(t, "builder", wt)
	e.fake.set(app, forge.PR{Number: 57, Branch: "fix/login-timeout", Head: "a1", Open: true})
	e.w.Round(ctx)

	worktree(t, dir, "work", "feat/export") // the agent moves on in the same worktree
	e.a.Update(ctx, "builder", agents.Update{CWD: &wt})
	e.w.Round(ctx)
	if p := e.profile(t, "builder"); p.Branch != "feat/export" || !slices.Equal(p.FoundPRs, []int{57}) {
		t.Fatalf("branch %q, found %v", p.Branch, p.FoundPRs)
	}
	if !slices.Contains(e.fake.queries[app].Numbers, 57) {
		t.Fatalf("57 not looked up: %+v", e.fake.queries[app])
	}

	merged := green(57, "fix/login-timeout", "a2")
	merged.Open = false
	e.fake.set(app, merged)
	e.w.Round(ctx)
	if p := e.profile(t, "builder"); len(p.FoundPRs) != 0 {
		t.Fatalf("merged still found: %v", p.FoundPRs)
	}
	if b := e.board(t); len(b) != 0 {
		t.Fatalf("messages %v", b)
	}
}

func TestDeclaredPullRequestIsFollowed(t *testing.T) {
	e := newEnv(t)
	dir := repo(t, "git@github.com:example-org/example-app.git")
	e.agent(t, "builder", dir, 61)
	e.fake.set(app, green(61, "feat/report", "d1"))
	e.w.Round(ctx)
	if b := e.board(t); !slices.Equal(b, []string{"general: @builder CI is green on #61."}) {
		t.Fatalf("messages %v", b)
	}
	if p := e.profile(t, "builder"); len(p.FoundPRs) != 0 || !slices.Equal(p.PRs, []int{61}) {
		t.Fatalf("profile %v %v", p.PRs, p.FoundPRs)
	}
}

func TestUnknownHostsAreSkippedQuietly(t *testing.T) {
	e := newEnv(t)
	e.agent(t, "builder", repo(t, "git@git.example.com:example-org/example-app.git"))
	e.agent(t, "local", repo(t, ""))
	e.agent(t, "outside", t.TempDir())
	e.w.Round(ctx)
	if len(e.fake.calls) != 0 {
		t.Fatalf("calls %v", e.fake.calls)
	}
	if strings.Contains(e.log.String(), "level=ERROR") || strings.Contains(e.log.String(), "level=WARN") {
		t.Fatalf("log: %s", e.log.String())
	}
}

func TestFailedLookupDoesNotStopTheRound(t *testing.T) {
	e := newEnv(t)
	broken := "github.com/example-org/broken"
	e.agent(t, "alpha", repo(t, "git@github.com:example-org/broken.git"), 5)
	e.agent(t, "builder", repo(t, "git@github.com:example-org/example-app.git"), 61)
	e.fake.fail[broken] = errors.New("gh: rate limited")
	e.fake.set(app, green(61, "feat/report", "d1"))
	e.w.Round(ctx)
	if b := e.board(t); len(b) != 1 {
		t.Fatalf("messages %v", b)
	}
	if !strings.Contains(e.log.String(), "rate limited") {
		t.Fatalf("failure not logged: %s", e.log.String())
	}
	e.w.Round(ctx)
	if e.fake.calls[broken] != 2 {
		t.Fatalf("not retried: %v", e.fake.calls)
	}
}

func TestCIMessages(t *testing.T) {
	failed := func(names ...string) []forge.Check {
		var out []forge.Check
		for _, n := range names {
			out = append(out, forge.Check{Name: n, Outcome: forge.Failed})
		}
		return out
	}
	cases := []struct {
		name string
		pr   forge.PR
		want string // "" for no message
	}{
		{"green", green(57, "x", "h"), "@builder CI is green on #57."},
		{"one failed", forge.PR{Number: 57, Open: true, Head: "h", Checks: append(failed("test"), forge.Check{Name: "lint", Outcome: forge.Succeeded})}, "@builder CI failed on #57: test."},
		{"failed list", forge.PR{Number: 57, Open: true, Head: "h", Checks: failed("lint", "test")}, "@builder CI failed on #57: lint, test."},
		{"capped", forge.PR{Number: 57, Open: true, Head: "h", Checks: failed("a", "b", "c", "d", "e", "f", "g", "g")}, "@builder CI failed on #57: a, b, c, d, e and 2 more."},
		{"superseded", forge.PR{Number: 57, Open: true, Head: "h", Checks: []forge.Check{{Name: "test", Outcome: forge.Cancelled}, {Name: "test", Outcome: forge.Succeeded}, {Name: "lint", Outcome: forge.Succeeded}}}, "@builder CI is green on #57."},
		{"running", forge.PR{Number: 57, Open: true, Head: "h", Checks: []forge.Check{{Name: "lint", Outcome: forge.Succeeded}, {Name: "test", Outcome: forge.Unfinished}}}, ""},
		{"failed while running", forge.PR{Number: 57, Open: true, Head: "h", Checks: append(failed("lint"), forge.Check{Name: "test", Outcome: forge.Unfinished})}, "@builder CI failed on #57: lint."},
		{"only cancelled", forge.PR{Number: 57, Open: true, Head: "h", Checks: []forge.Check{{Name: "test", Outcome: forge.Cancelled}}}, ""},
		{"no checks", forge.PR{Number: 57, Open: true, Head: "h"}, ""},
		{"draft", func() forge.PR { p := green(57, "x", "h"); p.Draft = true; return p }(), ""},
		{"conflict", func() forge.PR { p := green(57, "x", "h"); p.Conflicts = true; return p }(), "@builder CI is green on #57, but it conflicts with its base."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			e.agent(t, "builder", repo(t, "git@github.com:example-org/example-app.git"), 57)
			e.fake.set(app, c.pr)
			e.w.Round(ctx)
			b := e.board(t)
			if c.want == "" && len(b) != 0 || c.want != "" && !slices.Equal(b, []string{"general: " + c.want}) {
				t.Fatalf("messages %q, want %q", b, c.want)
			}
		})
	}
}

func TestCIIsReportedOncePerStateAndCommit(t *testing.T) {
	e := newEnv(t)
	e.agent(t, "builder", worktree(t, repo(t, "git@github.com:example-org/example-app.git"), "w", "fix/login-timeout"))
	pr := green(57, "fix/login-timeout", "a1")
	e.fake.set(app, pr)
	e.w.Round(ctx)
	e.w.Round(ctx)
	e.restart()
	e.w.Round(ctx)
	if b := e.board(t); len(b) != 1 {
		t.Fatalf("messages %v", b)
	}
	pr.Head = "a2" // a new push, CI green again
	e.fake.set(app, pr)
	e.w.Round(ctx)
	pr.Checks = []forge.Check{{Name: "test", Outcome: forge.Failed}}
	e.fake.set(app, pr) // a re-run on the same commit fails
	e.w.Round(ctx)
	want := []string{"general: @builder CI is green on #57.", "general: @builder CI is green on #57.", "general: @builder CI failed on #57: test."}
	if b := e.board(t); !slices.Equal(b, want) {
		t.Fatalf("messages %q", b)
	}
}

func TestMessageIsAddressedInTheFirstFollowedRoom(t *testing.T) {
	e := newEnv(t)
	e.agent(t, "builder", repo(t, "git@github.com:example-org/example-app.git"), 57)
	for _, room := range []string{"reviews", "example-app"} {
		if err := e.r.Create(ctx, room, "work on "+room, "builder"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.r.Subscribe(ctx, "builder", []string{"reviews", "example-app"}, true); err != nil {
		t.Fatal(err)
	}
	e.fake.set(app, green(57, "x", "h"))
	e.w.Round(ctx)
	if b := e.board(t); !slices.Equal(b, []string{"example-app: @builder CI is green on #57."}) {
		t.Fatalf("messages %v", b)
	}
	unread, _, err := e.r.Unread(ctx, "builder", true, 10)
	if err != nil || len(unread) != 1 || !unread[0].Addressed {
		t.Fatalf("not addressed to builder: %+v %v", unread, err)
	}
}

func TestInactiveAgentsAreNotLookedUp(t *testing.T) {
	e := newEnv(t)
	e.agent(t, "builder", repo(t, "git@github.com:example-org/example-app.git"), 57)
	if _, err := e.a.Leave(ctx, "builder"); err != nil {
		t.Fatal(err)
	}
	e.w.Round(ctx)
	if len(e.fake.calls) != 0 {
		t.Fatalf("calls %v", e.fake.calls)
	}
}
