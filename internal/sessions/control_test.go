package sessions_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/vsem-azamat/agora/internal/rooms"
	"github.com/vsem-azamat/agora/internal/sessions"
	"github.com/vsem-azamat/agora/internal/store"
)

var allCommands = sessions.Commands{Wake: true, Compact: true, Interrupt: true}

func alive(int, int64) bool { return true }

// claim does what the hub does: it claims every candidate compact that may run.
func (e env) claim(settle time.Duration) ([]sessions.QueuedCompact, error) {
	return e.claimAlive(settle, alive)
}

func (e env) claimAlive(settle time.Duration, alive func(int, int64) bool) ([]sessions.QueuedCompact, error) {
	cands, err := e.s.CompactCandidates(ctx, settle, alive)
	var out []sessions.QueuedCompact
	for _, c := range cands {
		ok, cerr := e.s.ClaimCompact(ctx, c.ID, settle)
		if cerr != nil {
			return out, cerr
		}
		if ok {
			out = append(out, c)
		}
	}
	return out, err
}

// terminalSession starts session-1 in a terminal, bound to builder, with reviewer joined.
func (e env) terminalSession(t *testing.T, terminal string) {
	t.Helper()
	if _, err := e.s.Report(ctx, sessions.Report{SessionID: "session-1", Kind: "claude-code", Event: sessions.Start, PID: 4242, PIDStart: 111, Terminal: terminal}); err != nil {
		t.Fatal(err)
	}
	e.join(t, "builder", "session-1")
	e.join(t, "reviewer", "")
}

func (e env) notices(t *testing.T, room string) []string {
	t.Helper()
	var out []string
	for _, m := range e.history(t, room) {
		if m.Author == rooms.Board {
			out = append(out, m.Body)
		}
	}
	return out
}

func TestCapabilitiesFollowKindTerminalAndCommands(t *testing.T) {
	got := sessions.CapabilitiesOf(sessions.Commands{Wake: true, Compact: true}, "claude-code", "pane-7", 4242)
	if want := (sessions.Capabilities{Wake: "hook", Compact: "terminal", Interrupt: "none"}); got != want {
		t.Fatalf("claude code in a terminal: %+v", got)
	}
	got = sessions.CapabilitiesOf(allCommands, "claude-code", "", 4242)
	if want := (sessions.Capabilities{Wake: "hook", Compact: "none", Interrupt: "none"}); got != want {
		t.Fatalf("no terminal: %+v", got)
	}
	got = sessions.CapabilitiesOf(allCommands, "codex", "", 4242)
	if want := (sessions.Capabilities{Wake: "none", Compact: "none", Interrupt: "none"}); got != want {
		t.Fatalf("other tool without a terminal: %+v", got)
	}
	got = sessions.CapabilitiesOf(allCommands, "codex", "pane-7", 4242)
	if want := (sessions.Capabilities{Wake: "terminal", Compact: "terminal", Interrupt: "terminal"}); got != want {
		t.Fatalf("other tool in a terminal: %+v", got)
	}
	if got = sessions.CapabilitiesOf(allCommands, "codex", "pane-7", 0); got.Compact != "none" {
		t.Fatalf("unknown process: %+v", got)
	}
	if got = sessions.CapabilitiesOf(sessions.Commands{}, "codex", "pane-7", 4242); got.Compact != "none" {
		t.Fatalf("hub without commands: %+v", got)
	}
}

