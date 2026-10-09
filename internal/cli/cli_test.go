package cli_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vsem-azamat/agora/internal/cli"
	"github.com/vsem-azamat/agora/internal/hub"
	"github.com/vsem-azamat/agora/internal/store"
)

// startHub runs a hub on a temporary socket and returns the socket path.
func startHub(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "agora-test-") // short path: unix sockets have a length limit
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	ctx, cancel := context.WithCancel(context.Background())
	db, err := store.Open(ctx, filepath.Join(dir, "agora.db"))
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "hub.sock")
	l, err := hub.Listen(socket)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		hub.Open(db, nil, nil).Serve(ctx, l)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
		db.Close()
	})
	return socket
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

func TestSlotsAndListing(t *testing.T) {
	socket := startHub(t)
	ctx := context.Background()
	if r := agora(ctx, socket, "a", "queue", "slots", "heavy/typecheck", "2"); !strings.Contains(r.stdout, "heavy/typecheck has 2 slots") {
		t.Fatalf("slots: %+v", r)
	}
	for _, a := range []string{"a", "b", "c"} {
		agora(ctx, socket, a, "queue", "join", "heavy/typecheck", fmt.Sprintf("check by %s", a))
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
	if r := agora(ctx, socket, "", "leave"); r.code != 0 || !strings.Contains(r.stdout, "released example-app/merge") {
		t.Fatalf("leave: %+v", r)
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
	if r := agora(ctx, socket, "reviewer", "vote", "#1", "yes", "makes", "sense"); r.code != 0 {
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
