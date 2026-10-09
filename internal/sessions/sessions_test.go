package sessions_test

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

var ctx = context.Background()

type env struct {
	s     *sessions.Sessions
	q     *queue.Queue
	r     *rooms.Rooms
	a     *agents.Agents
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
	r := rooms.New(db, c.now)
	return env{s: sessions.New(db, q, r, c.now), q: q, r: r, a: agents.New(db, q, c.now), clock: c}
}

func (e env) report(t *testing.T, id string, ev sessions.Event) sessions.Reply {
	t.Helper()
	r, err := e.s.Report(ctx, sessions.Report{SessionID: id, Kind: "claude-code", Event: ev, PID: 4242, PIDStart: 111, CWD: "/src/example-app"})
	if err != nil {
		t.Fatalf("report %s %s: %v", id, ev, err)
	}
	return r
}

func (e env) join(t *testing.T, name, session string) {
	t.Helper()
	if _, err := e.s.Join(ctx, name, session, false); err != nil {
		t.Fatalf("join %s: %v", name, err)
	}
}

func (e env) state(t *testing.T, id string) sessions.State {
	t.Helper()
	list, err := e.s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range list {
		if x.ID == id {
			return x.State
		}
	}
	return sessions.Ended
}

func (e env) places(t *testing.T, agent string) []queue.Entry {
	t.Helper()
	entries, err := e.q.EntriesOf(ctx, agent)
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

// --- identity -----------------------------------------------------------------------

func TestNamesFollowTheRule(t *testing.T) {
	e := newEnv(t)
	if _, err := e.s.Join(ctx, "reviewer-2", "", false); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"Reviewer_2", "x", "all", "agora", "2fast", strings.Repeat("a", 33)} {
		if _, err := e.s.Join(ctx, bad, "", false); !errors.Is(err, sessions.ErrInvalid) {
			t.Errorf("%q: err = %v", bad, err)
		}
	}
}

func TestJoiningBindsTheNameToTheSession(t *testing.T) {
	e := newEnv(t)
	e.report(t, "session-1", sessions.Start)
	e.join(t, "builder", "session-1")
	if got, _ := e.s.Resolve(ctx, "session-1"); got != "builder" {
		t.Fatalf("resolved %q", got)
	}
}

func TestSwitchingNamesWithinASession(t *testing.T) {
	e := newEnv(t)
	e.join(t, "alpha", "session-1")
	e.join(t, "beta", "session-1")
	if got, _ := e.s.Resolve(ctx, "session-1"); got != "beta" {
		t.Fatalf("resolved %q", got)
	}
	e.join(t, "alpha", "session-2") // alpha is free again
}

func TestANameBelongsToOneLiveSession(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "session-1")
	if _, err := e.s.Join(ctx, "builder", "session-2", false); !errors.Is(err, sessions.ErrNameTaken) {
		t.Fatalf("err = %v", err)
	}
	if _, err := e.s.Join(ctx, "builder", "session-2", true); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.s.Resolve(ctx, "session-1"); got != "" {
		t.Fatalf("old session still bound to %q", got)
	}
	if got, _ := e.s.Resolve(ctx, "session-2"); got != "builder" {
		t.Fatalf("new session bound to %q", got)
	}
}

func TestNameOfAnEndedSessionIsFree(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "session-1")
	e.report(t, "session-1", sessions.End)
	e.join(t, "builder", "session-2")
}

// --- sessions -----------------------------------------------------------------------

func TestSessionStatesFollowEvents(t *testing.T) {
	e := newEnv(t)
	e.report(t, "session-1", sessions.Start)
	if st := e.state(t, "session-1"); st != sessions.Busy {
		t.Fatalf("after start: %s", st)
	}
	e.report(t, "session-1", sessions.Stop)
	if st := e.state(t, "session-1"); st != sessions.Idle {
		t.Fatalf("after stop: %s", st)
	}
	e.report(t, "session-1", sessions.Prompt)
	if st := e.state(t, "session-1"); st != sessions.Busy {
		t.Fatalf("after prompt: %s", st)
	}
}