func TestCompactIsRefusedWithWhatIsMissing(t *testing.T) {
	e := newEnv(t)
	e.terminalSession(t, "")
	_, err := e.s.Compact(ctx, allCommands, "reviewer", "builder", "")
	if !errors.Is(err, sessions.ErrRefused) || !strings.Contains(err.Error(), "no terminal") {
		t.Fatalf("no terminal: %v", err)
	}
	e = newEnv(t)
	e.terminalSession(t, "pane-7")
	_, err = e.s.PrepareInterrupt(ctx, sessions.Commands{Compact: true}, "reviewer", "builder")
	if !errors.Is(err, sessions.ErrRefused) || !strings.Contains(err.Error(), "--interrupt-command") {
		t.Fatalf("no command: %v", err)
	}
	if _, err := e.s.Compact(ctx, allCommands, "reviewer", "ghost", ""); err == nil {
		t.Fatal("compact of an unknown agent was accepted")
	}
	e.join(t, "idler", "")
	if _, err := e.s.Compact(ctx, allCommands, "reviewer", "idler", ""); !errors.Is(err, sessions.ErrRefused) || !strings.Contains(err.Error(), "no live session") {
		t.Fatalf("agent without a session: %v", err)
	}
	if _, err := e.s.Compact(ctx, allCommands, "stranger", "builder", ""); err == nil {
		t.Fatal("a name that has not joined asked")
	}
}

func TestCompactFocusHasNoControlCharacters(t *testing.T) {
	e := newEnv(t)
	e.terminalSession(t, "pane-7")
	for _, focus := range []string{"two\nlines", "tab\there", strings.Repeat("x", sessions.MaxFocus+1)} {
		if _, err := e.s.Compact(ctx, allCommands, "reviewer", "builder", focus); !errors.Is(err, store.ErrInvalid) {
			t.Fatalf("focus %q: %v", focus, err)
		}
	}
	if q, _ := e.claim(0); len(q) != 0 {
		t.Fatalf("something was queued: %+v", q)
	}
}

func TestCompactRunsOnlyWhenIdleForTheSettleTime(t *testing.T) {
	e := newEnv(t)
	e.terminalSession(t, "pane-7")
	e.report(t, "session-1", sessions.Prompt) // busy
	if _, err := e.s.Compact(ctx, allCommands, "reviewer", "builder", "keep the schema"); err != nil {
		t.Fatal(err)
	}
	e.clock.add(time.Hour)
	if q, err := e.claim(10 * time.Second); err != nil || len(q) != 0 {
		t.Fatalf("a busy session: %+v %v", q, err)
	}
	e.report(t, "session-1", sessions.Stop) // idle
	e.clock.add(5 * time.Second)
	if q, _ := e.claim(10 * time.Second); len(q) != 0 {
		t.Fatalf("idle for 5 seconds: %+v", q)
	}
	e.clock.add(5 * time.Second)
	q, err := e.claim(10 * time.Second)
	if err != nil || len(q) != 1 || q[0].Terminal != "pane-7" || q[0].Text() != "/compact keep the schema" || q[0].By != "reviewer" {
		t.Fatalf("claimed %+v %v", q, err)
	}
	if again, _ := e.claim(0); len(again) != 0 {
		t.Fatalf("claimed twice: %+v", again)
	}
	if err := e.s.FinishCompact(ctx, q[0].ID, true, "ok"); err != nil {
		t.Fatal(err)
	}
	got := e.notices(t, "general")
	if len(got) != 2 || !strings.Contains(got[0], "reviewer queued a compact of builder") || !strings.Contains(got[0], "keep the schema") || got[1] != "reviewer compacted builder" {
		t.Fatalf("notices %q", got)
	}
}

func TestCompactWithoutFocusTypesOnlyTheCommand(t *testing.T) {
	e := newEnv(t)
	e.terminalSession(t, "pane-7")
	if _, err := e.s.Compact(ctx, allCommands, "reviewer", "builder", ""); err != nil {
		t.Fatal(err)
	}
	if q, _ := e.claim(0); len(q) != 1 || q[0].Text() != "/compact" {
		t.Fatalf("claimed %+v", q)
	}
}

