package hub_test

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/durationpb"

	agorav1 "github.com/vsem-azamat/agora/gen/agora/v1"
	"github.com/vsem-azamat/agora/gen/agora/v1/agorav1connect"
	"github.com/vsem-azamat/agora/internal/forge"
	"github.com/vsem-azamat/agora/internal/hub"
	"github.com/vsem-azamat/agora/internal/proc"
	"github.com/vsem-azamat/agora/internal/store"
)

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

func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "agora-hub-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

type running struct {
	client   agorav1connect.ResourceServiceClient
	sessions agorav1connect.SessionServiceClient
	rooms    agorav1connect.RoomServiceClient
	agents   agorav1connect.AgentServiceClient
	clock    *clock
	stop     context.CancelFunc
	done     chan struct{} // closed when Serve returns
	err      error         // what Serve returned; read after done
}

func start(t *testing.T) *running { return startWith(t, nil) }

func startWith(t *testing.T, configure func(*hub.Hub)) *running {
	t.Helper()
	dir := shortDir(t)
	ctx, cancel := context.WithCancel(context.Background())
	db, err := store.Open(ctx, filepath.Join(dir, "agora.db"))
	if err != nil {
		t.Fatal(err)
	}
	c := &clock{t: time.Now()}
	socket := filepath.Join(dir, "hub.sock")
	l, err := hub.Listen(socket)
	if err != nil {
		t.Fatal(err)
	}
	r := &running{clock: c, stop: cancel, done: make(chan struct{})}
	h := hub.Open(db, c.now, nil)
	if configure != nil {
		configure(h)
	}
	go func() {
		r.err = h.Serve(ctx, l)
		close(r.done)
	}()
	t.Cleanup(func() {
		cancel()
		<-r.done
		db.Close()
	})
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", socket)
	}}
	r.client = agorav1connect.NewResourceServiceClient(&http.Client{Transport: transport}, "http://agora")
	r.sessions = agorav1connect.NewSessionServiceClient(&http.Client{Transport: transport}, "http://agora")
	r.rooms = agorav1connect.NewRoomServiceClient(&http.Client{Transport: transport}, "http://agora")
	r.agents = agorav1connect.NewAgentServiceClient(&http.Client{Transport: transport}, "http://agora")
	return r
}

func join(t *testing.T, r *running, key, agent string, lease time.Duration) {
	t.Helper()
	if _, err := r.client.Join(context.Background(), connect.NewRequest(&agorav1.JoinRequest{Key: key, Agent: agent, Lease: durationpb.New(lease)})); err != nil {
		t.Fatal(err)
	}
}

// waitFor starts a wait stream and returns a channel that receives its error when it ends.
func waitFor(t *testing.T, r *running, key, agent string) <-chan error {
	t.Helper()
	ended := make(chan error, 1)
	stream, err := r.client.Wait(context.Background(), connect.NewRequest(&agorav1.WaitRequest{Key: key, Agent: agent}))
	if err != nil {
		t.Fatal(err)
	}
	if !stream.Receive() { // the first message: the current position
		t.Fatalf("no first message: %v", stream.Err())
	}
	go func() {
		defer stream.Close()
		for stream.Receive() {
		}
		ended <- stream.Err()
	}()
	return ended
}

func TestWaiterWakesWhenAListingOffersItTheSlot(t *testing.T) {
	r := start(t)
	join(t, r, "r", "a", time.Minute)
	join(t, r, "r", "b", time.Minute)
	ended := waitFor(t, r, "r", "b")
	r.clock.add(time.Minute) // a's lease ends; nothing has settled it yet
	if _, err := r.client.List(context.Background(), connect.NewRequest(&agorav1.ListRequest{})); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-ended:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(500 * time.Millisecond): // well before the one-second safety net
		t.Fatal("waiter was not woken by the listing")
	}
}

