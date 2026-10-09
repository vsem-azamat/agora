package cli

import (
	"context"
	"io"
	"time"

	"github.com/spf13/cobra"
)

// SetHubGiveUp changes how long a wait tolerates an unreachable hub, for tests; the returned
// function restores it.
func SetHubGiveUp(d time.Duration) func() {
	old := hubGiveUp
	hubGiveUp = d
	return func() { hubGiveUp = old }
}

// NewRoot builds the agora command tree, for tests that inspect it.
func NewRoot() *cobra.Command { return newRoot(io.Discard, io.Discard) }

// SetSystemctl replaces how systemctl runs and returns a function that restores it.
func SetSystemctl(f func(ctx context.Context, args ...string) ([]byte, error)) func() {
	old := systemctl
	systemctl = f
	return func() { systemctl = old }
}

// SetGOOS pretends to run on another operating system and returns a function that restores it.
func SetGOOS(s string) func() {
	old := goos
	goos = s
	return func() { goos = old }
}
