package hub

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"connectrpc.com/connect"

	agorav1 "github.com/vsem-azamat/agora/gen/agora/v1"
)

// waiter tracks the connector waits of one session: only the newest generation stays.
type waiter struct {
	gen    int
	active int
}

// startWait registers a connector wait for a session and returns its generation and a release
// function.
func (h *Hub) startWait(sessionID string) (int, func()) {
	h.waitMu.Lock()
	defer h.waitMu.Unlock()
	w := h.waiters[sessionID]
	if w == nil {
		w = &waiter{}
		h.waiters[sessionID] = w
	}
	w.gen++
	w.active++
	gen := w.gen
	return gen, func() {
		h.waitMu.Lock()
		defer h.waitMu.Unlock()
		if w.active--; w.active == 0 {
			delete(h.waiters, sessionID)
		}
	}
}

func (h *Hub) currentWait(sessionID string) (gen, active int) {
	h.waitMu.Lock()
	defer h.waitMu.Unlock()
	if w := h.waiters[sessionID]; w != nil {
		return w.gen, w.active
	}
	return 0, 0
}

func (s *sessionService) WaitWake(ctx context.Context, req *connect.Request[agorav1.WaitWakeRequest], stream *connect.ServerStream[agorav1.WaitWakeResponse]) error {
	id := req.Msg.GetSessionId()
	turn, err := s.h.sessions.Turn(ctx, id)
	if err != nil {
		return toConnect(err)
	}
	gen, done := s.h.startWait(id)
	defer done()
	if err := stream.Send(&agorav1.WaitWakeResponse{Armed: true}); err != nil {
		return err
	}
	for {
		changed := s.h.changes.wait()
		if current, _ := s.h.currentWait(id); current != gen {
			return nil // a newer wait for this session took over
		}
		w, finished, err := s.h.sessions.CheckWake(ctx, id, turn)
		if err != nil {
			return toConnect(err)
		}
		if finished {
			if w == nil {
				return nil
			}
			if err := stream.Send(&agorav1.WaitWakeResponse{Text: w.Text}); err != nil {
				return err // not delivered: nothing is marked read
			}
			s.h.log.Info("woke a session", "session", id)
			if err := s.h.sessions.ConfirmWake(context.WithoutCancel(ctx), id, w); err != nil {
				s.h.log.Error("confirm wake", "session", id, "err", err)
			}
			s.h.changes.fire()
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		case <-time.After(SweepEvery):
		}
	}
}

// wakeLoop runs the wake command for idle sessions with a terminal that no connector waits for.
func (h *Hub) wakeLoop(ctx context.Context) {
	t := time.NewTicker(h.wakeEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := h.wakeByCommand(ctx); err != nil && ctx.Err() == nil {
				h.log.Error("wake command", "err", err)
			}
		}
	}
}

func (h *Hub) wakeByCommand(ctx context.Context) error {
	idle, err := h.sessions.IdleWithTerminal(ctx, h.WakeSettle)
	if err != nil {
		return err
	}
	now := h.sessions.Now()
	var wg sync.WaitGroup
	for _, c := range idle {
		if _, active := h.currentWait(c.SessionID); active > 0 || !h.alive(c.PID, c.PIDStart) {
			continue // its connector waits and will wake it, or its process is gone
		}
		key, text, err := h.sessions.Pending(ctx, c.Agent)
		if err != nil {
			return err
		}
		if key == "" || key == c.WokenFor || now.Sub(c.WokenAt) < WakeGap {
			continue
		}
		h.waitMu.Lock()
		busy := h.waking[c.SessionID]
		h.waking[c.SessionID] = true
		h.waitMu.Unlock()
		if busy {
			continue // the previous run for this session has not finished
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { h.waitMu.Lock(); delete(h.waking, c.SessionID); h.waitMu.Unlock() }()
			result, ok := runWakeCommand(ctx, h.WakeCommand, c.Terminal, text)
			h.log.Info("woke a session by command", "session", c.SessionID, "agent", c.Agent, "result", result)
			if err := h.sessions.RecordWake(context.WithoutCancel(ctx), c.SessionID, key, result, ok); err != nil {
				h.log.Error("record wake", "session", c.SessionID, "err", err)
			}
		}()
	}
	wg.Wait()
	return nil
}

// runWakeCommand runs the wake command for one terminal in its own process group, killing the
// whole group after WakeTimeout, and returns "ok" or the error with the end of its output.
func runWakeCommand(ctx context.Context, command, terminal, text string) (string, bool) {
	ctx, cancel := context.WithTimeout(ctx, WakeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", command) //nolint:gosec // the wake command is configured by the operator who runs the hub
	cmd.Env = append(os.Environ(), "AGORA_TERMINAL="+terminal, "AGORA_WAKE_TEXT="+text)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = wakeKillWait
	out, err := cmd.CombinedOutput()
	if err == nil {
		return "ok", true
	}
	tail := strings.TrimSpace(string(out))
	if len(tail) > wakeOutputTail {
		tail = tail[len(tail)-wakeOutputTail:]
	}
	return strings.TrimSpace(err.Error() + ": " + tail), false
}