func TestShutdownEndsWaitsAndReturns(t *testing.T) {
	r := start(t)
	join(t, r, "r", "a", time.Minute)
	join(t, r, "r", "b", time.Minute)
	ended := waitFor(t, r, "r", "b")
	r.stop()
	select {
	case <-r.done:
		if r.err != nil {
			t.Fatal(r.err)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("hub did not stop")
	}
	select {
	case <-ended:
	case <-time.After(time.Second):
		t.Fatal("wait stream did not end")
	}
}

func TestListenNeverRemovesAFileThatIsNotASocket(t *testing.T) {
	path := filepath.Join(shortDir(t), "notes.txt")
	os.WriteFile(path, []byte("keep me"), 0o600)
	if _, err := hub.Listen(path); err == nil {
		t.Fatal("listened on a regular file")
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != "keep me" {
		t.Fatalf("file changed: %q %v", b, err)
	}
}

func TestSecondHubOnTheSameSocketIsRefused(t *testing.T) {
	path := filepath.Join(shortDir(t), "hub.sock")
	l, err := hub.Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if _, err := hub.Listen(path); err == nil {
		t.Fatal("second hub was allowed")
	}
}

func TestStaleSocketIsReplaced(t *testing.T) {
	path := filepath.Join(shortDir(t), "hub.sock")
	// a hub that crashed leaves its socket file behind, with nobody listening and no lock held
	old, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	old.SetUnlinkOnClose(false)
	old.Close()
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("stale socket missing: %v", err)
	}
	l, err := hub.Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	l.Close()
}

func TestLongSocketPathIsExplained(t *testing.T) {
	long := filepath.Join(shortDir(t), strings.Repeat("x", hub.MaxSocketPath))
	if _, err := hub.Listen(long); err == nil || !strings.Contains(err.Error(), "unix sockets allow at most") {
		t.Fatalf("err = %v", err)
	}
}

func (r *running) idleAgent(t *testing.T, name, session, terminal string) {
	t.Helper()
	r.idleAgentWithPID(t, name, session, terminal, int32(os.Getpid()))
}

func (r *running) idleAgentWithPID(t *testing.T, name, session, terminal string, pid int32) {
	t.Helper()
	bg := context.Background()
	if _, err := r.sessions.Report(bg, connect.NewRequest(&agorav1.ReportRequest{SessionId: session, Kind: "claude-code", Event: agorav1.SessionEvent_SESSION_EVENT_START,
		Terminal: terminal, Pid: pid, PidStart: proc.StartTime(int(pid))})); err != nil {
		t.Fatal(err)
	}
	if _, err := r.sessions.JoinName(bg, connect.NewRequest(&agorav1.JoinNameRequest{Name: name, SessionId: session})); err != nil {
		t.Fatal(err)
	}
	if _, err := r.sessions.Report(bg, connect.NewRequest(&agorav1.ReportRequest{SessionId: session, Event: agorav1.SessionEvent_SESSION_EVENT_STOP})); err != nil {
		t.Fatal(err)
	}
}

func (r *running) post(t *testing.T, author, body string) {
	t.Helper()
	bg := context.Background()
	r.sessions.JoinName(bg, connect.NewRequest(&agorav1.JoinNameRequest{Name: author}))
	if _, err := r.rooms.Post(bg, connect.NewRequest(&agorav1.PostRequest{Author: author, Room: "general", Body: body})); err != nil {
		t.Fatal(err)
	}
}

// wake starts a WaitWake stream and returns a channel with its wake text ("" for a quiet end).
func (r *running) wake(t *testing.T, session string) <-chan string {
	t.Helper()
	out := make(chan string, 1)
	stream, err := r.sessions.WaitWake(context.Background(), connect.NewRequest(&agorav1.WaitWakeRequest{SessionId: session}))
	if err != nil {
		t.Fatal(err)
	}
	if !stream.Receive() || !stream.Msg().GetArmed() {
		t.Fatalf("not armed: %v", stream.Err())
	}
	go func() {
		defer stream.Close()
		text := ""
		if stream.Receive() {
			text = stream.Msg().GetText()
		}
		out <- text
	}()
	return out
}

func within(t *testing.T, ch <-chan string, d time.Duration) (string, bool) {
	t.Helper()
	select {
	case s := <-ch:
		return s, true
	case <-time.After(d):
		return "", false
	}
}

func TestMentionWakesAWaitingSession(t *testing.T) {
	r := start(t)
	r.idleAgent(t, "builder", "session-1", "")
	woke := r.wake(t, "session-1")
	if _, ended := within(t, woke, 300*time.Millisecond); ended {
		t.Fatal("wait ended with nothing to wake for")
	}
	r.post(t, "reviewer", "@builder can you take #57?")
	text, ended := within(t, woke, 2*time.Second)
	if !ended || !strings.Contains(text, "can you take #57") {
		t.Fatalf("ended %v text %q", ended, text)
	}
}

func TestNewPromptEndsTheWaitQuietly(t *testing.T) {
	r := start(t)
	r.idleAgent(t, "builder", "session-1", "")
	woke := r.wake(t, "session-1")
	r.sessions.Report(context.Background(), connect.NewRequest(&agorav1.ReportRequest{SessionId: "session-1", Event: agorav1.SessionEvent_SESSION_EVENT_PROMPT}))
	if text, ended := within(t, woke, 2*time.Second); !ended || text != "" {
		t.Fatalf("ended %v text %q", ended, text)
	}
}

func TestNewerWaitReplacesTheOlder(t *testing.T) {
	r := start(t)
	r.idleAgent(t, "builder", "session-1", "")
	older := r.wake(t, "session-1")
	time.Sleep(100 * time.Millisecond)
	newer := r.wake(t, "session-1")
	if text, ended := within(t, older, 2*time.Second); !ended || text != "" {
		t.Fatalf("older: ended %v text %q", ended, text)
	}
	r.post(t, "reviewer", "@builder ping")
	if text, ended := within(t, newer, 2*time.Second); !ended || !strings.Contains(text, "ping") {
		t.Fatalf("newer: ended %v text %q", ended, text)
	}
}

func TestWakeCommandRunsOncePerMentionAndSkipsWaitingSessions(t *testing.T) {
	log := filepath.Join(shortDir(t), "wakes")
	var h *hub.Hub
	r := startWith(t, func(x *hub.Hub) {
		x.WakeCommand = `printf '%s|%s\n' "$AGORA_TERMINAL" "$AGORA_WAKE_TEXT" >> ` + log
		x.WakeSettle = 0
		h = x
	})
	r.idleAgent(t, "builder", "session-1", "pane-7")
	r.post(t, "reviewer", "@builder ping")
	if err := hub.WakeByCommand(h, context.Background()); err != nil {
		t.Fatal(err)
	}
	hub.WakeByCommand(h, context.Background()) // same mention: no second run
	b, _ := os.ReadFile(log)
	if strings.Count(string(b), "\n") != 1 || !strings.HasPrefix(string(b), "pane-7|Agora: 1 board message(s) addressed to you (reviewer in #general)") {
		t.Fatalf("wakes %q", b)
	}
	// a connector waiting for the session takes precedence over the command
	r.idleAgent(t, "waiter", "session-2", "pane-8")
	woke := r.wake(t, "session-2")
	time.Sleep(100 * time.Millisecond)
	r.post(t, "reviewer", "@waiter ping")
	hub.WakeByCommand(h, context.Background())
	if b, _ := os.ReadFile(log); strings.Contains(string(b), "pane-8") {
		t.Fatalf("command ran for a waiting session: %q", b)
	}
	if text, ended := within(t, woke, 2*time.Second); !ended || !strings.Contains(text, "ping") {
		t.Fatalf("connector: ended %v text %q", ended, text)
	}
}

func TestWakeCommandSkipsDeadProcessesAndRetriesFailures(t *testing.T) {
	log := filepath.Join(shortDir(t), "wakes")
	var h *hub.Hub
	r := startWith(t, func(x *hub.Hub) {
		x.WakeCommand = `test -e ` + log + `.ready && echo "$AGORA_TERMINAL" >> ` + log
		x.WakeSettle = 0
		h = x
	})
	r.idleAgentWithPID(t, "ghost", "session-dead", "pane-dead", 999999) // no such process
	r.idleAgent(t, "builder", "session-1", "pane-7")
	r.post(t, "reviewer", "@ghost @builder ping")
	hub.WakeByCommand(h, context.Background()) // fails: not ready
	os.WriteFile(log+".ready", nil, 0o600)
	hub.WakeByCommand(h, context.Background()) // within the gap: not retried yet
	if b, _ := os.ReadFile(log); len(b) != 0 {
		t.Fatalf("ran within the gap: %q", b)
	}
	r.clock.add(hub.WakeGap)
	hub.WakeByCommand(h, context.Background())
	if b, _ := os.ReadFile(log); string(b) != "pane-7\n" {
		t.Fatalf("wakes %q", b)
	}
}

func TestWakeCommandTimeoutKillsTheWholeGroup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	result, ok := hub.RunWakeCommand(ctx, "sleep 30 & wait")
	if ok || time.Since(start) > 5*time.Second {
		t.Fatalf("ok %v after %s: %s", ok, time.Since(start), result)
	}
}

