package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vsem-azamat/agora/internal/cli"
	"github.com/vsem-azamat/agora/internal/forge"
	"github.com/vsem-azamat/agora/internal/hub"
	"github.com/vsem-azamat/agora/internal/store"
)

// startHub runs a hub on a temporary socket and returns the socket path.
func startHub(t *testing.T) string { return startHubWith(t, nil) }

// startHubWith is startHub with the hub configured by configure before it serves.
func startHubWith(t *testing.T, configure func(*hub.Hub)) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "agora-test-") // short path: unix sockets have a length limit
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	stop := runHub(t, dir, configure)
	t.Cleanup(stop)
	return filepath.Join(dir, "hub.sock")
}

// runHub runs a hub with its socket and database in dir and returns a function that stops it.
func runHub(t *testing.T, dir string, configure func(*hub.Hub)) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	db, err := store.Open(ctx, filepath.Join(dir, "agora.db"))
	if err != nil {
		t.Fatal(err)
	}
	l, err := hub.Listen(filepath.Join(dir, "hub.sock"))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	h := hub.Open(db, nil, nil)
	if configure != nil {
		configure(h)
	}
	go func() {
		if err := h.Serve(ctx, l); err != nil {
			t.Error(err)
		}
		close(done)
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			cancel()
			<-done
			db.Close()
		})
	}
}

type result struct {
	code           int
	stdout, stderr string
}

func agora(ctx context.Context, socket, agent string, args ...string) result {
	var out, errOut bytes.Buffer
	all := append([]string{"--socket", socket, "--as", agent}, args...)
	code := cli.Run(ctx, all, &out, &errOut)
	return result{code, out.String(), errOut.String()}
}

func TestLockIsRefusedWithExitCodeTwo(t *testing.T) {
	socket := startHub(t)
	ctx := context.Background()
	if r := agora(ctx, socket, "a", "lock", "example-app/merge", "--ttl", "10m", "merging", "#57"); r.code != 0 || !strings.Contains(r.stdout, "locked example-app/merge") {
		t.Fatalf("first lock: %+v", r)
	}
	r := agora(ctx, socket, "b", "lock", "example-app/merge")
	if r.code != cli.ExitRefused || !strings.Contains(r.stderr, "held by a until") || !strings.Contains(r.stderr, "merging #57") {
		t.Fatalf("second lock: %+v", r)
	}
	if r := agora(ctx, socket, "b", "queue", "ls", "example-app/merge"); strings.Contains(r.stdout, " b ") {
		t.Fatalf("b was queued: %s", r.stdout)
	}
}

func TestUnlockOthersNeedsForce(t *testing.T) {
	socket := startHub(t)
	ctx := context.Background()
	agora(ctx, socket, "a", "lock", "db/shared")
	if r := agora(ctx, socket, "b", "unlock", "db/shared"); r.code != 1 || !strings.Contains(r.stderr, "held by a until") {
		t.Fatalf("unlock without force: %+v", r)
	}
	if r := agora(ctx, socket, "b", "unlock", "db/shared", "--force"); r.code != 0 || !strings.Contains(r.stdout, "removed a from db/shared") {
		t.Fatalf("forced unlock: %+v", r)
	}
	if r := agora(ctx, socket, "c", "locks"); strings.Contains(r.stdout, "db/shared") {
		t.Fatalf("lock still held: %s", r.stdout)
	}
}