func TestSecondCompactIsRefusedUntilTheFirstIsDone(t *testing.T) {
	e := newEnv(t)
	e.terminalSession(t, "pane-7")
	if _, err := e.s.Compact(ctx, allCommands, "reviewer", "builder", ""); err != nil {
		t.Fatal(err)
	}
	e.join(t, "tester", "")
	if _, err := e.s.Compact(ctx, allCommands, "tester", "builder", ""); !errors.Is(err, sessions.ErrRefused) || !strings.Contains(err.Error(), "reviewer") {
		t.Fatalf("second request: %v", err)
	}
	q, _ := e.claim(0)
	if _, err := e.s.Compact(ctx, allCommands, "tester", "builder", ""); err == nil {
		t.Fatal("a compact that runs was queued again")
	}
	if err := e.s.FinishCompact(ctx, q[0].ID, true, "ok"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Compact(ctx, allCommands, "tester", "builder", ""); err != nil {
		t.Fatalf("after the first: %v", err)
	}
}

func TestCompactIsDroppedWhenItsSessionEnds(t *testing.T) {
	e := newEnv(t)
	e.terminalSession(t, "pane-7")
	e.report(t, "session-1", sessions.Prompt)
	if _, err := e.s.Compact(ctx, allCommands, "reviewer", "builder", ""); err != nil {
		t.Fatal(err)
	}
	e.report(t, "session-1", sessions.End)
	if err := e.s.DropEndedCompacts(ctx); err != nil {
		t.Fatal(err)
	}
	if q, _ := e.claim(0); len(q) != 0 {
		t.Fatalf("claimed %+v", q)
	}
	got := e.notices(t, "general")
	if len(got) != 2 || !strings.Contains(got[1], "dropped") || !strings.Contains(got[1], "session ended") {
		t.Fatalf("notices %q", got)
	}
	if err := e.s.DropEndedCompacts(ctx); err != nil || len(e.notices(t, "general")) != 2 {
		t.Fatalf("announced twice: %v", e.notices(t, "general"))
	}
}

func TestFailedCompactIsAnnouncedAndNotRetried(t *testing.T) {
	e := newEnv(t)
	e.terminalSession(t, "pane-7")
	if _, err := e.s.Compact(ctx, allCommands, "reviewer", "builder", ""); err != nil {
		t.Fatal(err)
	}
	q, _ := e.claim(0)
	if err := e.s.FinishCompact(ctx, q[0].ID, false, "exit status 3: no such pane"); err != nil {
		t.Fatal(err)
	}
	if again, _ := e.claim(0); len(again) != 0 {
		t.Fatalf("retried %+v", again)
	}
	got := e.notices(t, "general")
	if last := got[len(got)-1]; !strings.Contains(last, "failed") || !strings.Contains(last, "no such pane") {
		t.Fatalf("notices %q", got)
	}
}

func TestCompactStoppedByAHubRestartIsFailed(t *testing.T) {
	e := newEnv(t)
	e.terminalSession(t, "pane-7")
	if _, err := e.s.Compact(ctx, allCommands, "reviewer", "builder", ""); err != nil {
		t.Fatal(err)
	}
	e.claim(0) //nolint:errcheck,gosec // the claim is what a stopped hub left behind
	if err := e.s.FailRunningCompacts(ctx); err != nil {
		t.Fatal(err)
	}
	got := e.notices(t, "general")
	if last := got[len(got)-1]; !strings.Contains(last, "failed") {
		t.Fatalf("notices %q", got)
	}
	if _, err := e.s.Compact(ctx, allCommands, "reviewer", "builder", ""); err != nil {
		t.Fatalf("a new compact after the failed one: %v", err)
	}
}

func TestInterruptNeedsABusySessionAndMakesItIdle(t *testing.T) {
	e := newEnv(t)
	e.terminalSession(t, "pane-7")
	e.report(t, "session-1", sessions.Stop)
	if _, err := e.s.PrepareInterrupt(ctx, allCommands, "reviewer", "builder"); !errors.Is(err, sessions.ErrRefused) || !strings.Contains(err.Error(), "idle") {
		t.Fatalf("idle session: %v", err)
	}
	e.report(t, "session-1", sessions.Prompt)
	tg, err := e.s.PrepareInterrupt(ctx, allCommands, "reviewer", "builder")
	if err != nil || tg.Terminal != "pane-7" {
		t.Fatalf("busy session: %+v %v", tg, err)
	}
	if e.state(t, "session-1") != sessions.Busy || len(e.notices(t, "general")) != 0 {
		t.Fatal("the session changed before the command ran")
	}
	if err := e.s.FinishInterrupt(ctx, "reviewer", "builder", tg); err != nil {
		t.Fatal(err)
	}
	if e.state(t, "session-1") != sessions.Idle {
		t.Fatalf("state %s", e.state(t, "session-1"))
	}
	if got := e.notices(t, "general"); len(got) != 1 || got[0] != "reviewer interrupted builder" {
		t.Fatalf("notices %q", got)
	}
}

func TestInterruptKeepsANewTurnBusy(t *testing.T) {
	e := newEnv(t)
	e.terminalSession(t, "pane-7")
	e.report(t, "session-1", sessions.Prompt)
	tg, err := e.s.PrepareInterrupt(ctx, allCommands, "reviewer", "builder")
	if err != nil {
		t.Fatal(err)
	}
	e.report(t, "session-1", sessions.Stop)
	e.report(t, "session-1", sessions.Prompt) // a new turn began while the command ran
	if err := e.s.FinishInterrupt(ctx, "reviewer", "builder", tg); err != nil {
		t.Fatal(err)
	}
	if e.state(t, "session-1") != sessions.Busy {
		t.Fatalf("state %s", e.state(t, "session-1"))
	}
}

func TestNoticesGoToTheProjectRoom(t *testing.T) {
	e := newEnv(t)
	e.terminalSession(t, "pane-7")
	e.room(t, "example-app", "reviewer")
	e.follow(t, "builder", "example-app", rooms.ModeAll)
	e.report(t, "session-1", sessions.Prompt)
	tg, err := e.s.PrepareInterrupt(ctx, allCommands, "reviewer", "builder")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.s.FinishInterrupt(ctx, "reviewer", "builder", tg); err != nil {
		t.Fatal(err)
	}
	if got := e.notices(t, "example-app"); len(got) != 1 || got[0] != "reviewer interrupted builder" {
		t.Fatalf("project room %q", got)
	}
	if got := e.notices(t, "general"); len(got) != 0 {
		t.Fatalf("general %q", got)
	}
}

func TestTargetIsToldOnceAfterACompaction(t *testing.T) {
	e := newEnv(t)
	e.terminalSession(t, "pane-7")
	if _, err := e.s.Compact(ctx, allCommands, "reviewer", "builder", "keep the schema"); err != nil {
		t.Fatal(err)
	}
	if r := e.report(t, "session-1", sessions.Tool); strings.Contains(r.Context, "compacted") {
		t.Fatalf("told before it ran: %q", r.Context)
	}
	q, _ := e.claim(0)
	if err := e.s.FinishCompact(ctx, q[0].ID, true, "ok"); err != nil {
		t.Fatal(err)
	}
	r, err := e.s.Report(ctx, sessions.Report{SessionID: "session-1", Kind: "claude-code", Event: sessions.Start, PID: 4242, PIDStart: 111, Reason: sessions.StartReasonCompact})
	if err != nil || !strings.Contains(r.Context, "reviewer compacted your conversation, with the focus: keep the schema") {
		t.Fatalf("start after the compaction: %q %v", r.Context, err)
	}
	if r := e.report(t, "session-1", sessions.Prompt); strings.Contains(r.Context, "compacted") {
		t.Fatalf("told twice: %q", r.Context)
	}
}

func TestTargetIsToldAfterAnInterrupt(t *testing.T) {
	e := newEnv(t)
	e.terminalSession(t, "pane-7")
	e.report(t, "session-1", sessions.Prompt)
	tg, _ := e.s.PrepareInterrupt(ctx, allCommands, "reviewer", "builder")
	if err := e.s.FinishInterrupt(ctx, "reviewer", "builder", tg); err != nil {
		t.Fatal(err)
	}
	if r := e.report(t, "session-1", sessions.Prompt); !strings.Contains(r.Context, "reviewer interrupted your turn") {
		t.Fatalf("prompt: %q", r.Context)
	}
	if r := e.report(t, "session-1", sessions.Prompt); strings.Contains(r.Context, "interrupted") {
		t.Fatalf("told twice: %q", r.Context)
	}
}

func TestWakeCommandWaitsAfterTheCompactCommand(t *testing.T) {
	e := newEnv(t)
	e.terminalSession(t, "pane-7")
	e.report(t, "session-1", sessions.Stop)
	if _, err := e.s.Compact(ctx, allCommands, "reviewer", "builder", ""); err != nil {
		t.Fatal(err)
	}
	e.claim(0) //nolint:errcheck // only the time it ran matters here
	e.clock.add(30 * time.Second)
	if idle, _ := e.s.IdleWithTerminal(ctx, 0); len(idle) != 0 {
		t.Fatalf("30 seconds after the compact command: %+v", idle)
	}
	e.clock.add(sessions.AfterCompact)
	if idle, _ := e.s.IdleWithTerminal(ctx, 0); len(idle) != 1 {
		t.Fatalf("after the gap: %+v", idle)
	}
}

func TestRenamedRequesterIsNamedByItsNewName(t *testing.T) {
	e := newEnv(t)
	e.terminalSession(t, "pane-7")
	if _, err := e.s.Compact(ctx, allCommands, "reviewer", "builder", ""); err != nil {
		t.Fatal(err)
	}
	e.rename(t, "reviewer", "checker")
	q, _ := e.claim(0)
	if len(q) != 1 || q[0].By != "checker" {
		t.Fatalf("claimed %+v", q)
	}
	if err := e.s.FinishCompact(ctx, q[0].ID, true, "ok"); err != nil {
		t.Fatal(err)
	}
	got := e.notices(t, "general")
	if last := got[len(got)-1]; last != "checker compacted builder" {
		t.Fatalf("notices %q", got)
	}
}

func TestFocusRejectsUnicodeLineSeparators(t *testing.T) {
	for _, focus := range []string{"a b", "a b", "a\x00b", "a\xffb"} {
		if err := sessions.CheckFocus(focus); !errors.Is(err, store.ErrInvalid) {
			t.Fatalf("focus %q: %v", focus, err)
		}
	}
	if err := sessions.CheckFocus("keep the schema, ünïcode ok"); err != nil {
		t.Fatal(err)
	}
}

func TestCompactIsDroppedWhenTheAgentLeftOrTheNameMoved(t *testing.T) {
	e := newEnv(t)
	e.terminalSession(t, "pane-7")
	if _, err := e.s.Compact(ctx, allCommands, "reviewer", "builder", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Leave(ctx, "builder"); err != nil {
		t.Fatal(err)
	}
	if c, _ := e.s.CompactCandidates(ctx, 0, alive); len(c) != 0 {
		t.Fatalf("candidate after the agent left: %+v", c)
	}
	if q, _ := e.claim(0); len(q) != 0 {
		t.Fatalf("claimed after the agent left: %+v", q)
	}
	if err := e.s.DropEndedCompacts(ctx); err != nil {
		t.Fatal(err)
	}
	got := e.notices(t, "general")
	if last := got[len(got)-1]; !strings.Contains(last, "dropped") || !strings.Contains(last, "compact of builder") {
		t.Fatalf("notices %q", got)
	}

	// the name moves to another session; someone else joins the first one
	e = newEnv(t)
	e.terminalSession(t, "pane-7")
	if _, err := e.s.Compact(ctx, allCommands, "reviewer", "builder", ""); err != nil {
		t.Fatal(err)
	}
	e.moveName(t)
	if c, _ := e.s.CompactCandidates(ctx, 0, alive); len(c) != 0 {
		t.Fatalf("candidate for the other agent's session: %+v", c)
	}
	if q, _ := e.claim(0); len(q) != 0 {
		t.Fatalf("typed into the other agent's session: %+v", q)
	}
	if err := e.s.DropEndedCompacts(ctx); err != nil {
		t.Fatal(err)
	}
	got = e.notices(t, "general")
	if last := got[len(got)-1]; !strings.Contains(last, "dropped") || strings.Contains(last, "compacted") {
		t.Fatalf("notices %q", got)
	}
}

func TestCompactDoesNotRunForASessionJustWoken(t *testing.T) {
	e := newEnv(t)
	e.terminalSession(t, "pane-7")
	e.report(t, "session-1", sessions.Stop)
	e.clock.add(time.Minute)
	if _, err := e.s.Compact(ctx, allCommands, "reviewer", "builder", ""); err != nil {
		t.Fatal(err)
	}
	if err := e.s.RecordWake(ctx, "session-1", "m1", "ok", true); err != nil {
		t.Fatal(err)
	}
	e.clock.add(5 * time.Second)
	if q, _ := e.claim(10 * time.Second); len(q) != 0 {
		t.Fatalf("claimed 5 seconds after a wake: %+v", q)
	}
	e.clock.add(6 * time.Second)
	if q, _ := e.claim(10 * time.Second); len(q) != 1 {
		t.Fatalf("not claimed after the settle time: %+v", q)
	}
}

func TestCompactNeedsARunningProcess(t *testing.T) {
	e := newEnv(t)
	e.terminalSession(t, "pane-7")
	if _, err := e.s.Compact(ctx, allCommands, "reviewer", "builder", ""); err != nil {
		t.Fatal(err)
	}
	if q, _ := e.claimAlive(0, func(int, int64) bool { return false }); len(q) != 0 {
		t.Fatalf("claimed for a dead process: %+v", q)
	}
	if q, _ := e.claim(0); len(q) != 1 {
		t.Fatalf("not claimed for a live process: %+v", q)
	}
}

func TestNotesWaitForAPromptToolUseOrStart(t *testing.T) {
	e := newEnv(t)
	e.terminalSession(t, "pane-7")
	e.report(t, "session-1", sessions.Prompt)
	tg, _ := e.s.PrepareInterrupt(ctx, allCommands, "reviewer", "builder")
	if err := e.s.FinishInterrupt(ctx, "reviewer", "builder", tg); err != nil {
		t.Fatal(err)
	}
	for _, ev := range []sessions.Event{sessions.Stop, sessions.Waiting} {
		if r := e.report(t, "session-1", ev); strings.Contains(r.Context+r.BlockReason, "interrupted") {
			t.Fatalf("%s showed the note", ev)
		}
	}
	if r := e.report(t, "session-1", sessions.Tool); !strings.Contains(r.Context, "reviewer interrupted your turn") {
		t.Fatalf("the note was used up by a stop: %q", r.Context)
	}
}

func TestNoteIsNotToldToAnotherAgentOfTheSession(t *testing.T) {
	e := newEnv(t)
	e.terminalSession(t, "pane-7")
	e.report(t, "session-1", sessions.Prompt)
	tg, _ := e.s.PrepareInterrupt(ctx, allCommands, "reviewer", "builder")
	if err := e.s.FinishInterrupt(ctx, "reviewer", "builder", tg); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Leave(ctx, "builder"); err != nil {
		t.Fatal(err)
	}
	e.join(t, "other", "session-1")
	if r := e.report(t, "session-1", sessions.Prompt); strings.Contains(r.Context, "interrupted") {
		t.Fatalf("told the new agent: %q", r.Context)
	}
}

func TestNoWakeWindowEndsAfterExactlyTwoMinutes(t *testing.T) {
	e := newEnv(t)
	e.terminalSession(t, "pane-7")
	e.report(t, "session-1", sessions.Stop)
	if _, err := e.s.Compact(ctx, allCommands, "reviewer", "builder", ""); err != nil {
		t.Fatal(err)
	}
	e.claim(0) //nolint:errcheck,gosec // only the time it ran matters here
	e.clock.add(2*time.Minute - time.Second)
	if idle, _ := e.s.IdleWithTerminal(ctx, 0); len(idle) != 0 {
		t.Fatalf("1m59 after the compact command: %+v", idle)
	}
	e.clock.add(2 * time.Second)
	if idle, _ := e.s.IdleWithTerminal(ctx, 0); len(idle) != 1 {
		t.Fatalf("2m01 after the compact command: %+v", idle)
	}
}

func TestClaimCompactDecidesAgainWhenItClaims(t *testing.T) {
	settle := 10 * time.Second
	cases := map[string]func(e env, t *testing.T){
		"woken in between": func(e env, t *testing.T) { _ = e.s.RecordWake(ctx, "session-1", "m1", "ok", true) },
		"busy in between":  func(e env, t *testing.T) { e.report(t, "session-1", sessions.Prompt) },
		"turn ended again": func(e env, t *testing.T) {
			e.report(t, "session-1", sessions.Prompt)
			e.report(t, "session-1", sessions.Stop)
		},
		"agent left": func(e env, t *testing.T) { e.leave(t, "builder") },
		"name moved": func(e env, t *testing.T) {
			e.moveName(t)
		},
		"nothing in between": nil,
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			e.terminalSession(t, "pane-7")
			e.report(t, "session-1", sessions.Stop)
			e.clock.add(time.Minute)
			if _, err := e.s.Compact(ctx, allCommands, "reviewer", "builder", ""); err != nil {
				t.Fatal(err)
			}
			cands, err := e.s.CompactCandidates(ctx, settle, alive)
			if err != nil || len(cands) != 1 {
				t.Fatalf("candidates %+v %v", cands, err)
			}
			if change != nil {
				change(e, t)
			}
			ok, err := e.s.ClaimCompact(ctx, cands[0].ID, settle)
			if err != nil || ok != (change == nil) {
				t.Fatalf("claimed %v %v", ok, err)
			}
		})
	}
}

