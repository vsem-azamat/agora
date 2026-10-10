package hub

import (
	"context"
	"time"
)

// WakeByCommand runs one round of command wakes, for tests.
func WakeByCommand(ctx context.Context, h *Hub) error { return h.wakeByCommand(ctx) }

// RunWakeCommand runs one wake command, for tests.
func RunWakeCommand(ctx context.Context, command string) (string, bool) {
	return runWakeCommand(ctx, command, "pane", "text")
}

// SetWakeEvery changes how often the wake loop runs, for tests.
func SetWakeEvery(h *Hub, d time.Duration) { h.wakeEvery = d }

// Constants tests depend on.
const (
	MaxSocketPath = maxSocketPath
	WakeGap       = wakeGap
	WatchGap      = watchGap
)
