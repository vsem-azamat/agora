package hub_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"

	agorav1 "github.com/vsem-azamat/agora/gen/agora/v1"
	"github.com/vsem-azamat/agora/internal/hub"
	"github.com/vsem-azamat/agora/internal/proc"
)

func (r *running) report(t *testing.T, session string, ev agorav1.SessionEvent) {
	t.Helper()
	if _, err := r.sessions.Report(context.Background(), connect.NewRequest(&agorav1.ReportRequest{SessionId: session, Event: ev})); err != nil {
		t.Fatal(err)
	}
}

func (r *running) compact(by, agent, focus string) error {
	_, err := r.sessions.Compact(context.Background(), connect.NewRequest(&agorav1.CompactRequest{Agent: by, Target: agent, Focus: focus}))
	return err
}

func (r *running) interrupt(by, agent string) error {
	_, err := r.sessions.Interrupt(context.Background(), connect.NewRequest(&agorav1.InterruptRequest{Agent: by, Target: agent}))
	return err
}

func (r *running) boardNotices(t *testing.T) []string {
	t.Helper()
	resp, err := r.rooms.History(context.Background(), connect.NewRequest(&agorav1.HistoryRequest{Room: "general", Last: 50}))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, m := range resp.Msg.GetMessages() {
		if m.GetAuthor() == "agora" {
			out = append(out, m.GetBody())
		}
	}
	return out
}

func TestCompactCommandRunsForAnIdleSessionWithTheTextAndTerminal(t *testing.T) {
	log := filepath.Join(shortDir(t), "compacts")
	var h *hub.Hub
	r := startWith(t, func(x *hub.Hub) {
		x.CompactCommand = `printf '%s|%s\n' "$AGORA_TERMINAL" "$AGORA_WAKE_TEXT" >> ` + log
		x.WakeSettle = 0
		hub.SetControlEvery(x, time.Hour)
		h = x
	})
	r.idleAgent(t, "builder", "session-1", "pane-7")
	r.post(t, "reviewer", "hello")
	r.report(t, "session-1", agorav1.SessionEvent_SESSION_EVENT_PROMPT)
	if err := r.compact("reviewer", "builder", "keep the schema"); err != nil {
		t.Fatal(err)
	}
	if err := hub.RunCompacts(context.Background(), h); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(log); len(b) != 0 {
		t.Fatalf("ran for a busy session: %q", b)
	}
	r.report(t, "session-1", agorav1.SessionEvent_SESSION_EVENT_STOP)
	for range 2 { // the second round must not repeat it
		if err := hub.RunCompacts(context.Background(), h); err != nil {
			t.Fatal(err)
		}
	}
	if b, _ := os.ReadFile(log); string(b) != "pane-7|/compact keep the schema\n" {
		t.Fatalf("ran %q", b)
	}
	if got := r.boardNotices(t); len(got) != 2 || got[1] != "reviewer compacted builder" {
		t.Fatalf("notices %q", got)
	}
}

func TestFailedCompactCommandIsAnnouncedWithItsOutput(t *testing.T) {
	var h *hub.Hub
	r := startWith(t, func(x *hub.Hub) {
		x.CompactCommand = `echo no such pane >&2; exit 3`
		x.WakeSettle = 0
		hub.SetControlEvery(x, time.Hour)
		h = x
	})
	r.idleAgent(t, "builder", "session-1", "pane-7")
	r.post(t, "reviewer", "hello")
	if err := r.compact("reviewer", "builder", ""); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := hub.RunCompacts(context.Background(), h); err != nil {
			t.Fatal(err)
		}
	}
	got := r.boardNotices(t)
	if len(got) != 2 || !strings.Contains(got[1], "failed") || !strings.Contains(got[1], "no such pane") {
		t.Fatalf("notices %q", got)
	}
}

func TestCompactIsRefusedWithoutTheCommandOrTerminal(t *testing.T) {
	r := startWith(t, func(x *hub.Hub) { x.CompactCommand = "true" })
	r.idleAgent(t, "builder", "session-1", "")
	r.post(t, "reviewer", "hello")
	err := r.compact("reviewer", "builder", "")
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "no terminal") {
		t.Fatalf("no terminal: %v", err)
	}
	r.idleAgent(t, "other", "session-2", "pane-2")
	err = r.interrupt("reviewer", "other")
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "--interrupt-command") {
		t.Fatalf("no command: %v", err)
	}
}

