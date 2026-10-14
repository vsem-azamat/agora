package claudecode

import "context"

// SetInvokingVersion makes the hooks take version for the version of the Claude Code process
// they run under, and returns a function that restores the previous one.
func SetInvokingVersion(f func(context.Context) (string, error)) (restore func()) {
	old := InvokingVersion
	InvokingVersion = f
	return func() { InvokingVersion = old }
}

// SetExecutable makes the real version lookup run exe instead of the Claude Code process's own
// program, and returns a function that restores the previous one.
func SetExecutable(exe string) (restore func()) {
	old := executable
	executable = func() string { return exe }
	return func() { executable = old }
}

// LookUpVersion is the real lookup of the version of the process a hook runs under.
var LookUpVersion = lookUpVersion