func TestWakeStillDueDecidesAgain(t *testing.T) {
	settle := 10 * time.Second
	cases := map[string]func(e env, t *testing.T){
		"compact ran":     func(e env, t *testing.T) { compactNow(e, t) },
		"woken meanwhile": func(e env, t *testing.T) { _ = e.s.RecordWake(ctx, "session-1", "m9", "ok", true) },
		"busy meanwhile":  func(e env, t *testing.T) { e.report(t, "session-1", sessions.Prompt) },
		"idle again": func(e env, t *testing.T) {
			e.report(t, "session-1", sessions.Prompt)
			e.report(t, "session-1", sessions.Stop)
		},
		"agent left": func(e env, t *testing.T) { e.leave(t, "builder") },
		"name moved": func(e env, t *testing.T) {
			e.moveName(t)
		},
		"nothing changed": nil,
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			e.terminalSession(t, "pane-7")
			e.report(t, "session-1", sessions.Stop)
			e.clock.add(time.Minute)
			idle, err := e.s.IdleWithTerminal(ctx, settle)
			if err != nil || len(idle) != 1 {
				t.Fatalf("idle %+v %v", idle, err)
			}
			if change != nil {
				change(e, t)
			}
			due, err := e.s.WakeStillDue(ctx, idle[0], settle)
			if err != nil || due != (change == nil) {
				t.Fatalf("due %v %v", due, err)
			}
		})
	}
}