func TestFirstEventRegistersTheSession(t *testing.T) {
	e := newEnv(t)
	e.report(t, "session-1", sessions.Tool)
	list, _ := e.s.List(ctx)
	if len(list) != 1 || list[0].Kind != "claude-code" || list[0].PID != 4242 || list[0].CWD != "/src/example-app" {
		t.Fatalf("sessions %+v", list)
	}
}

func TestInvalidSessionIDsAreRefused(t *testing.T) {
	e := newEnv(t)
	for _, id := range []string{"short", "has space in it", strings.Repeat("a", 81), "../../etc"} {
		if _, err := e.s.Report(ctx, sessions.Report{SessionID: id, Event: sessions.Start}); !errors.Is(err, sessions.ErrInvalid) {
			t.Errorf("%q: err = %v", id, err)
		}
	}
}

func TestDeadProcessEndsTheSessionAndGivesBackPlaces(t *testing.T) {
	e := newEnv(t)
	e.report(t, "session-1", sessions.Start)
	e.join(t, "builder", "session-1")
	if _, _, err := e.q.Join(ctx, "example-app/merge", "builder", "", 0, false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.q.Join(ctx, "heavy/typecheck", "other", "", 0, false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.q.Join(ctx, "heavy/typecheck", "builder", "", 0, false); err != nil {
		t.Fatal(err)
	}
	ended, err := e.s.EndDead(ctx, func(pid int, _ int64) bool { return pid != 4242 })
	if err != nil || len(ended) != 1 {
		t.Fatalf("ended %v, err %v", ended, err)
	}
	if st := e.state(t, "session-1"); st != sessions.Ended {
		t.Fatalf("state %s", st)
	}
	if p := e.places(t, "builder"); len(p) != 0 {
		t.Fatalf("builder still queued: %+v", p)
	}
}

func TestLiveProcessesAreLeftAlone(t *testing.T) {
	e := newEnv(t)
	e.report(t, "session-1", sessions.Start)
	if ended, _ := e.s.EndDead(ctx, func(int, int64) bool { return true }); len(ended) != 0 {
		t.Fatalf("ended %v", ended)
	}
}

func TestEndedSessionKeepsPlacesOfAnAgentThatMovedOn(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "session-1")
	if _, _, err := e.q.Join(ctx, "db/shared", "builder", "", 0, false); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Join(ctx, "builder", "session-2", true); err != nil {
		t.Fatal(err)
	}
	e.report(t, "session-1", sessions.End)
	if p := e.places(t, "builder"); len(p) != 1 {
		t.Fatalf("places %+v", p)
	}
}

func TestSessionEndGivesBackPlaces(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "session-1")
	if _, _, err := e.q.Join(ctx, "db/shared", "builder", "", 0, false); err != nil {
		t.Fatal(err)
	}
	e.report(t, "session-1", sessions.End)
	if p := e.places(t, "builder"); len(p) != 0 {
		t.Fatalf("places %+v", p)
	}
}

// --- greeting (Claude Code connector) ------------------------------------------------

func TestUnboundStartIsInvited(t *testing.T) {
	e := newEnv(t)
	repo := filepath.Join(t.TempDir(), "example-app")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	start := sessions.Report{SessionID: "session-1", Kind: "claude-code", Event: sessions.Start, PID: 4242, PIDStart: 111, CWD: filepath.Join(repo, "src")}
	for range 2 { // a new session, then a resumed or compacted one
		r, err := e.s.Report(ctx, start)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{
			"agora join <name> --project example-app --task '<what you are doing>'",
			"(name: 2-32 lowercase letters, digits and dashes, starting with a letter)", "agora status", "agora charter", "If you",
		} {
			if !strings.Contains(r.Context, want) {
				t.Fatalf("invitation lacks %q: %q", want, r.Context)
			}
		}
	}
	if r := e.report(t, "session-1", sessions.Prompt); r.Context != "" {
		t.Fatalf("prompt: %q", r.Context)
	}
	e.clock.add(sessions.ToolCheckEvery)
	if r := e.report(t, "session-1", sessions.Tool); r.Context != "" {
		t.Fatalf("tool: %q", r.Context)
	}
}

