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

// SetBridgeDelays changes the restart delays of bridges and how long a run counts as steady,
// for tests.
func SetBridgeDelays(h *Hub, minDelay, maxDelay, steady time.Duration) {
	h.bridgeRuns.minDelay, h.bridgeRuns.maxDelay, h.bridgeRuns.steady = minDelay, maxDelay, steady
}

// NextBridgeDelay is the delay after d before a bridge starts again, for tests.
func NextBridgeDelay(d time.Duration) time.Duration { return nextBridgeDelay(d, bridgeMaxDelay) }

// Bridge restart constants tests depend on.
const (
	BridgeMinDelay = bridgeMinDelay
	BridgeSteady   = bridgeSteady
)
