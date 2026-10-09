package hub

import "context"

// WakeByCommand runs one round of command wakes, for tests.
func WakeByCommand(ctx context.Context, h *Hub) error { return h.wakeByCommand(ctx) }

// RunWakeCommand runs one wake command, for tests.
func RunWakeCommand(ctx context.Context, command string) (string, bool) {
	return runWakeCommand(ctx, command, "pane", "text")
}