func TestUnboundStartOutsideARepositoryUsesAPlaceholder(t *testing.T) {
	e := newEnv(t)
	r, err := e.s.Report(ctx, sessions.Report{SessionID: "session-1", Event: sessions.Start, CWD: t.TempDir()})
	if err != nil || !strings.Contains(r.Context, "--project <project>") {
		t.Fatalf("context %q err %v", r.Context, err)
	}
}

func TestBoundStartAfterCompactionRemindsWhoTheAgentIs(t *testing.T) {
	e := newEnv(t)
	e.report(t, "session-1", sessions.Start)
	e.join(t, "builder", "session-1")
	task, status := "fix login timeout", "reviewing"
	if _, err := e.a.Update(ctx, "builder", agents.Update{Task: &task, Status: &status}); err != nil {
		t.Fatal(err)
	}
	e.join(t, "reviewer", "")
	if _, err := e.r.Post(ctx, "reviewer", "general", "@builder please review #57", 0); err != nil {
		t.Fatal(err)
	}
	r := e.report(t, "session-1", sessions.Start) // the conversation was compacted
	for _, want := range []string{
		"Agora: you are builder on the Agora board.", "Task: fix login timeout.", "Status: reviewing.",
		"Rooms: #general.", "1 unread message addresses you (below).", "agora unread", "agora set --task", "agora leave",
	} {
		if !strings.Contains(r.Context, want) {
			t.Fatalf("reminder lacks %q: %q", want, r.Context)
		}
	}
	if strings.Index(r.Context, "please review #57") < strings.Index(r.Context, "agora leave") {
		t.Fatalf("message not after the reminder: %q", r.Context)
	}
}

func TestStartRemindsEvenWhenAConcurrentHookWonTheCheck(t *testing.T) {
	e := newEnv(t)
	e.report(t, "session-1", sessions.Start)
	e.join(t, "builder", "session-1")
	e.join(t, "reviewer", "")
	if _, err := e.r.Post(ctx, "reviewer", "general", "@builder please review #57", 0); err != nil {
		t.Fatal(err)
	}
	sessions.RaceNoteCheck(t) // a concurrent prompt records its check first
	r := e.report(t, "session-1", sessions.Start)
	if !strings.HasPrefix(r.Context, "Agora: you are builder on the Agora board.") || !strings.Contains(r.Context, "please review #57") {
		t.Fatalf("context %q", r.Context)
	}
}

func TestReminderCountsAddressedMessagesNotShown(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "session-1")
	e.join(t, "reviewer", "")
	for range 7 {
		if _, err := e.r.Post(ctx, "reviewer", "general", "@builder ping", 0); err != nil {
			t.Fatal(err)
		}
	}
	r := e.report(t, "session-1", sessions.Start)
	if !strings.Contains(r.Context, "7 unread messages address you (5 below).") {
		t.Fatalf("context %q", r.Context)
	}
}

func TestUnsafeRepositoryNameIsNotSuggested(t *testing.T) {
	e := newEnv(t)
	repo := filepath.Join(t.TempDir(), "example app;x")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := e.s.Report(ctx, sessions.Report{SessionID: "session-1", Event: sessions.Start, CWD: repo})
	if err != nil || !strings.Contains(r.Context, "--project <project>") {
		t.Fatalf("context %q err %v", r.Context, err)
	}
}

// --- reminders (Claude Code connector) ------------------------------------------------

func TestSessionStartRemindsOfHeldSlots(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "session-1")
	if _, _, err := e.q.Join(ctx, "db/shared", "builder", "", 10*time.Minute, false); err != nil {
		t.Fatal(err)
	}
	r := e.report(t, "session-1", sessions.Start)
	if !strings.Contains(r.Context, "you are builder") || !strings.Contains(r.Context, "You hold db/shared until") {
		t.Fatalf("context %q", r.Context)
	}
}