func TestInterruptRunsTheCommandForABusySession(t *testing.T) {
	log := filepath.Join(shortDir(t), "interrupts")
	r := startWith(t, func(x *hub.Hub) {
		x.InterruptCommand = `printf '%s\n' "$AGORA_TERMINAL" >> ` + log
	})
	r.idleAgent(t, "builder", "session-1", "pane-7")
	r.post(t, "reviewer", "hello")
	if err := r.interrupt("reviewer", "builder"); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("idle session: %v", err)
	}
	r.report(t, "session-1", agorav1.SessionEvent_SESSION_EVENT_PROMPT)
	if err := r.interrupt("reviewer", "builder"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(log); string(b) != "pane-7\n" {
		t.Fatalf("ran %q", b)
	}
	if got := r.boardNotices(t); len(got) != 1 || got[0] != "reviewer interrupted builder" {
		t.Fatalf("notices %q", got)
	}
	if err := r.interrupt("reviewer", "builder"); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("interrupted session is idle: %v", err)
	}
}

func TestFailedInterruptChangesNothing(t *testing.T) {
	r := startWith(t, func(x *hub.Hub) { x.InterruptCommand = `echo stuck >&2; exit 1` })
	r.idleAgent(t, "builder", "session-1", "pane-7")
	r.post(t, "reviewer", "hello")
	r.report(t, "session-1", agorav1.SessionEvent_SESSION_EVENT_PROMPT)
	err := r.interrupt("reviewer", "builder")
	if err == nil || !strings.Contains(err.Error(), "stuck") {
		t.Fatalf("error %v", err)
	}
	if got := r.boardNotices(t); len(got) != 0 {
		t.Fatalf("notices %q", got)
	}
	list, err := r.sessions.ListSessions(context.Background(), connect.NewRequest(&agorav1.ListSessionsRequest{}))
	if err != nil || list.Msg.GetSessions()[0].GetState() != agorav1.SessionState_SESSION_STATE_BUSY {
		t.Fatalf("state %v %v", list, err)
	}
}