// compactNow queues a compact and runs it, as the hub does.
func compactNow(e env, t *testing.T) {
	t.Helper()
	if _, err := e.s.Compact(ctx, allCommands, "reviewer", "builder", ""); err != nil {
		t.Fatal(err)
	}
	if q, _ := e.claim(0); len(q) != 1 {
		t.Fatalf("claimed %+v", q)
	}
}

// moveName makes builder take over session-2, a session of another process, and gives its old
// session, which stays live, to someone else.
func (e env) moveName(t *testing.T) {
	t.Helper()
	if _, err := e.s.Report(ctx, sessions.Report{SessionID: "session-2", Kind: "claude-code", Event: sessions.Start, PID: 4343, PIDStart: 112, Terminal: "pane-7"}); err != nil {
		t.Fatal(err)
	}
	if bound, err := e.s.Join(ctx, "builder", "session-2", true); err != nil || !bound {
		t.Fatalf("builder takes session-2: %v %v", bound, err)
	}
	e.join(t, "other", "session-1")
	if list, _ := e.s.List(ctx); len(list) != 2 {
		t.Fatalf("both sessions stay live: %+v", list)
	}
}

func (e env) leave(t *testing.T, name string) {
	t.Helper()
	if _, err := e.s.Leave(ctx, name); err != nil {
		t.Fatal(err)
	}
}