func TestNoNoteWhenNothingChanged(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "session-1")
	if _, _, err := e.q.Join(ctx, "db/shared", "builder", "", 0, false); err != nil {
		t.Fatal(err)
	}
	e.report(t, "session-1", sessions.Start)
	if r := e.report(t, "session-1", sessions.Prompt); r.Context != "" {
		t.Fatalf("context %q", r.Context)
	}
}

func TestOfferedSlotIsAnnouncedAfterToolUse(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "session-1")
	if _, _, err := e.q.Join(ctx, "example-app/merge", "other", "", 0, false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.q.Join(ctx, "example-app/merge", "builder", "", 0, false); err != nil {
		t.Fatal(err)
	}
	e.report(t, "session-1", sessions.Prompt)
	if _, err := e.q.Release(ctx, "example-app/merge", "other", "other", false); err != nil {
		t.Fatal(err)
	}
	if r := e.report(t, "session-1", sessions.Tool); r.Context != "" {
		t.Fatalf("tool use within 15s noted: %q", r.Context)
	}
	e.clock.add(sessions.ToolCheckEvery)
	r := e.report(t, "session-1", sessions.Tool)
	if !strings.Contains(r.Context, "your turn on example-app/merge") || !strings.Contains(r.Context, "agora queue renew example-app/merge") {
		t.Fatalf("context %q", r.Context)
	}
}

func TestOfferedSlotKeepsTheTurnGoingOnce(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "session-1")
	if _, _, err := e.q.Join(ctx, "example-app/merge", "other", "", 0, false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.q.Join(ctx, "example-app/merge", "builder", "", 0, false); err != nil {
		t.Fatal(err)
	}
	if _, err := e.q.Release(ctx, "example-app/merge", "other", "other", false); err != nil {
		t.Fatal(err)
	}
	r := e.report(t, "session-1", sessions.Stop)
	if !r.Block || !strings.Contains(r.BlockReason, "agora queue release example-app/merge") {
		t.Fatalf("reply %+v", r)
	}
	if st := e.state(t, "session-1"); st == sessions.Idle {
		t.Fatal("session went idle while blocked")
	}
	r, err := e.s.Report(ctx, sessions.Report{SessionID: "session-1", Event: sessions.Stop, StopActive: true})
	if err != nil || r.Block {
		t.Fatalf("second stop: %+v %v", r, err)
	}
	if st := e.state(t, "session-1"); st != sessions.Idle {
		t.Fatalf("state %s", st)
	}
}

func alwaysAlive(int, int64) bool { return true }

func TestJoiningAnEndedSessionOnlyRegistersTheName(t *testing.T) {
	e := newEnv(t)
	e.report(t, "session-1", sessions.Start)
	e.report(t, "session-1", sessions.End)
	bound, err := e.s.Join(ctx, "builder", "session-1", false)
	if err != nil || bound {
		t.Fatalf("bound %v err %v", bound, err)
	}
	if st := e.state(t, "session-1"); st != sessions.Ended {
		t.Fatalf("session came back as %s", st)
	}
}

func TestUnknownProcessEndsAfterSixHoursWithoutEvents(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "session-1") // registered by join only: no process known
	if _, _, err := e.q.Join(ctx, "db/shared", "builder", "", 0, false); err != nil {
		t.Fatal(err)
	}
	e.clock.add(sessions.UnknownProcessTimeout - time.Minute)
	if ended, _ := e.s.EndDead(ctx, alwaysAlive); len(ended) != 0 {
		t.Fatalf("ended early: %v", ended)
	}
	e.clock.add(time.Minute)
	if ended, _ := e.s.EndDead(ctx, alwaysAlive); len(ended) != 1 {
		t.Fatalf("ended %v", ended)
	}
	if p := e.places(t, "builder"); len(p) != 0 {
		t.Fatalf("places %+v", p)
	}
	e.join(t, "builder", "session-2") // the name is free again
}