// greenForge reports pull request 57 of every repository as open with CI green, and counts lookups.
type greenForge struct {
	mu    sync.Mutex
	calls int
}

func (f *greenForge) Lookup(context.Context, forge.Repo, forge.Query) (forge.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return forge.Result{DefaultBranch: "main", PRs: []forge.PR{{Number: 57, Head: "a1", Open: true, Checks: []forge.Check{{Name: "test", Outcome: forge.Succeeded}}}}}, nil
}

func (f *greenForge) count() int { f.mu.Lock(); defer f.mu.Unlock(); return f.calls }

// joinIn joins builder working in a checkout whose origin is on GitHub, declaring #57.
func joinIn(t *testing.T, r *running) {
	t.Helper()
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".git"), 0o755)
	os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644)
	os.WriteFile(filepath.Join(dir, ".git", "config"), []byte("[remote \"origin\"]\n\turl = git@github.com:example-org/example-app.git\n"), 0o644)
	ctx := context.Background()
	if _, err := r.sessions.JoinName(ctx, connect.NewRequest(&agorav1.JoinNameRequest{Name: "builder"})); err != nil {
		t.Fatal(err)
	}
	if _, err := r.agents.UpdateProfile(ctx, connect.NewRequest(&agorav1.UpdateProfileRequest{Name: "builder", Cwd: &dir, AddPrs: []int32{57}})); err != nil {
		t.Fatal(err)
	}
}

