package agents_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vsem-azamat/agora/internal/agents"
	"github.com/vsem-azamat/agora/internal/queue"
	"github.com/vsem-azamat/agora/internal/rooms"
	"github.com/vsem-azamat/agora/internal/sessions"
	"github.com/vsem-azamat/agora/internal/store"
)

var ctx = context.Background()

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type env struct {
	a     *agents.Agents
	s     *sessions.Sessions
	q     *queue.Queue
	clock *clock
}

func newEnv(t *testing.T) env {
	t.Helper()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	c := &clock{t: time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)}
	q := queue.New(db, c.now)
	return env{a: agents.New(db, c.now), s: sessions.New(db, q, rooms.New(db, c.now), c.now), q: q, clock: c}
}

func str(s string) *string { return &s }

// checkout makes a fake git checkout with a main repo on branch main and a worktree on fix/login-timeout.
func checkout(t *testing.T) (repo, worktree string) {
	t.Helper()
	repo = t.TempDir()
	write := func(p, body string) {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(repo, ".git", "HEAD"), "ref: refs/heads/main\n")
	worktree = filepath.Join(repo, ".worktrees", "login-fix")
	gitdir := filepath.Join(repo, ".git", "worktrees", "login-fix")
	write(filepath.Join(gitdir, "HEAD"), "ref: refs/heads/fix/login-timeout\n")
	write(filepath.Join(worktree, ".git"), "gitdir: "+gitdir+"\n")
	return repo, worktree
}

func (e env) join(t *testing.T, name, session string) {
	t.Helper()
	if _, err := e.s.Join(ctx, name, session, false); err != nil {
		t.Fatal(err)
	}
}

func TestProfileAfterJoining(t *testing.T) {
	e := newEnv(t)
	repo, _ := checkout(t)
	e.join(t, "builder", "")
	p, err := e.a.Update(ctx, "builder", agents.Update{Kind: str("claude-code"), Project: str("example-app"), Task: str("fix login timeout"), Status: str("working"), CWD: str(repo)})
	if err != nil {
		t.Fatal(err)
	}
	if p.Kind != "claude-code" || p.Project != "example-app" || p.Task != "fix login timeout" || p.Status != "working" || p.CWD != repo || p.Branch != "main" {
		t.Fatalf("profile %+v", p)
	}
}

func TestUpdatingTheTaskKeepsOtherFields(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "")
	if _, err := e.a.Update(ctx, "builder", agents.Update{Project: str("example-app")}); err != nil {
		t.Fatal(err)
	}
	e.clock.add(time.Minute)
	p, _ := e.a.Update(ctx, "builder", agents.Update{Task: str("write tests")})
	if p.Project != "example-app" || p.Task != "write tests" || !p.UpdatedAt.Equal(e.clock.now()) {
		t.Fatalf("profile %+v", p)
	}
}

func TestUnknownAgentIsRefused(t *testing.T) {
	e := newEnv(t)
	if _, err := e.a.Update(ctx, "ghost", agents.Update{Task: str("x")}); !errors.Is(err, agents.ErrUnknown) {
		t.Fatalf("err = %v", err)
	}
}