func TestSessionListShowsWhatEachCanDo(t *testing.T) {
	r := startWith(t, func(x *hub.Hub) { x.CompactCommand = "true" })
	r.idleAgent(t, "builder", "session-1", "pane-7")
	list, err := r.sessions.ListSessions(context.Background(), connect.NewRequest(&agorav1.ListSessionsRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if c := list.Msg.GetSessions()[0].GetCapabilities(); c.GetWake() != agorav1.SessionMechanism_SESSION_MECHANISM_HOOK || c.GetCompact() != agorav1.SessionMechanism_SESSION_MECHANISM_TERMINAL || c.GetInterrupt() != agorav1.SessionMechanism_SESSION_MECHANISM_NONE {
		t.Fatalf("capabilities %v", c)
	}
}

func TestFailedCompactOutputIsValidUTF8(t *testing.T) {
	var h *hub.Hub
	r := startWith(t, func(x *hub.Hub) {
		// 799 bytes: the cut of the output's tail falls inside a character
		x.CompactCommand = `printf x >&2; yes é | head -n 399 | tr -d '\n' >&2; printf '\377\376' >&2; exit 1`
		x.WakeSettle = 0
		hub.SetControlEvery(x, time.Hour)
		h = x
	})
	r.idleAgent(t, "builder", "session-1", "pane-7")
	r.post(t, "reviewer", "hello")
	if err := r.compact("reviewer", "builder", ""); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := hub.RunCompacts(context.Background(), h); err != nil {
			t.Fatal(err)
		}
	}
	if got := r.boardNotices(t); len(got) != 2 || !strings.Contains(got[1], "failed") { // History fails on invalid text
		t.Fatalf("notices %q", got)
	}
	if _, err := r.sessions.Report(context.Background(), connect.NewRequest(&agorav1.ReportRequest{SessionId: "session-1", Event: agorav1.SessionEvent_SESSION_EVENT_PROMPT})); err != nil {
		t.Fatalf("report: %v", err)
	}
}

func TestCompactLeftRunningByAStoppedHubIsFailedAtStart(t *testing.T) {
	r := startPrepared(t, func(db *sql.DB) {
		for _, q := range []string{
			`INSERT INTO agents (name, joined_at) VALUES ('builder', 1), ('reviewer', 1)`,
			`INSERT INTO sessions (id, kind, agent, state, state_at, started_at, seen_at, terminal, pid) VALUES ('session-1', 'claude-code', 'builder', 'idle', 1, 1, 1, 'pane-7', ` + strconv.Itoa(os.Getpid()) + `)`,
			`INSERT INTO session_controls (session_id, agent, action, requested_by, requested_at, state) VALUES ('session-1', 'builder', 'compact', 'reviewer', 1, 'running')`,
		} {
			if _, err := db.Exec(q); err != nil {
				t.Fatal(err)
			}
		}
	}, nil)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if got := r.boardNotices(t); len(got) == 1 && strings.Contains(got[0], "failed") && strings.Contains(got[0], "hub stopped") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("notices %q", r.boardNotices(t))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestEndedSessionsCompactIsDroppedByTheNextRound(t *testing.T) {
	log := filepath.Join(shortDir(t), "compacts")
	var h *hub.Hub
	r := startWith(t, func(x *hub.Hub) {
		x.CompactCommand = `echo ran >> ` + log
		x.WakeSettle = 0
		hub.SetControlEvery(x, time.Hour)
		h = x
	})
	r.idleAgent(t, "builder", "session-1", "pane-7")
	r.post(t, "reviewer", "hello")
	r.report(t, "session-1", agorav1.SessionEvent_SESSION_EVENT_PROMPT)
	if err := r.compact("reviewer", "builder", ""); err != nil {
		t.Fatal(err)
	}
	r.report(t, "session-1", agorav1.SessionEvent_SESSION_EVENT_END)
	if err := hub.RunCompacts(context.Background(), h); err != nil {
		t.Fatal(err)
	}
	if got := r.boardNotices(t); len(got) != 2 || !strings.Contains(got[1], "dropped") {
		t.Fatalf("notices %q", got)
	}
	if b, _ := os.ReadFile(log); len(b) != 0 {
		t.Fatalf("ran for an ended session: %q", b)
	}
}

func TestCompactWaitsTheSettleTimeAfterTheSessionGoesIdle(t *testing.T) {
	log := filepath.Join(shortDir(t), "compacts")
	var h *hub.Hub
	r := startWith(t, func(x *hub.Hub) {
		x.CompactCommand = `echo ran >> ` + log
		hub.SetControlEvery(x, time.Hour)
		h = x
	})
	r.idleAgent(t, "builder", "session-1", "pane-7") // idle since now
	r.post(t, "reviewer", "hello")
	if err := r.compact("reviewer", "builder", ""); err != nil {
		t.Fatal(err)
	}
	r.clock.add(9 * time.Second)
	if err := hub.RunCompacts(context.Background(), h); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(log); len(b) != 0 {
		t.Fatalf("ran after 9 seconds idle: %q", b)
	}
	r.clock.add(time.Second)
	if err := hub.RunCompacts(context.Background(), h); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(log); string(b) != "ran\n" {
		t.Fatalf("ran %q", b)
	}
}

func TestControlCommandsDoNotRunForADeadProcess(t *testing.T) {
	log := filepath.Join(shortDir(t), "ran")
	var h *hub.Hub
	r := startWith(t, func(x *hub.Hub) {
		x.CompactCommand = `echo compact >> ` + log
		x.InterruptCommand = `echo interrupt >> ` + log
		x.WakeSettle = 0
		hub.SetControlEvery(x, time.Hour)
		h = x
	})
	r.idleAgentWithPID(t, "builder", "session-1", "pane-7", 999999) // no such process
	r.post(t, "reviewer", "hello")
	// the session reports a process that is gone; it is still listed until the sweep ends it
	if err := r.compact("reviewer", "builder", ""); err != nil {
		t.Fatal(err)
	}
	if err := hub.RunCompacts(context.Background(), h); err != nil {
		t.Fatal(err)
	}
	r.report(t, "session-1", agorav1.SessionEvent_SESSION_EVENT_PROMPT)
	if err := r.interrupt("reviewer", "builder"); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("interrupt: %v", err)
	}
	if b, _ := os.ReadFile(log); len(b) != 0 {
		t.Fatalf("ran for a dead process: %q", b)
	}
}

func TestOneCommandTypesIntoATerminalAtATime(t *testing.T) {
	log := filepath.Join(shortDir(t), "log")
	slow := func(name string) string {
		return `echo ` + name + `-start >> ` + log + `; sleep 0.4; echo ` + name + `-end >> ` + log
	}
	var h *hub.Hub
	r := startWith(t, func(x *hub.Hub) {
		x.WakeCommand = slow("wake")
		x.CompactCommand = slow("compact")
		x.WakeSettle = 0
		hub.SetControlEvery(x, time.Hour)
		h = x
	})
	r.idleAgent(t, "builder", "session-1", "pane-7")
	r.post(t, "reviewer", "@builder ping")
	if err := r.compact("reviewer", "builder", ""); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); _ = hub.WakeByCommand(context.Background(), h) }()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if b, _ := os.ReadFile(log); strings.Contains(string(b), "wake-start") {
			break // the wake types now; the compact must wait for it
		}
	}
	_ = hub.RunCompacts(context.Background(), h)
	wg.Wait()
	b, _ := os.ReadFile(log)
	got := strings.Fields(string(b))
	ok := len(got) == 4 && strings.TrimSuffix(got[0], "-start") == strings.TrimSuffix(got[1], "-end") && strings.TrimSuffix(got[2], "-start") == strings.TrimSuffix(got[3], "-end")
	if !ok {
		t.Fatalf("commands interleaved or missing: %v", got)
	}
}

