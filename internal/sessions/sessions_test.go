package sessions_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vsem-azamat/agora/internal/queue"
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
	return env{s: sessions.New(db, q, c.now), q: q, clock: c}
}

func (e env) report(t *testing.T, id string, ev sessions.Event) sessions.Reply {
	t.Helper()
	r, err := e.s.Report(ctx, sessions.Report{SessionID: id, Kind: "claude-code", Event: ev, PID: 4242, CWD: "/src/example-app"})
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
	e.q.Join(ctx, "example-app/merge", "builder", "", 0, false)
	e.q.Join(ctx, "heavy/typecheck", "other", "", 0, false)
	e.q.Join(ctx, "heavy/typecheck", "builder", "", 0, false)
	ended, err := e.s.EndDead(ctx, func(pid int) bool { return pid != 4242 })
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
	if ended, _ := e.s.EndDead(ctx, func(int) bool { return true }); len(ended) != 0 {
		t.Fatalf("ended %v", ended)
	}
}

func TestEndedSessionKeepsPlacesOfAnAgentThatMovedOn(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "session-1")
	e.q.Join(ctx, "db/shared", "builder", "", 0, false)
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
	e.q.Join(ctx, "db/shared", "builder", "", 0, false)
	e.report(t, "session-1", sessions.End)
	if p := e.places(t, "builder"); len(p) != 0 {
		t.Fatalf("places %+v", p)
	}
}

// --- reminders (Claude Code connector) ------------------------------------------------

func TestSessionStartRemindsOfHeldSlots(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "session-1")
	e.q.Join(ctx, "db/shared", "builder", "", 10*time.Minute, false)
	r := e.report(t, "session-1", sessions.Start)
	if !strings.Contains(r.Context, "you are builder") || !strings.Contains(r.Context, "You hold db/shared until") {
		t.Fatalf("context %q", r.Context)
	}
}

func TestNoNoteWhenNothingChanged(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "session-1")
	e.q.Join(ctx, "db/shared", "builder", "", 0, false)
	e.report(t, "session-1", sessions.Start)
	if r := e.report(t, "session-1", sessions.Prompt); r.Context != "" {
		t.Fatalf("context %q", r.Context)
	}
}

func TestOfferedSlotIsAnnouncedAfterToolUse(t *testing.T) {
	e := newEnv(t)
	e.join(t, "builder", "session-1")
	e.q.Join(ctx, "example-app/merge", "other", "", 0, false)
	e.q.Join(ctx, "example-app/merge", "builder", "", 0, false)
	e.report(t, "session-1", sessions.Prompt)
	e.q.Release(ctx, "example-app/merge", "other", "other", false)
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
	e.q.Join(ctx, "example-app/merge", "other", "", 0, false)
	e.q.Join(ctx, "example-app/merge", "builder", "", 0, false)
	e.q.Release(ctx, "example-app/merge", "other", "other", false)
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