func TestBranchFollowsDirectoryAndSurvivesDetachedHead(t *testing.T) {
	e := newEnv(t)
	repo, wt := checkout(t)
	e.join(t, "builder", "")
	p, _ := e.a.Update(ctx, "builder", agents.Update{CWD: str(wt)})
	if p.Branch != "fix/login-timeout" {
		t.Fatalf("branch %q", p.Branch)
	}
	if err := os.WriteFile(filepath.Join(repo, ".git", "worktrees", "login-fix", "HEAD"), []byte("0123456789abcdef0123456789abcdef01234567\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, _ = e.a.Update(ctx, "builder", agents.Update{CWD: str(wt)})
	if p.Branch != "fix/login-timeout" {
		t.Fatalf("detached: branch %q", p.Branch)
	}
	p, _ = e.a.Update(ctx, "builder", agents.Update{CWD: str(t.TempDir())})
	if p.Branch != "" {
		t.Fatalf("outside: branch %q", p.Branch)
	}
}

func TestDirectoryFollowsTheSessionButKeepsADeclaredWorktree(t *testing.T) {
	e := newEnv(t)
	repo, wt := checkout(t)
	e.join(t, "builder", "session-1")
	if _, err := e.a.Update(ctx, "builder", agents.Update{CWD: str(wt)}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Report(ctx, sessions.Report{SessionID: "session-1", Event: sessions.Prompt, CWD: repo}); err != nil {
		t.Fatal(err)
	}
	if p, _ := e.a.Get(ctx, "builder"); p.CWD != wt {
		t.Fatalf("worktree lost: %q", p.CWD)
	}
	other := t.TempDir()
	if _, err := e.s.Report(ctx, sessions.Report{SessionID: "session-1", Event: sessions.Tool, CWD: other}); err != nil {
		t.Fatal(err)
	}
	if p, _ := e.a.Get(ctx, "builder"); p.CWD != other || p.Branch != "" {
		t.Fatalf("profile %+v", p)
	}
}

func TestDeclaredPullRequests(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "")
	if _, err := e.a.Update(ctx, "builder", agents.Update{AddPRs: []int{42, 41, 57}}); err != nil {
		t.Fatal(err)
	}
	p, _ := e.a.Update(ctx, "builder", agents.Update{DropPRs: []int{41}})
	if len(p.PRs) != 2 || p.PRs[0] != 42 || p.PRs[1] != 57 {
		t.Fatalf("prs %v", p.PRs)
	}
}

func TestActivity(t *testing.T) {
	e := newEnv(t)
	e.join(t, "live", "session-1")
	if _, err := e.s.Report(ctx, sessions.Report{SessionID: "session-1", Event: sessions.Start}); err != nil {
		t.Fatal(err)
	}
	e.join(t, "silent", "")
	if _, err := e.a.Update(ctx, "silent", agents.Update{Task: str("x")}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.a.Update(ctx, "live", agents.Update{Task: str("y")}); err != nil {
		t.Fatal(err)
	}
	e.clock.add(7 * time.Hour)
	list, _ := e.a.List(ctx, false)
	if len(list) != 1 || list[0].Name != "live" {
		t.Fatalf("active %+v", list)
	}
	all, _ := e.a.List(ctx, true)
	if len(all) != 2 {
		t.Fatalf("all %+v", all)
	}
}

func TestLeavingReleasesPlaces(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "")
	if _, _, err := e.q.Join(ctx, "example-app/merge", "builder", "", 0, false); err != nil {
		t.Fatal(err)
	}
	left, err := e.s.Leave(ctx, "builder")
	if err != nil || len(left) != 1 {
		t.Fatalf("left %v err %v", left, err)
	}
	p, _ := e.a.Get(ctx, "builder")
	if p.Status != agents.Left || p.Active {
		t.Fatalf("profile %+v", p)
	}
	if entries, _ := e.q.EntriesOf(ctx, "builder"); len(entries) != 0 {
		t.Fatalf("still queued %+v", entries)
	}
}

func TestLeavingInALiveSessionSticks(t *testing.T) {
	e := newEnv(t)
	report := func(ev sessions.Event) {
		t.Helper()
		if _, err := e.s.Report(ctx, sessions.Report{SessionID: "session-1", Kind: "claude-code", Event: ev, CWD: "/src/example-app"}); err != nil {
			t.Fatal(err)
		}
	}
	report(sessions.Start)
	e.join(t, "builder", "session-1")
	if _, err := e.s.Leave(ctx, "builder"); err != nil {
		t.Fatal(err)
	}
	for _, ev := range []sessions.Event{sessions.Tool, sessions.Stop, sessions.Prompt} {
		report(ev)
	}
	if p, _ := e.a.Get(ctx, "builder"); p.Status != agents.Left || p.Active {
		t.Fatalf("profile %+v", p)
	}
	if name, err := e.s.Resolve(ctx, "session-1"); err != nil || name != "" {
		t.Fatalf("session still acts as %q (err %v)", name, err)
	}
	e.join(t, "builder", "session-1")
	if name, _ := e.s.Resolve(ctx, "session-1"); name != "builder" {
		t.Fatalf("rejoined session acts as %q", name)
	}
}

func TestSessionEndMarksTheAgentLeft(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "session-1")
	if _, err := e.s.Report(ctx, sessions.Report{SessionID: "session-1", Event: sessions.End}); err != nil {
		t.Fatal(err)
	}
	if p, _ := e.a.Get(ctx, "builder"); p.Status != agents.Left {
		t.Fatalf("status %q", p.Status)
	}
}

func TestWho(t *testing.T) {
	e := newEnv(t)
	repo, wt := checkout(t)
	e.join(t, "builder", "")
	if _, err := e.a.Update(ctx, "builder", agents.Update{CWD: str(wt), AddPRs: []int{57}}); err != nil {
		t.Fatal(err)
	}
	e.join(t, "reviewer", "")
	if _, err := e.a.Update(ctx, "reviewer", agents.Update{CWD: str(t.TempDir())}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		query string
		path  bool
		want  string
	}{
		{"#57", false, "builder"},
		{"57", false, "builder"},
		{repo, true, "builder"},
		{filepath.Join(wt, "src"), true, "builder"},
		{"login", false, "builder"},
		{"fix/login-timeout", false, "builder"},
		{"reviewer", false, "reviewer"},
	} {
		got, err := e.a.Who(ctx, tc.query, tc.path, false)
		if err != nil || len(got) != 1 || got[0].Name != tc.want {
			t.Errorf("%q: %+v %v", tc.query, got, err)
		}
	}
	if got, _ := e.a.Who(ctx, "fi", false, false); len(got) != 0 {
		t.Errorf("short fragment matched %+v", got)
	}
}

func TestResumedSessionBringsALeftAgentBack(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "session-1")
	if _, err := e.s.Report(ctx, sessions.Report{SessionID: "session-1", Event: sessions.End}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Report(ctx, sessions.Report{SessionID: "session-1", Event: sessions.Start}); err != nil {
		t.Fatal(err)
	}
	p, _ := e.a.Get(ctx, "builder")
	if p.Status != agents.Working || !p.Active {
		t.Fatalf("profile %+v", p)
	}
}

func TestSettingStatusLeftIsRefused(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "")
	if _, err := e.a.Update(ctx, "builder", agents.Update{Status: str("left")}); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("err = %v", err)
	}
}