func TestTwoInterruptsSendOneEsc(t *testing.T) {
	log := filepath.Join(shortDir(t), "esc")
	r := startWith(t, func(x *hub.Hub) { x.InterruptCommand = `echo esc >> ` + log + `; sleep 0.5` })
	r.idleAgent(t, "builder", "session-1", "pane-7")
	r.post(t, "reviewer", "hello")
	r.post(t, "tester", "hello")
	r.report(t, "session-1", agorav1.SessionEvent_SESSION_EVENT_PROMPT)
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, by := range []string{"reviewer", "tester"} { // two callers, one target
		wg.Add(1)
		go func() { defer wg.Done(); errs[i] = r.interrupt(by, "builder") }()
	}
	wg.Wait()
	if b, _ := os.ReadFile(log); string(b) != "esc\n" {
		t.Fatalf("Esc sent %q (errors %v)", b, errs)
	}
	if (errs[0] == nil) == (errs[1] == nil) {
		t.Fatalf("exactly one interrupt succeeds: %v", errs)
	}
}

// logHas waits until the log holds text.
func logHas(t *testing.T, log, text string) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if b, _ := os.ReadFile(log); strings.Contains(string(b), text) {
			return
		}
	}
	t.Fatalf("%q never reached the log", text)
}

func slowCommand(log, name string, seconds string) string {
	return `echo ` + name + `-start >> ` + log + `; sleep ` + seconds + `; echo ` + name + `-end >> ` + log
}

// holdCommand logs its start, waits until the release file exists and logs its end.
func holdCommand(log, name, release string) string {
	return `echo ` + name + `-start >> ` + log + `; while [ ! -e ` + release + ` ]; do sleep 0.02; done; echo ` + name + `-end >> ` + log
}

func releaseNow(t *testing.T, release string) {
	t.Helper()
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

// waiting waits until n commands wait for the terminal's lock.
func waiting(t *testing.T, h *hub.Hub, terminal string, n int) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if hub.TerminalWaiting(h, terminal) == n {
			return
		}
	}
	t.Fatalf("%d commands never waited for %s", n, terminal)
}

func fields(log string) string {
	b, _ := os.ReadFile(log)
	return strings.Join(strings.Fields(string(b)), " ")
}

// twoSessions has builder idle with a mention pending and other busy, on one terminal.
func (r *running) twoSessions(t *testing.T, mention bool) {
	t.Helper()
	r.idleAgent(t, "builder", "session-1", "pane-7")
	r.idleAgentWithPID(t, "other", "session-2", "pane-7", int32(os.Getppid()))
	if mention {
		r.post(t, "reviewer", "@builder ping")
	} else {
		r.post(t, "reviewer", "hello")
	}
	r.report(t, "session-2", agorav1.SessionEvent_SESSION_EVENT_PROMPT)
}

func TestInterruptHoldsTheTerminalAndACompactBeforeAWakeSkipsIt(t *testing.T) {
	dir := shortDir(t)
	log, release := filepath.Join(dir, "log"), filepath.Join(dir, "release")
	var h *hub.Hub
	r := startWith(t, func(x *hub.Hub) {
		x.WakeCommand = slowCommand(log, "wake", "0")
		x.CompactCommand = slowCommand(log, "compact", "0")
		x.InterruptCommand = holdCommand(log, "interrupt", release)
		x.WakeSettle = 0
		hub.SetControlEvery(x, time.Hour)
		h = x
	})
	r.twoSessions(t, true)
	if err := r.compact("reviewer", "builder", ""); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); _ = r.interrupt("reviewer", "other") }()
	logHas(t, log, "interrupt-start") // the interrupt holds the terminal
	go func() { defer wg.Done(); _ = hub.RunCompacts(context.Background(), h) }()
	waiting(t, h, "pane-7", 1) // the compact is first in line
	go func() { defer wg.Done(); _ = hub.WakeByCommand(context.Background(), h) }()
	waiting(t, h, "pane-7", 2)
	releaseNow(t, release)
	wg.Wait()
	if got := fields(log); got != "interrupt-start interrupt-end compact-start compact-end" {
		t.Fatalf("log %q: the commands interleaved, or the wake typed into the compaction", got)
	}
}