func TestReusedPidDoesNotKeepASessionAlive(t *testing.T) {
	e := newEnv(t)
	e.report(t, "session-1", sessions.Start) // pid 4242 started at 111
	sameProcess := func(pid int, start int64) bool { return pid == 4242 && start == 111 }
	if ended, _ := e.s.EndDead(ctx, sameProcess); len(ended) != 0 {
		t.Fatalf("ended a live session: %v", ended)
	}
	reused := func(pid int, start int64) bool { return pid == 4242 && start == 999 }
	if ended, _ := e.s.EndDead(ctx, reused); len(ended) != 1 {
		t.Fatalf("ended %v", ended)
	}
}

func TestClearKeepsTheNameAndPlaces(t *testing.T) {
	e := newEnv(t)
	e.report(t, "session-1", sessions.Start)
	e.join(t, "builder", "session-1")
	if _, _, err := e.q.Join(ctx, "db/shared", "builder", "", 0, false); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Report(ctx, sessions.Report{SessionID: "session-1", Event: sessions.End, Reason: sessions.EndReasonClear}); err != nil {
		t.Fatal(err)
	}
	r := e.report(t, "session-2", sessions.Start) // same process 4242/111
	if got, _ := e.s.Resolve(ctx, "session-2"); got != "builder" {
		t.Fatalf("new session bound to %q", got)
	}
	if got, _ := e.s.Resolve(ctx, "session-1"); got != "" {
		t.Fatalf("old session still bound to %q", got)
	}
	if p := e.places(t, "builder"); len(p) != 1 {
		t.Fatalf("places %+v", p)
	}
	if !strings.HasPrefix(r.Context, "Agora: you are builder on the Agora board.") || !strings.Contains(r.Context, "You hold db/shared") {
		t.Fatalf("context %q", r.Context)
	}
}

func TestLostPlacesAreAnnounced(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "session-1")
	if _, _, err := e.q.Join(ctx, "db/shared", "builder", "", 10*time.Minute, false); err != nil {
		t.Fatal(err)
	}
	e.report(t, "session-1", sessions.Start)
	e.clock.add(10 * time.Minute)
	r := e.report(t, "session-1", sessions.Prompt)
	if !strings.Contains(r.Context, "no longer hold or wait for db/shared") {
		t.Fatalf("context %q", r.Context)
	}
	if r := e.report(t, "session-1", sessions.Prompt); r.Context != "" {
		t.Fatalf("repeated: %q", r.Context)
	}
}

func TestConcurrentHooksNoteOnce(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "session-1")
	if _, _, err := e.q.Join(ctx, "db/shared", "builder", "", 0, false); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	notes := 0
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := e.s.Report(ctx, sessions.Report{SessionID: "session-1", Event: sessions.Prompt})
			if err != nil {
				t.Error(err)
			}
			if r.Context != "" {
				mu.Lock()
				notes++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if notes != 1 {
		t.Fatalf("%d notes", notes)
	}
}

// --- delivery ---------------------------------------------------------------------------

func TestMessagesArriveDuringTheTurn(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "session-1")
	e.join(t, "reviewer", "")
	e.report(t, "session-1", sessions.Prompt)
	if _, err := e.r.Post(ctx, "reviewer", "general", "PR #57 is ready", 0); err != nil {
		t.Fatal(err)
	}
	if r := e.report(t, "session-1", sessions.Tool); r.Context != "" {
		t.Fatalf("delivered within 15s: %q", r.Context)
	}
	e.clock.add(sessions.ToolCheckEvery)
	r := e.report(t, "session-1", sessions.Tool)
	if !strings.Contains(r.Context, "PR #57 is ready") || !strings.Contains(r.Context, "agora post <room>") {
		t.Fatalf("context %q", r.Context)
	}
	e.clock.add(sessions.ToolCheckEvery)
	if r := e.report(t, "session-1", sessions.Tool); r.Context != "" {
		t.Fatalf("delivered twice: %q", r.Context)
	}
}

