package agents_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/vsem-azamat/agora/internal/agents"
	"github.com/vsem-azamat/agora/internal/queue"
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
	return env{a: agents.New(db, q, c.now), s: sessions.New(db, q, c.now), q: q, clock: c}
}

func str(s string) *string { return &s }

// checkout makes a fake git checkout with a main repo on branch main and a worktree on fix/login-timeout.
func checkout(t *testing.T) (repo, worktree string) {
	t.Helper()
	repo = t.TempDir()
	write := func(p, body string) {
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o644)
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
	e.a.Update(ctx, "builder", agents.Update{Project: str("example-app")})
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
	os.WriteFile(filepath.Join(repo, ".git", "worktrees", "login-fix", "HEAD"), []byte("0123456789abcdef0123456789abcdef01234567\n"), 0o644)
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
	e.a.Update(ctx, "builder", agents.Update{CWD: str(wt)})
	e.s.Report(ctx, sessions.Report{SessionID: "session-1", Event: sessions.Prompt, CWD: repo})
	if p, _ := e.a.Get(ctx, "builder"); p.CWD != wt {
		t.Fatalf("worktree lost: %q", p.CWD)
	}
	other := t.TempDir()
	e.s.Report(ctx, sessions.Report{SessionID: "session-1", Event: sessions.Tool, CWD: other})
	if p, _ := e.a.Get(ctx, "builder"); p.CWD != other || p.Branch != "" {
		t.Fatalf("profile %+v", p)
	}
}

func TestDeclaredPullRequests(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "")
	e.a.Update(ctx, "builder", agents.Update{AddPRs: []int{42, 41, 57}})
	p, _ := e.a.Update(ctx, "builder", agents.Update{DropPRs: []int{41}})
	if len(p.PRs) != 2 || p.PRs[0] != 42 || p.PRs[1] != 57 {
		t.Fatalf("prs %v", p.PRs)
	}
}

func TestActivity(t *testing.T) {
	e := newEnv(t)
	e.join(t, "live", "session-1")
	e.s.Report(ctx, sessions.Report{SessionID: "session-1", Event: sessions.Start})
	e.join(t, "silent", "")
	e.a.Update(ctx, "silent", agents.Update{Task: str("x")})
	e.a.Update(ctx, "live", agents.Update{Task: str("y")})
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
	e.q.Join(ctx, "example-app/merge", "builder", "", 0, false)
	left, err := e.a.Leave(ctx, "builder")
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

func TestSessionEndMarksTheAgentLeft(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "session-1")
	e.s.Report(ctx, sessions.Report{SessionID: "session-1", Event: sessions.End})
	if p, _ := e.a.Get(ctx, "builder"); p.Status != agents.Left {
		t.Fatalf("status %q", p.Status)
	}
}

func TestWho(t *testing.T) {
	e := newEnv(t)
	repo, wt := checkout(t)
	e.join(t, "builder", "")
	e.a.Update(ctx, "builder", agents.Update{CWD: str(wt), AddPRs: []int{57}})
	e.join(t, "reviewer", "")
	e.a.Update(ctx, "reviewer", agents.Update{CWD: str(t.TempDir())})
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
	e.s.Report(ctx, sessions.Report{SessionID: "session-1", Event: sessions.End})
	e.s.Report(ctx, sessions.Report{SessionID: "session-1", Event: sessions.Start})
	p, _ := e.a.Get(ctx, "builder")
	if p.Status != agents.Working || !p.Active {
		t.Fatalf("profile %+v", p)
	}
}

func TestSettingStatusLeftIsRefused(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "")
	if _, err := e.a.Update(ctx, "builder", agents.Update{Status: str("left")}); !errors.Is(err, agents.ErrInvalid) {
		t.Fatalf("err = %v", err)
	}
}

func TestRelativeDirectoryIsRefused(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "")
	if _, err := e.a.Update(ctx, "builder", agents.Update{CWD: str("rel/dir")}); !errors.Is(err, agents.ErrInvalid) {
		t.Fatalf("err = %v", err)
	}
}

func TestDetachedCommitInAnotherRepoDoesNotKeepTheOldBranch(t *testing.T) {
	e := newEnv(t)
	repo, _ := checkout(t)
	other := t.TempDir()
	os.MkdirAll(filepath.Join(other, ".git"), 0o755)
	os.WriteFile(filepath.Join(other, ".git", "HEAD"), []byte("0123456789abcdef0123456789abcdef01234567\n"), 0o644)
	e.join(t, "builder", "")
	e.a.Update(ctx, "builder", agents.Update{CWD: str(repo)})
	p, _ := e.a.Update(ctx, "builder", agents.Update{CWD: str(other)})
	if p.Branch != "0123456789ab" {
		t.Fatalf("branch %q", p.Branch)
	}
}
