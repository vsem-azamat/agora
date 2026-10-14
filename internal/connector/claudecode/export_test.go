package claudecode

import "context"

// SetInvokingVersion makes the hooks take version for the version of the Claude Code process
// they run under, and returns a function that restores the previous one.
func SetInvokingVersion(f func(context.Context) string) (restore func()) {
	old := invokingVersion
	invokingVersion = f
	return func() { invokingVersion = old }
}
