package hub

import "context"

// WakeByCommand runs one round of command wakes, for tests.
func WakeByCommand(h *Hub, ctx context.Context) error { return h.wakeByCommand(ctx) }