func TestOnlyFiveMessagesAtOnce(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "session-1")
	e.join(t, "reviewer", "")
	for range 8 {
		if _, err := e.r.Post(ctx, "reviewer", "general", "note", 0); err != nil {
			t.Fatal(err)
		}
	}
	r := e.report(t, "session-1", sessions.Prompt)
	if strings.Count(r.Context, "#general [") != 5 || !strings.Contains(r.Context, "3 more") {
		t.Fatalf("context %q", r.Context)
	}
	if left, total, _ := e.r.Unread(ctx, "builder", false, 0); total != 3 || len(left) != 3 {
		t.Fatalf("%d left", total)
	}
}

func TestMentionKeepsTheTurnGoingOnce(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "session-1")
	e.join(t, "reviewer", "")
	if _, err := e.r.Post(ctx, "reviewer", "general", "chatter", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := e.r.Post(ctx, "reviewer", "general", "@builder can you take #57?", 0); err != nil {
		t.Fatal(err)
	}
	r := e.report(t, "session-1", sessions.Stop)
	if !r.Block || !strings.Contains(r.BlockReason, "can you take #57") || strings.Contains(r.BlockReason, "chatter") {
		t.Fatalf("reply %+v", r)
	}
	if left, _, _ := e.r.Unread(ctx, "builder", false, 0); len(left) != 1 || left[0].Body != "chatter" {
		t.Fatalf("unread %+v", left)
	}
	if r, _ := e.s.Report(ctx, sessions.Report{SessionID: "session-1", Event: sessions.Stop, StopActive: true}); r.Block {
		t.Fatalf("blocked again: %+v", r)
	}
}

func TestMentionAndOfferBlockTogether(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "session-1")
	e.join(t, "reviewer", "")
	if _, _, err := e.q.Join(ctx, "example-app/merge", "reviewer", "", 0, false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.q.Join(ctx, "example-app/merge", "builder", "", 0, false); err != nil {
		t.Fatal(err)
	}
	if _, err := e.q.Release(ctx, "example-app/merge", "reviewer", "reviewer", false); err != nil {
		t.Fatal(err)
	}
	if _, err := e.r.Post(ctx, "reviewer", "general", "@builder your turn to merge", 0); err != nil {
		t.Fatal(err)
	}
	r := e.report(t, "session-1", sessions.Stop)
	if !r.Block || !strings.Contains(r.BlockReason, "your turn to merge") || !strings.Contains(r.BlockReason, "agora queue renew example-app/merge") {
		t.Fatalf("reply %+v", r)
	}
}

// --- wakeups ---------------------------------------------------------------------------

func TestCheckWake(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "session-1")
	e.join(t, "reviewer", "")
	e.report(t, "session-1", sessions.Prompt)
	turn, _ := e.s.Turn(ctx, "session-1")

	if _, done, _ := e.s.CheckWake(ctx, "session-1", turn); done {
		t.Fatal("busy session finished its wait")
	}
	e.report(t, "session-1", sessions.Stop) // idle
	if _, done, _ := e.s.CheckWake(ctx, "session-1", turn); done {
		t.Fatal("woken with nothing waiting")
	}
	if _, err := e.r.Post(ctx, "reviewer", "general", "chatter", 0); err != nil {
		t.Fatal(err)
	}
	if _, done, _ := e.s.CheckWake(ctx, "session-1", turn); done {
		t.Fatal("woken by chatter")
	}
	if _, err := e.r.Post(ctx, "reviewer", "general", "@builder can you take #57?", 0); err != nil {
		t.Fatal(err)
	}
	w, done, err := e.s.CheckWake(ctx, "session-1", turn)
	if err != nil || !done || w == nil || !strings.Contains(w.Text, "can you take #57") {
		t.Fatalf("wake %+v done %v err %v", w, done, err)
	}
	if left, _, _ := e.r.Unread(ctx, "builder", true, 0); len(left) != 1 {
		t.Fatal("mention consumed before the wake was delivered")
	}
	if err := e.s.ConfirmWake(ctx, "session-1", w); err != nil {
		t.Fatal(err)
	}
	if left, _, _ := e.r.Unread(ctx, "builder", true, 0); len(left) != 0 {
		t.Fatalf("mention still unread after the wake: %+v", left)
	}
	if st := e.state(t, "session-1"); st != sessions.Busy {
		t.Fatalf("woken session is %s", st)
	}
	if next, _ := e.s.Turn(ctx, "session-1"); next != turn+1 {
		t.Fatalf("turn %d, want %d", next, turn+1)
	}
}