func TestRelativeDirectoryIsRefused(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "")
	if _, err := e.a.Update(ctx, "builder", agents.Update{CWD: str("rel/dir")}); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("err = %v", err)
	}
}

func TestDetachedCommitInAnotherRepoDoesNotKeepTheOldBranch(t *testing.T) {
	e := newEnv(t)
	repo, _ := checkout(t)
	other := t.TempDir()
	if err := os.MkdirAll(filepath.Join(other, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, ".git", "HEAD"), []byte("0123456789abcdef0123456789abcdef01234567\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.join(t, "builder", "")
	if _, err := e.a.Update(ctx, "builder", agents.Update{CWD: str(repo)}); err != nil {
		t.Fatal(err)
	}
	p, _ := e.a.Update(ctx, "builder", agents.Update{CWD: str(other)})
	if p.Branch != "0123456789ab" {
		t.Fatalf("branch %q", p.Branch)
	}
}

func TestWhoFindsAFormerName(t *testing.T) {
	e := newEnv(t)
	e.join(t, "fixer", "")
	if err := e.s.Rename(ctx, "fixer", "docs-writer"); err != nil {
		t.Fatal(err)
	}
	if got, err := e.a.Who(ctx, "fixer", false, true); err != nil || len(got) != 1 || got[0].Name != "docs-writer" {
		t.Fatalf("who fixer: %+v %v", got, err)
	}
}

func TestSigilAndPigment(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "")
	p, err := e.a.Update(ctx, "builder", agents.Update{Icon: str("lyre"), Pigment: str("ochre")})
	if err != nil || p.Icon != "lyre" || p.Pigment != "ochre" {
		t.Fatalf("profile %+v, err %v", p, err)
	}
	for _, u := range []agents.Update{{Icon: str("owl")}, {Pigment: str("pink")}, {Icon: str("lyre"), Pigment: str("Ochre")}} {
		_, err := e.a.Update(ctx, "builder", u)
		if !errors.Is(err, store.ErrInvalid) || !strings.Contains(err.Error(), "one of") {
			t.Errorf("%+v: err = %v", u, err)
		}
	}
	if p, _ := e.a.Get(ctx, "builder"); p.Icon != "lyre" || p.Pigment != "ochre" {
		t.Fatalf("a refused update changed the profile: %+v", p)
	}
	if p, err := e.a.Update(ctx, "builder", agents.Update{Icon: str("")}); err != nil || p.Icon != "" || p.Pigment != "ochre" {
		t.Fatalf("unset: %+v, err %v", p, err)
	}
}