func TestWaitEndsWhenTheTurnComes(t *testing.T) {
	socket := startHub(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	agora(ctx, socket, "a", "queue", "join", "heavy/typecheck")
	if r := agora(ctx, socket, "b", "queue", "join", "heavy/typecheck"); !strings.Contains(r.stdout, "position 1") {
		t.Fatalf("join: %+v", r)
	}
	waited := make(chan result)
	go func() { waited <- agora(ctx, socket, "b", "queue", "wait", "heavy/typecheck") }()
	time.Sleep(200 * time.Millisecond) // let the wait start streaming
	agora(ctx, socket, "a", "queue", "release", "heavy/typecheck")
	select {
	case r := <-waited:
		if r.code != 0 || !strings.Contains(r.stdout, "waiting for heavy/typecheck at position 1") || !strings.Contains(r.stdout, "holding heavy/typecheck") {
			t.Fatalf("wait: %+v", r)
		}
	case <-ctx.Done():
		t.Fatal("wait did not end")
	}
}

func TestWaitSaysTheNewNameAfterARename(t *testing.T) {
	socket := startHub(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	t.Setenv("AGORA_SESSION", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	agora(ctx, socket, "fixer", "join", "fixer")
	agora(ctx, socket, "a", "queue", "join", "heavy/typecheck")
	agora(ctx, socket, "fixer", "queue", "join", "heavy/typecheck")
	waited := make(chan result)
	go func() { waited <- agora(ctx, socket, "fixer", "queue", "wait", "heavy/typecheck") }()
	time.Sleep(200 * time.Millisecond) // let the wait start streaming
	if r := agora(ctx, socket, "fixer", "rename", "docs-writer"); r.code != 0 {
		t.Fatalf("rename: %+v", r)
	}
	time.Sleep(200 * time.Millisecond)
	agora(ctx, socket, "a", "queue", "release", "heavy/typecheck")
	select {
	case r := <-waited:
		if r.code != 0 || strings.Count(r.stdout, "now waiting as docs-writer") != 1 || !strings.Contains(r.stdout, "holding heavy/typecheck") ||
			strings.Index(r.stdout, "now waiting as") > strings.Index(r.stdout, "holding") {
			t.Fatalf("wait: %+v", r)
		}
	case <-ctx.Done():
		t.Fatal("wait did not end")
	}
}

func TestWaitEndsWithAnErrorWhenRemoved(t *testing.T) {
	socket := startHub(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	agora(ctx, socket, "a", "queue", "join", "r")
	agora(ctx, socket, "b", "queue", "join", "r")
	waited := make(chan result)
	go func() { waited <- agora(ctx, socket, "b", "queue", "wait", "r") }()
	time.Sleep(200 * time.Millisecond)
	agora(ctx, socket, "a", "queue", "release", "r", "--agent", "b", "--force")
	select {
	case r := <-waited:
		if r.code == 0 || !strings.Contains(r.stderr, "not in the queue") {
			t.Fatalf("wait: %+v", r)
		}
	case <-ctx.Done():
		t.Fatal("wait did not end")
	}
}

func TestWaitSurvivesAHubRestart(t *testing.T) {
	defer cli.SetHubGiveUp(5 * time.Second)()
	dir, err := os.MkdirTemp("", "agora-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "hub.sock")
	stop := runHub(t, dir, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	agora(ctx, socket, "a", "queue", "join", "r")
	agora(ctx, socket, "b", "queue", "join", "r")
	waited := make(chan result)
	go func() { waited <- agora(ctx, socket, "b", "queue", "wait", "r") }()
	time.Sleep(6 * time.Second) // a quiet wait, longer than the give-up time
	stop()
	time.Sleep(1500 * time.Millisecond) // the wait retries against a missing hub, well within the give-up time
	stop = runHub(t, dir, nil)
	t.Cleanup(stop)
	agora(ctx, socket, "a", "queue", "release", "r")
	select {
	case r := <-waited:
		if r.code != 0 || !strings.Contains(r.stdout, "holding r") {
			t.Fatalf("wait: %+v", r)
		}
	case <-ctx.Done():
		t.Fatal("wait did not end")
	}
}

func TestWaitGivesUpWithoutAHub(t *testing.T) {
	defer cli.SetHubGiveUp(500 * time.Millisecond)()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	socket := filepath.Join(t.TempDir(), "none.sock")
	r := agora(ctx, socket, "a", "queue", "wait", "r")
	if ctx.Err() != nil || r.code != 1 || !strings.Contains(r.stderr, "cannot reach the hub") {
		t.Fatalf("%+v", r)
	}
}

func TestSlotsAndListing(t *testing.T) {
	socket := startHub(t)
	ctx := context.Background()
	if r := agora(ctx, socket, "a", "queue", "slots", "heavy/typecheck", "2"); !strings.Contains(r.stdout, "heavy/typecheck has 2 slots") {
		t.Fatalf("slots: %+v", r)
	}
	for _, a := range []string{"a", "b", "c"} {
		agora(ctx, socket, a, "queue", "join", "heavy/typecheck", "check by "+a)
	}
	r := agora(ctx, socket, "a", "queue", "ls")
	for _, want := range []string{"heavy/typecheck (2 slots)", "held     a", "held     b", "#1       c"} {
		if !strings.Contains(r.stdout, want) {
			t.Fatalf("listing lacks %q:\n%s", want, r.stdout)
		}
	}
}

func TestNoHubIsReported(t *testing.T) {
	r := agora(context.Background(), filepath.Join(t.TempDir(), "none.sock"), "a", "locks")
	if r.code != 1 || !strings.Contains(r.stderr, "cannot reach the hub") {
		t.Fatalf("%+v", r)
	}
}

func TestIdentityIsRequired(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("AGORA_SESSION", "")
	socket := startHub(t)
	r := agora(context.Background(), socket, "", "lock", "r")
	if r.code != 1 || !strings.Contains(r.stderr, "--as") {
		t.Fatalf("%+v", r)
	}
}

func TestLockWhileQueuedIsRefused(t *testing.T) {
	socket := startHub(t)
	ctx := context.Background()
	agora(ctx, socket, "a", "lock", "r")
	agora(ctx, socket, "b", "queue", "join", "r")
	if r := agora(ctx, socket, "b", "lock", "r"); r.code != cli.ExitRefused || !strings.Contains(r.stderr, "held by a until") || !strings.Contains(r.stderr, "1 waiting") {
		t.Fatalf("%+v", r)
	}
}

func TestRefusalNamesAnAgentWhoseTurnItIs(t *testing.T) {
	socket := startHub(t)
	ctx := context.Background()
	agora(ctx, socket, "a", "lock", "r")
	agora(ctx, socket, "b", "queue", "join", "r")
	agora(ctx, socket, "a", "unlock", "r")
	if r := agora(ctx, socket, "c", "lock", "r"); r.code != cli.ExitRefused || !strings.Contains(r.stderr, "b (its turn, claim by") {
		t.Fatalf("%+v", r)
	}
}

func TestUnlockForceWithNothingHeld(t *testing.T) {
	socket := startHub(t)
	if r := agora(context.Background(), socket, "a", "unlock", "r", "--force"); r.code != 0 || !strings.Contains(r.stdout, "r is not locked") {
		t.Fatalf("%+v", r)
	}
}

func TestBadValuesAreRefused(t *testing.T) {
	socket := startHub(t)
	ctx := context.Background()
	for _, args := range [][]string{
		{"lock", "r", "--ttl", "-1m"},
		{"lock", "r", "--ttl", "0s"},
		{"queue", "join", "r", "--lease", "0s"},
		{"queue", "join", "r", "--lease", "1ms"},
		{"lock", "r", "--ttl", "1ms"},
		{"queue", "slots", "r", "4294967297"},
		{"queue", "slots", "r", "0"},
	} {
		if r := agora(ctx, socket, "a", args...); r.code != 1 {
			t.Errorf("%v: %+v", args, r)
		}
	}
}

func TestJoinedSessionActsWithoutAName(t *testing.T) {
	socket := startHub(t)
	ctx := context.Background()
	t.Setenv("AGORA_NAME", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "") // the test may itself run inside Claude Code
	t.Setenv("AGORA_SESSION", "session-123")
	if r := agora(ctx, socket, "", "join", "builder"); r.code != 0 || !strings.Contains(r.stdout, "joined as builder; commands from this session") {
		t.Fatalf("join: %+v", r)
	}
	if r := agora(ctx, socket, "", "lock", "db/shared"); r.code != 0 {
		t.Fatalf("lock: %+v", r)
	}
	if r := agora(ctx, socket, "", "locks"); !strings.Contains(r.stdout, "builder") {
		t.Fatalf("locks: %+v", r)
	}
	if r := agora(ctx, socket, "", "whoami"); !strings.Contains(r.stdout, "name: builder") || !strings.Contains(r.stdout, "session: session-123") {
		t.Fatalf("whoami: %+v", r)
	}
	if r := agora(ctx, socket, "", "sessions"); !strings.Contains(r.stdout, "builder") || !strings.Contains(r.stdout, "session-123") {
		t.Fatalf("sessions: %+v", r)
	}
}

func TestTakenNameIsRefused(t *testing.T) {
	socket := startHub(t)
	ctx := context.Background()
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("AGORA_SESSION", "session-aaa")
	agora(ctx, socket, "", "join", "builder")
	t.Setenv("AGORA_SESSION", "session-bbb")
	if r := agora(ctx, socket, "", "join", "builder"); r.code != 1 || !strings.Contains(r.stderr, "live session") {
		t.Fatalf("%+v", r)
	}
	if r := agora(ctx, socket, "", "join", "builder", "--force"); r.code != 0 {
		t.Fatalf("forced: %+v", r)
	}
}

func TestHookIsSilentWithoutAHub(t *testing.T) {
	var out, errOut bytes.Buffer
	root := []string{"--socket", filepath.Join(t.TempDir(), "none.sock"), "hook", "claude-code"}
	t.Setenv("AGORA_DEBUG", "")
	code := cli.RunWithInput(context.Background(), root, strings.NewReader(`{"session_id":"session-1","hook_event_name":"Stop"}`), &out, &errOut)
	if code != 0 || out.Len() != 0 || errOut.Len() != 0 {
		t.Fatalf("code %d out %q err %q", code, out.String(), errOut.String())
	}
}

func TestHookReportsTheSession(t *testing.T) {
	socket := startHub(t)
	ctx := context.Background()
	var out, errOut bytes.Buffer
	in := strings.NewReader(`{"session_id":"session-hook","hook_event_name":"SessionStart","cwd":"/src/example-app"}`)
	if code := cli.RunWithInput(ctx, []string{"--socket", socket, "hook", "claude-code"}, in, &out, &errOut); code != 0 {
		t.Fatalf("code %d err %q", code, errOut.String())
	}
	if r := agora(ctx, socket, "", "sessions"); !strings.Contains(r.stdout, "session-hook") || !strings.Contains(r.stdout, "/src/example-app") {
		t.Fatalf("sessions: %+v", r)
	}
	var hookOut struct {
		HookSpecificOutput struct {
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out.Bytes(), &hookOut); err != nil || !strings.Contains(hookOut.HookSpecificOutput.AdditionalContext, "agora join <name>") {
		t.Fatalf("no invitation: %q", out.String())
	}
}

func TestProfilesStatusWhoAndLeave(t *testing.T) {
	socket := startHub(t)
	ctx := context.Background()
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("AGORA_SESSION", "session-prof")
	t.Setenv("AGORA_NAME", "")
	dir := t.TempDir()
	if r := agora(ctx, socket, "", "join", "builder", "--project", "example-app", "--task", "fix login", "--cwd", dir, "--pr", "#57"); r.code != 0 {
		t.Fatalf("join: %+v", r)
	}
	if r := agora(ctx, socket, "", "set", "--task", "write tests", "--drop-pr", "57", "--pr", "58"); r.code != 0 || !strings.Contains(r.stdout, "write tests") || !strings.Contains(r.stdout, "#58") {
		t.Fatalf("set: %+v", r)
	}
	agora(ctx, socket, "", "lock", "example-app/merge")
	r := agora(ctx, socket, "", "status")
	for _, want := range []string{"AGENTS (1 active)", "builder", "working", "example-app", "#58", "write tests", "RESOURCES", "example-app/merge"} {
		if !strings.Contains(r.stdout, want) {
			t.Fatalf("status lacks %q:\n%s", want, r.stdout)
		}
	}
	if r := agora(ctx, socket, "x", "who", "#58"); !strings.Contains(r.stdout, "builder") {
		t.Fatalf("who pr: %+v", r)
	}
	if r := agora(ctx, socket, "x", "who", dir); !strings.Contains(r.stdout, "builder") {
		t.Fatalf("who dir: %+v", r)
	}
	if r := agora(ctx, socket, "x", "who", "#99"); !strings.Contains(r.stdout, "nobody active matches") {
		t.Fatalf("who none: %+v", r)
	}
	if r := agora(ctx, socket, "", "set"); r.code != 1 || !strings.Contains(r.stderr, "nothing to change") {
		t.Fatalf("set without flags: %+v", r)
	}
	if r := agora(ctx, socket, "builder", "set"); r.code != 1 || !strings.Contains(r.stderr, "nothing to change") {
		t.Fatalf("set with only --as: %+v", r)
	}
	if r := agora(ctx, socket, "", "leave"); r.code != 0 || !strings.Contains(r.stdout, "released example-app/merge") ||
		!strings.Contains(r.stdout, "commands from this session no longer act as builder") {
		t.Fatalf("leave: %+v", r)
	}
	if r := agora(ctx, socket, "", "whoami"); strings.Contains(r.stdout, "name: builder") {
		t.Fatalf("the session still acts as builder after leaving: %+v", r)
	}
	if r := agora(ctx, socket, "", "status"); !strings.Contains(r.stdout, "AGENTS (0 active)") {
		t.Fatalf("status after leave: %s", r.stdout)
	}
	if r := agora(ctx, socket, "", "join", "builder"); r.code != 0 {
		t.Fatalf("rejoin: %+v", r)
	}
	if r := agora(ctx, socket, "", "status"); !strings.Contains(r.stdout, "AGENTS (1 active)") {
		t.Fatalf("status after rejoin: %s", r.stdout)
	}
}

func TestRenameAndSigil(t *testing.T) {
	socket := startHub(t)
	ctx := context.Background()
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("AGORA_SESSION", "session-rename")
	t.Setenv("AGORA_NAME", "")
	if r := agora(ctx, socket, "", "join", "fixer", "--project", "example-app"); r.code != 0 {
		t.Fatalf("join: %+v", r)
	}
	if r := agora(ctx, socket, "", "set", "--icon", "lyre", "--pigment", "ochre"); r.code != 0 || !strings.Contains(r.stdout, "lyre") || !strings.Contains(r.stdout, "ochre") {
		t.Fatalf("set sigil: %+v", r)
	}
	if r := agora(ctx, socket, "", "set", "--icon", "owl"); r.code != 1 || !strings.Contains(r.stderr, "one of") {
		t.Fatalf("set the board's sigil: %+v", r)
	}
	if r := agora(ctx, socket, "", "rename", "docs-writer"); r.code != 0 || !strings.Contains(r.stdout, "fixer is now called docs-writer") ||
		!strings.Contains(r.stdout, "commands from this session now act as docs-writer") {
		t.Fatalf("rename: %+v", r)
	}
	if r := agora(ctx, socket, "", "whoami"); !strings.Contains(r.stdout, "name: docs-writer") {
		t.Fatalf("whoami after rename: %+v", r)
	}
	if r := agora(ctx, socket, "", "status"); !strings.Contains(r.stdout, "docs-writer (was fixer)") {
		t.Fatalf("status lacks the former name:\n%s", r.stdout)
	}
	if r := agora(ctx, socket, "x", "who", "fixer"); !strings.Contains(r.stdout, "docs-writer (was fixer)") {
		t.Fatalf("who by the former name: %+v", r)
	}
	if r := agora(ctx, socket, "docs-writer", "rename", "reviewer"); r.code != 0 || !strings.Contains(r.stdout, "pass --as reviewer") {
		t.Fatalf("rename by name: %+v", r)
	}
	if r := agora(ctx, socket, "fixer", "post", "general", "hello"); r.code != 1 || !strings.Contains(r.stderr, `now called "reviewer"`) {
		t.Fatalf("posting as a former name: %+v", r)
	}
}

func TestLeavingByNameSaysNothingAboutTheSession(t *testing.T) {
	socket := startHub(t)
	ctx := context.Background()
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("AGORA_SESSION", "")
	agora(ctx, socket, "builder", "join", "builder")
	if r := agora(ctx, socket, "builder", "leave"); r.code != 0 || !strings.Contains(r.stdout, "builder marked as left") || strings.Contains(r.stdout, "no longer act") {
		t.Fatalf("leave: %+v", r)
	}
}

func TestRoomsAndMessages(t *testing.T) {
	socket := startHub(t)
	ctx := context.Background()
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("AGORA_SESSION", "")
	agora(ctx, socket, "builder", "join", "builder")
	agora(ctx, socket, "reviewer", "join", "reviewer")
	if r := agora(ctx, socket, "builder", "room-create", "#example-app", "work", "on", "example-app"); r.code != 0 || !strings.Contains(r.stdout, "created #example-app") {
		t.Fatalf("create: %+v", r)
	}
	if r := agora(ctx, socket, "reviewer", "subscribe", "example-app"); !strings.Contains(r.stdout, "#general #example-app") {
		t.Fatalf("subscribe: %+v", r)
	}
	if r := agora(ctx, socket, "builder", "subscribe", "example-app", "--mode", "wake"); r.code != 0 || !strings.Contains(r.stdout, "#general #example-app (wake)") {
		t.Fatalf("subscribe with a mode: %+v", r)
	}
	if r := agora(ctx, socket, "builder", "subscribe", "general", "--mode", "mentions"); !strings.Contains(r.stdout, "#general (mentions) #example-app (wake)") {
		t.Fatalf("mode of general: %+v", r)
	}
	if r := agora(ctx, socket, "builder", "subscribe", "example-app", "--mode", "loud"); r.code == 0 {
		t.Fatalf("unknown mode accepted: %+v", r)
	}
	agora(ctx, socket, "builder", "subscribe", "general", "--mode", "all")
	r := agora(ctx, socket, "builder", "post", "example-app", "@reviewer", "PR", "#57", "is", "ready")
	if r.code != 0 || strings.TrimSpace(r.stdout) == "" {
		t.Fatalf("post: %+v", r)
	}
	id := strings.TrimSpace(r.stdout)
	if r := agora(ctx, socket, "reviewer", "unread", "--peek"); !strings.Contains(r.stdout, "PR #57 is ready") || !strings.Contains(r.stdout, "to you") {
		t.Fatalf("peek: %+v", r)
	}
	if r := agora(ctx, socket, "reviewer", "unread"); !strings.Contains(r.stdout, "PR #57 is ready") {
		t.Fatalf("unread: %+v", r)
	}
	if r := agora(ctx, socket, "reviewer", "unread"); !strings.Contains(r.stdout, "no unread messages") {
		t.Fatalf("unread again: %+v", r)
	}
	if r := agora(ctx, socket, "reviewer", "post", "example-app", "on it", "--reply", id); r.code != 0 {
		t.Fatalf("reply: %+v", r)
	}
	if r := agora(ctx, socket, "x", "read", "example-app"); !strings.Contains(r.stdout, "re "+id) {
		t.Fatalf("read: %+v", r)
	}
	if r := agora(ctx, socket, "builder", "status"); !strings.Contains(r.stdout, "ROOMS") || !strings.Contains(r.stdout, "#example-app") {
		t.Fatalf("status: %s", r.stdout)
	}
	var out, errOut bytes.Buffer
	code := cli.RunWithInput(ctx, []string{"--socket", socket, "--as", "builder", "post", "general", "-"}, strings.NewReader("from stdin\n"), &out, &errOut)
	if code != 0 {
		t.Fatalf("stdin post: %d %s", code, errOut.String())
	}
	if r := agora(ctx, socket, "x", "read", "general"); !strings.Contains(r.stdout, "from stdin") {
		t.Fatalf("read general: %+v", r)
	}
}

func TestWaitHookWakesWithExitCodeTwo(t *testing.T) {
	socket := startHub(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("AGORA_SESSION", "session-wait")
	hook := func(input string) (int, string) {
		var out, errOut bytes.Buffer
		code := cli.RunWithInput(ctx, []string{"--socket", socket, "hook", "claude-code"}, strings.NewReader(input), &out, &errOut)
		return code, out.String()
	}
	hook(`{"session_id":"session-wait","hook_event_name":"SessionStart"}`)
	agora(ctx, socket, "", "join", "builder")
	hook(`{"session_id":"session-wait","hook_event_name":"Stop"}`)
	type result struct {
		code   int
		stderr string
	}
	done := make(chan result, 1)
	go func() {
		var out, errOut bytes.Buffer
		code := cli.RunWithInput(ctx, []string{"--socket", socket, "hook", "claude-code-wait"},
			strings.NewReader(`{"session_id":"session-wait","hook_event_name":"Stop"}`), &out, &errOut)
		done <- result{code, errOut.String()}
	}()
	time.Sleep(300 * time.Millisecond)
	t.Setenv("AGORA_SESSION", "") // the reviewer is not in builder's session
	agora(ctx, socket, "reviewer", "join", "reviewer")
	agora(ctx, socket, "reviewer", "post", "general", "@builder", "please", "review")
	select {
	case r := <-done:
		if r.code != 2 || !strings.Contains(r.stderr, "please review") {
			t.Fatalf("%+v", r)
		}
	case <-ctx.Done():
		t.Fatal("hook did not wake")
	}
}

func TestWaitHookIsQuietWithoutAHub(t *testing.T) {
	var out, errOut bytes.Buffer
	t.Setenv("AGORA_DEBUG", "")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond) // it would retry for minutes
	defer cancel()
	code := cli.RunWithInput(ctx, []string{"--socket", filepath.Join(t.TempDir(), "none.sock"), "hook", "claude-code-wait"},
		strings.NewReader(`{"session_id":"session-x","hook_event_name":"Stop"}`), &out, &errOut)
	if code != 0 || out.Len() != 0 || errOut.Len() != 0 {
		t.Fatalf("code %d out %q err %q", code, out.String(), errOut.String())
	}
}

func TestProposalsAndCharter(t *testing.T) {
	socket := startHub(t)
	ctx := context.Background()
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("AGORA_SESSION", "")
	for _, n := range []string{"builder", "reviewer"} {
		agora(ctx, socket, n, "join", n)
	}
	if r := agora(ctx, socket, "builder", "propose", "Merge under the lock", "Take", "the", "merge", "lock", "first."); r.code != 0 || !strings.Contains(r.stdout, "proposal #1") {
		t.Fatalf("propose: %+v", r)
	}
	if r := agora(ctx, socket, "reviewer", "vote", "#1", "YES", "makes", "sense"); r.code != 0 || !strings.Contains(r.stdout, "reviewer voted yes on #1") {
		t.Fatalf("vote: %+v", r)
	}
	if r := agora(ctx, socket, "builder", "status"); !strings.Contains(r.stdout, "OPEN PROPOSALS") || !strings.Contains(r.stdout, "+1/-0") {
		t.Fatalf("status: %s", r.stdout)
	}
	if r := agora(ctx, socket, "x", "proposals", "--show", "1"); !strings.Contains(r.stdout, "Take the merge lock first.") || !strings.Contains(r.stdout, "makes sense") {
		t.Fatalf("show: %+v", r)
	}
	var out, errOut bytes.Buffer
	set := func() int {
		out.Reset()
		errOut.Reset()
		return cli.RunWithInput(ctx, []string{"--socket", socket, "--as", "builder", "charter", "set", "--proposal", "1"},
			strings.NewReader("# Agora charter\n\n1. Merge under the lock.\n"), &out, &errOut)
	}
	if code := set(); code != 1 || !strings.Contains(errOut.String(), "only after an accepted proposal") {
		t.Fatalf("set before accepting: %d %s", code, errOut.String())
	}
	if r := agora(ctx, socket, "builder", "close", "1", "accepted"); r.code != 0 {
		t.Fatalf("close: %+v", r)
	}
	if r := agora(ctx, socket, "builder", "close", "1", "rejected"); r.code != 1 {
		t.Fatalf("close twice: %+v", r)
	}
	if code := set(); code != 0 {
		t.Fatalf("set: %d %s", code, errOut.String())
	}
	if r := agora(ctx, socket, "x", "charter"); !strings.Contains(r.stdout, "Merge under the lock.") || !strings.Contains(r.stdout, "after proposal #1") {
		t.Fatalf("charter: %+v", r)
	}
	if r := agora(ctx, socket, "x", "read", "general"); !strings.Contains(r.stdout, "Charter updated by builder") {
		t.Fatalf("announcement: %s", r.stdout)
	}
}

// prForge reports pull request 57 open from feat/export.
type prForge struct{}

func (prForge) Lookup(context.Context, forge.Repo, forge.Query) (forge.Result, error) {
	return forge.Result{DefaultBranch: "main", PRs: []forge.PR{{Number: 57, Branch: "feat/export", Head: "a1", Open: true}}}, nil
}

func TestFoundPullRequestsShowInStatusAndWho(t *testing.T) {
	socket := startHubWith(t, func(h *hub.Hub) {
		h.Forges = forge.Forges{"github.com": prForge{}}
		h.WatchFirst, h.WatchEvery = 10*time.Millisecond, 10*time.Millisecond
	})
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte("ref: refs/heads/feat/export\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "config"), []byte("[remote \"origin\"]\n\turl = https://github.com/example-org/example-app.git\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if r := agora(ctx, socket, "builder", "join", "builder", "--cwd", dir, "--pr", "61"); r.code != 0 {
		t.Fatalf("join: %+v", r)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		r := agora(ctx, socket, "builder", "who", "#57")
		if strings.Contains(r.stdout, "builder") {
			if !strings.Contains(r.stdout, "PR #57,#61") {
				t.Fatalf("who: %s", r.stdout)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("pull request never found: %+v", r)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if r := agora(ctx, socket, "builder", "status"); !strings.Contains(r.stdout, "#57,#61") {
		t.Fatalf("status: %s", r.stdout)
	}
}

// lockedBuffer is a buffer the hub's log and the test can use at once.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
func (l *lockedBuffer) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

// hubLog starts `agora hub` with args, returns its log once it listens (or its exit), and stops it.
func hubLog(t *testing.T, args ...string) (string, int) {
	t.Helper()
	dir, err := os.MkdirTemp("", "agora-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out lockedBuffer
	code := make(chan int, 1)
	all := append([]string{"--socket", filepath.Join(dir, "hub.sock"), "hub", "--db", filepath.Join(dir, "agora.db")}, args...)
	go func() { code <- cli.Run(ctx, all, &out, &out) }()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(out.String(), "hub listening") {
		select {
		case c := <-code:
			return out.String(), c
		case <-time.After(10 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatalf("hub did not start: %s", out.String())
		}
	}
	cancel()
	return out.String(), <-code
}

func TestWatchPullRequestsSetting(t *testing.T) {
	withGH := t.TempDir()
	if err := os.WriteFile(filepath.Join(withGH, "gh"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, path, env string
		args            []string
		want            string // in the log
		code            int
	}{
		{"on by default", withGH, "", nil, "watch_prs=[github.com]", 0},
		{"off by flag", withGH, "", []string{"--watch-prs=false"}, "watch_prs=[]", 0},
		{"off by environment", withGH, "false", nil, "watch_prs=[]", 0},
		{"flag over environment", withGH, "false", []string{"--watch-prs"}, "watch_prs=[github.com]", 0},
		{"invalid environment", withGH, "off", nil, `AGORA_WATCH_PRS="off": use true or false`, 1},
		{"no gh", t.TempDir(), "", nil, "gh is not installed", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("PATH", c.path)
			t.Setenv("AGORA_WATCH_PRS", c.env)
			log, code := hubLog(t, c.args...)
			if code != c.code || !strings.Contains(log, c.want) {
				t.Fatalf("exit %d, log: %s", code, log)
			}
		})
	}
}

func TestWebSetting(t *testing.T) {
	cases := []struct {
		name, env string
		args      []string
		want      []string // in the log
		code      int
	}{
		{"off by default", "", nil, []string{`web=""`}, 0},
		{"only a port", "", []string{"--web", "0"}, []string{"web=127.0.0.1:", "web_as=operator"}, 0},
		{"from the environment", "0", nil, []string{"web=127.0.0.1:"}, 0},
		{"named operator", "", []string{"--web", "127.0.0.1:0", "--web-as", "owner"}, []string{"web=127.0.0.1:", "web_as=owner"}, 0},
		{"another interface", "", []string{"--web", "0.0.0.0:0"}, []string{"not a loopback address"}, 0},
		{"invalid operator", "", []string{"--web", "0", "--web-as", "agora"}, []string{`"agora" is reserved`}, 1},
		{"invalid address", "", []string{"--web", "example:port"}, []string{"--web"}, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("AGORA_WEB", c.env)
			t.Setenv("AGORA_WEB_AS", "")
			log, code := hubLog(t, c.args...)
			if code != c.code {
				t.Fatalf("exit %d, log: %s", code, log)
			}
			for _, w := range c.want {
				if !strings.Contains(log, w) {
					t.Fatalf("log lacks %q: %s", w, log)
				}
			}
		})
	}
}

func TestWebToken(t *testing.T) {
	ctx := context.Background()
	plain := startHub(t)
	r := agora(ctx, plain, "", "web", "token")
	if r.code != 0 || !strings.Contains(r.stdout, "serves no web app") {
		t.Fatalf("hub without web: %+v", r)
	}

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	socket := startHubWith(t, func(h *hub.Hub) {
		if err := h.EnableWeb(ctx, l, "operator"); err != nil {
			t.Fatal(err)
		}
	})
	r = agora(ctx, socket, "", "web", "token")
	token := strings.TrimSpace(strings.SplitN(r.stdout, "\n", 2)[0])
	if r.code != 0 || len(token) != 43 || !strings.Contains(r.stdout, "http://"+l.Addr().String()+"/#token="+token) {
		t.Fatalf("web token: %+v", r)
	}
	if again := agora(ctx, socket, "", "web", "token"); !strings.HasPrefix(again.stdout, token+"\n") {
		t.Fatalf("token changed: %+v", again)
	}
	rotated := agora(ctx, socket, "", "web", "token", "--rotate")
	if rotated.code != 0 || strings.Contains(rotated.stdout, token) || !strings.Contains(rotated.stdout, "#token=") {
		t.Fatalf("rotate: %+v", rotated)
	}
}

func TestBridges(t *testing.T) {
	socket := startHub(t)
	ctx := context.Background()
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("AGORA_SESSION", "")
	agora(ctx, socket, "builder", "join", "builder")
	agora(ctx, socket, "secretary", "join", "secretary")
	r := agora(ctx, socket, "builder", "bridge", "add", "example-chat", "--command", "exec sleep 600", "--purpose", "the example chat", "--agent", "secretary")
	if r.code != 0 || !strings.Contains(r.stdout, "created #example-chat") || !strings.Contains(r.stdout, "subscribed secretary to #example-chat with the mode wake") {
		t.Fatalf("add: %+v", r)
	}
	if r := agora(ctx, socket, "builder", "bridge", "add", "example-chat", "--command", "exec sleep 600"); r.code == 0 || !strings.Contains(r.stderr, "already has a bridge") {
		t.Fatalf("add again: %+v", r)
	}
	if r := agora(ctx, socket, "builder", "bridge", "list"); !strings.Contains(r.stdout, "#example-chat") || !strings.Contains(r.stdout, "running") ||
		!strings.Contains(r.stdout, "policy approve") || !strings.Contains(r.stdout, "agents secretary") || !strings.Contains(r.stdout, "exec sleep 600") {
		t.Fatalf("list: %+v", r)
	}
	if r := agora(ctx, socket, "secretary", "subscribe", "example-chat"); !strings.Contains(r.stdout, "#example-chat (wake)") {
		t.Fatalf("secretary's subscriptions: %+v", r)
	}
	if r := agora(ctx, socket, "secretary", "post", "example-chat", "looked, all fine"); r.code != 0 {
		t.Fatalf("post: %+v", r)
	}
	if r := agora(ctx, socket, "x", "read", "example-chat"); !strings.Contains(r.stdout, "secretary · ") || !strings.Contains(r.stdout, "· pending") {
		t.Fatalf("read: %+v", r)
	}
	if r := agora(ctx, socket, "builder", "bridge", "remove", "example-chat"); r.code != 0 || !strings.Contains(r.stdout, "the room stays") {
		t.Fatalf("remove: %+v", r)
	}
	if r := agora(ctx, socket, "builder", "bridge", "list"); !strings.Contains(r.stdout, "no bridges") {
		t.Fatalf("list after removing: %+v", r)
	}
	if r := agora(ctx, socket, "x", "read", "example-chat"); !strings.Contains(r.stdout, "looked, all fine") || !strings.Contains(r.stdout, "no longer bridged") {
		t.Fatalf("read after removing: %+v", r)
	}
}