func TestWaitEndsQuietlyOnANewTurn(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "session-1")
	e.report(t, "session-1", sessions.Stop)
	turn, _ := e.s.Turn(ctx, "session-1")
	e.report(t, "session-1", sessions.Prompt)
	if w, done, _ := e.s.CheckWake(ctx, "session-1", turn); !done || w != nil {
		t.Fatalf("wake %+v done %v", w, done)
	}
}

func TestWaitEndsWhenTheSessionLosesItsAgent(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "session-1")
	e.report(t, "session-1", sessions.Stop)
	turn, _ := e.s.Turn(ctx, "session-1")
	if _, err := e.s.Join(ctx, "builder", "session-2", true); err != nil {
		t.Fatal(err)
	}
	if w, done, _ := e.s.CheckWake(ctx, "session-1", turn); !done || w != nil {
		t.Fatalf("wake %+v done %v", w, done)
	}
}

func TestAnOfferWakesOnce(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "session-1")
	e.report(t, "session-1", sessions.Stop)
	turn, _ := e.s.Turn(ctx, "session-1")
	if _, _, err := e.q.Join(ctx, "example-app/merge", "other", "", 0, false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.q.Join(ctx, "example-app/merge", "builder", "", 0, false); err != nil {
		t.Fatal(err)
	}
	if _, err := e.q.Release(ctx, "example-app/merge", "other", "other", false); err != nil {
		t.Fatal(err)
	}
	w, done, _ := e.s.CheckWake(ctx, "session-1", turn)
	if !done || w == nil || !strings.Contains(w.Text, "agora queue renew example-app/merge") {
		t.Fatalf("wake %+v done %v", w, done)
	}
	if err := e.s.ConfirmWake(ctx, "session-1", w); err != nil {
		t.Fatal(err)
	}
	// the woken turn ends unclaimed
	if _, err := e.s.Report(ctx, sessions.Report{SessionID: "session-1", Event: sessions.Stop, StopActive: true}); err != nil {
		t.Fatal(err)
	}
	turn, _ = e.s.Turn(ctx, "session-1")
	if w, done, _ := e.s.CheckWake(ctx, "session-1", turn); done {
		t.Fatalf("woken again for the same offer: %+v", w)
	}
}

func TestPendingKeyChangesWithNewMentions(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "")
	e.join(t, "reviewer", "")
	if key, _, _ := e.s.Pending(ctx, "builder"); key != "" {
		t.Fatalf("key %q", key)
	}
	if _, err := e.r.Post(ctx, "reviewer", "general", "@builder one", 0); err != nil {
		t.Fatal(err)
	}
	k1, text, _ := e.s.Pending(ctx, "builder")
	if k1 == "" || !strings.Contains(text, "reviewer in #general") || strings.Contains(text, "`") {
		t.Fatalf("key %q text %q", k1, text)
	}
	if k, _, _ := e.s.Pending(ctx, "builder"); k != k1 {
		t.Fatal("pending consumed the mention")
	}
	if _, err := e.r.Post(ctx, "reviewer", "general", "@builder two", 0); err != nil {
		t.Fatal(err)
	}
	if k2, _, _ := e.s.Pending(ctx, "builder"); k2 == k1 {
		t.Fatal("key did not change")
	}
}