// TestWakeHoldsTheTerminalUntilItIsRecorded takes the hub's only database connection while the
// wake command runs, so recording the wake cannot finish: the terminal must stay held until it
// does, or a compact claimed meanwhile would not see the wake.
func TestWakeHoldsTheTerminalUntilItIsRecorded(t *testing.T) {
	log := filepath.Join(shortDir(t), "log")
	var h *hub.Hub
	r := startWith(t, func(x *hub.Hub) {
		x.WakeCommand = slowCommand(log, "wake", "0.2")
		hub.SetControlEvery(x, time.Hour)
		h = x
	})
	r.idleAgent(t, "builder", "session-1", "pane-7")
	r.post(t, "reviewer", "@builder ping")
	r.clock.add(11 * time.Second)
	done := make(chan struct{})
	go func() { defer close(done); _ = hub.WakeByCommand(context.Background(), h) }()
	logHas(t, log, "wake-start")
	conn, err := r.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	logHas(t, log, "wake-end")
	freed := false
	for deadline := time.Now().Add(300 * time.Millisecond); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if !hub.TerminalHeld(h, "pane-7") {
			freed = true
			break
		}
	}
	conn.Close()
	<-done
	if freed {
		t.Fatal("the terminal was free before the wake was recorded")
	}
}

// processes makes the hub see the process of builder (this test process) as gone once kill is called.
func processes() (alive func(int, int64) bool, kill func()) {
	var dead atomic.Bool
	return func(pid int, _ int64) bool { return pid != os.Getpid() || !dead.Load() }, func() { dead.Store(true) }
}

func TestCommandsRecheckTheProcessUnderTheTerminalLock(t *testing.T) {
	for _, name := range []string{"wake", "compact", "interrupt"} {
		t.Run(name, func(t *testing.T) {
			dir := shortDir(t)
			log, release := filepath.Join(dir, "log"), filepath.Join(dir, "release")
			alive, kill := processes()
			var h *hub.Hub
			r := startWith(t, func(x *hub.Hub) {
				x.WakeCommand = `echo wake >> ` + log
				x.CompactCommand = `echo compact >> ` + log
				x.InterruptCommand = holdCommand(log, "esc", release)
				x.WakeSettle = 0
				hub.SetControlEvery(x, time.Hour)
				hub.SetAlive(x, alive)
				h = x
			})
			r.twoSessions(t, name == "wake")
			var wg sync.WaitGroup
			wg.Add(2)
			go func() { defer wg.Done(); _ = r.interrupt("reviewer", "other") }()
			logHas(t, log, "esc-start")
			switch name {
			case "wake":
				go func() { defer wg.Done(); _ = hub.WakeByCommand(context.Background(), h) }()
			case "compact":
				if err := r.compact("reviewer", "builder", ""); err != nil {
					t.Fatal(err)
				}
				go func() { defer wg.Done(); _ = hub.RunCompacts(context.Background(), h) }()
			case "interrupt":
				r.report(t, "session-1", agorav1.SessionEvent_SESSION_EVENT_PROMPT)
				go func() { defer wg.Done(); _ = r.interrupt("reviewer", "builder") }()
			}
			waiting(t, h, "pane-7", 1)
			kill() // builder's process exits while its command waits for the terminal
			releaseNow(t, release)
			wg.Wait()
			if got := fields(log); got != "esc-start esc-end" {
				t.Fatalf("log %q: typed into a session whose process had exited", got)
			}
		})
	}
}