func TestHubWatchesPullRequestsAndPostsCI(t *testing.T) {
	f := &greenForge{}
	r := startWith(t, func(h *hub.Hub) {
		h.Forges = forge.Forges{"github.com": f}
		h.WatchFirst, h.WatchEvery = 20*time.Millisecond, 20*time.Millisecond
	})
	joinIn(t, r)
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err := r.rooms.Unread(context.Background(), connect.NewRequest(&agorav1.UnreadRequest{Agent: "builder", Peek: true}))
		if err != nil {
			t.Fatal(err)
		}
		if msgs := resp.Msg.GetMessages(); len(msgs) == 1 {
			if m := msgs[0]; m.GetAuthor() != "agora" || m.GetBody() != "@builder CI is green on #57." || !m.GetAddressed() {
				t.Fatalf("message %+v", m)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no CI message after %d lookups", f.count())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestWatchingTurnedOff(t *testing.T) {
	r := startWith(t, func(h *hub.Hub) { h.WatchFirst, h.WatchEvery = 10*time.Millisecond, 10*time.Millisecond })
	joinIn(t, r) // no forges: what --watch-prs=false gives
	time.Sleep(200 * time.Millisecond)
	resp, _ := r.rooms.Unread(context.Background(), connect.NewRequest(&agorav1.UnreadRequest{Agent: "builder", Peek: true}))
	if len(resp.Msg.GetMessages()) != 0 {
		t.Fatalf("messages %+v", resp.Msg.GetMessages())
	}
}
