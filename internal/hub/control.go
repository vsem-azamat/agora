package hub

import (
	"context"
	"sync"
	"time"
)

// controlLoop drops the compacts of sessions that ended and runs the compact command for the
// ones whose session is idle. A compact a stopped hub left running is failed first.
func (h *Hub) controlLoop(ctx context.Context) {
	if err := h.sessions.FailRunningCompacts(ctx); err != nil && ctx.Err() == nil {
		h.log.Error("fail running compacts", "err", err)
	}
	t := time.NewTicker(h.controlEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := h.runCompacts(ctx); err != nil && ctx.Err() == nil {
				h.log.Error("compact", "err", err)
			}
		}
	}
}

// runCompacts does one round of the control loop and waits for the commands it started.
func (h *Hub) runCompacts(ctx context.Context) error {
	if err := h.sessions.DropEndedCompacts(ctx); err != nil {
		return err
	}
	if h.CompactCommand == "" {
		return nil // what is queued waits for a hub with the command, or for its session to end
	}
	queued, err := h.sessions.CompactCandidates(ctx, h.WakeSettle, h.alive)
	if err != nil {
		return err
	}
	var wg sync.WaitGroup
	for _, c := range queued {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock := h.lockTerminal(c.Terminal)
			defer unlock()
			// decided again with the terminal held: a wake that typed meanwhile has made the session busy
			if !h.alive(c.PID, c.PIDStart) {
				return
			}
			if claimed, err := h.sessions.ClaimCompact(ctx, c.ID, h.WakeSettle); err != nil || !claimed {
				if err != nil && ctx.Err() == nil {
					h.log.Error("claim compact", "agent", c.Agent, "err", err)
				}
				return
			}
			result, ok := runTerminalCommand(ctx, h.CompactCommand, c.Terminal, c.Text())
			h.log.Info("compacted a session", "agent", c.Agent, "by", c.By, "result", result)
			if err := h.sessions.FinishCompact(context.WithoutCancel(ctx), c.ID, ok, result); err != nil {
				h.log.Error("finish compact", "agent", c.Agent, "err", err)
			}
			h.changes.fire()
		}()
	}
	wg.Wait()
	return nil
}

// takeSession claims key in the map of running commands and returns how to release it; false
// when it is taken.
func (h *Hub) takeSession(key string) (release func(), ok bool) {
	h.waitMu.Lock()
	defer h.waitMu.Unlock()
	if h.waking[key] {
		return nil, false
	}
	h.waking[key] = true
	return func() { h.waitMu.Lock(); delete(h.waking, key); h.waitMu.Unlock() }, true
}

// lockTerminal waits until no other command types into the terminal and returns how to let the
// next one in: a wake, a compact and an interrupt never interleave their keystrokes.
func (h *Hub) lockTerminal(name string) (unlock func()) {
	h.termMu.Lock()
	t := h.terminals[name]
	if t == nil {
		t = &terminal{}
		h.terminals[name] = t
	}
	h.termMu.Unlock()
	t.waiting.Add(1)
	t.mu.Lock()
	t.waiting.Add(-1)
	return t.mu.Unlock
}