func TestInterruptThatWaitedForTheTerminalChecksTheTurnAgain(t *testing.T) {
	changes := map[string]func(r *running, t *testing.T){
		"turn ended": func(r *running, t *testing.T) { r.report(t, "session-1", agorav1.SessionEvent_SESSION_EVENT_STOP) },
		"new turn began": func(r *running, t *testing.T) {
			r.report(t, "session-1", agorav1.SessionEvent_SESSION_EVENT_STOP)
			r.report(t, "session-1", agorav1.SessionEvent_SESSION_EVENT_PROMPT)
		},
		"name moved": func(r *running, t *testing.T) {
			// a new conversation in the same process takes the name; its turn counts the same
			if _, err := r.sessions.Report(context.Background(), connect.NewRequest(&agorav1.ReportRequest{
				SessionId: "session-3", Kind: "claude-code", Event: agorav1.SessionEvent_SESSION_EVENT_START,
				Pid: int32(os.Getpid()), PidStart: proc.StartTime(os.Getpid()), Terminal: "pane-7",
			})); err != nil {
				t.Fatal(err)
			}
			r.report(t, "session-3", agorav1.SessionEvent_SESSION_EVENT_PROMPT)
		},
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			dir := shortDir(t)
			log, release := filepath.Join(dir, "log"), filepath.Join(dir, "release")
			var h *hub.Hub
			r := startWith(t, func(x *hub.Hub) {
				x.InterruptCommand = holdCommand(log, "esc", release)
				h = x
			})
			r.twoSessions(t, false)
			r.report(t, "session-1", agorav1.SessionEvent_SESSION_EVENT_PROMPT)
			var wg sync.WaitGroup
			wg.Add(2)
			go func() { defer wg.Done(); _ = r.interrupt("reviewer", "other") }()
			logHas(t, log, "esc-start")
			var err error
			go func() { defer wg.Done(); err = r.interrupt("reviewer", "builder") }() // waits for the terminal
			waiting(t, h, "pane-7", 1)
			change(r, t)
			releaseNow(t, release)
			wg.Wait()
			if connect.CodeOf(err) != connect.CodeFailedPrecondition {
				t.Fatalf("interrupt of a turn that ended: %v", err)
			}
			if got := fields(log); got != "esc-start esc-end" {
				t.Fatalf("Esc sent into the wrong turn: %q", got)
			}
			for _, n := range r.boardNotices(t) {
				if strings.Contains(n, "interrupted builder") {
					t.Fatalf("announced: %q", r.boardNotices(t))
				}
			}
		})
	}
}

func TestInterruptIsAnnouncedWhenTheCallerGoesAway(t *testing.T) {
	log := filepath.Join(shortDir(t), "esc")
	r := startWith(t, func(x *hub.Hub) { x.InterruptCommand = `sleep 0.5; echo esc >> ` + log })
	r.idleAgent(t, "builder", "session-1", "pane-7")
	r.post(t, "reviewer", "hello")
	r.report(t, "session-1", agorav1.SessionEvent_SESSION_EVENT_PROMPT)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	_, err := r.sessions.Interrupt(ctx, connect.NewRequest(&agorav1.InterruptRequest{Agent: "reviewer", Target: "builder"}))
	if err == nil {
		t.Fatal("the call outlived its context")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if got := r.boardNotices(t); len(got) == 1 && got[0] == "reviewer interrupted builder" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("not announced: %q", r.boardNotices(t))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestCompactBehindAWakeIsNotClaimedBeforeTheSettleTime pins ClaimCompact's own use of the settle
// time: the compact waited for the terminal behind a wake, was a candidate before the wake was
// recorded, and must not be typed right after it.
func TestCompactBehindAWakeIsNotClaimedBeforeTheSettleTime(t *testing.T) {
	dir := shortDir(t)
	log, release := filepath.Join(dir, "log"), filepath.Join(dir, "release")
	var h *hub.Hub
	r := startWith(t, func(x *hub.Hub) {
		x.WakeCommand = holdCommand(log, "wake", release)
		x.CompactCommand = `echo compact >> ` + log
		hub.SetControlEvery(x, time.Hour)
		h = x
	})
	r.idleAgent(t, "builder", "session-1", "pane-7")
	r.post(t, "reviewer", "@builder ping")
	r.clock.add(11 * time.Second)
	if err := r.compact("reviewer", "builder", ""); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _ = hub.WakeByCommand(context.Background(), h) }()
	logHas(t, log, "wake-start")
	go func() { defer wg.Done(); _ = hub.RunCompacts(context.Background(), h) }()
	waiting(t, h, "pane-7", 1)
	releaseNow(t, release)
	wg.Wait()
	if got := fields(log); got != "wake-start wake-end" {
		t.Fatalf("log %q: a compact was typed right after a wake", got)
	}
}
