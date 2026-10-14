package claudecode

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// StopFailureSince is the first Claude Code version that reports a failure to load credentials
// as cloud_credential_error; older ones report it as server_error or unknown, which the
// StopFailure hooks take for transient errors and wake the session for.
const StopFailureSince = "2.1.267"

// VersionTimeout bounds how long asking Claude Code for its version waits.
const VersionTimeout = 10 * time.Second

// versionRE finds a version at the start of a line, so a line printed before it is skipped.
var versionRE = regexp.MustCompile(`(?m)^\s*(\d+)\.(\d+)\.(\d+)`)

// VersionOf returns the version that `command --version` prints, as major.minor.patch. A
// command that is not found gives an error that matches exec.ErrNotFound.
func VersionOf(ctx context.Context, command string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, VersionTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, command, "--version")
	cmd.WaitDelay = time.Second // a child left holding the output does not keep the caller waiting
	out, err := cmd.Output()
	var exitErr *exec.ExitError
	switch {
	case errors.Is(err, exec.ErrNotFound):
		return "", err
	case errors.Is(err, exec.ErrWaitDelay): // claude exited; a child of it kept the output open
	case ctx.Err() != nil:
		return "", fmt.Errorf("claude --version did not answer within %s", VersionTimeout)
	case errors.As(err, &exitErr) && len(bytes.TrimSpace(exitErr.Stderr)) > 0:
		return "", fmt.Errorf("claude --version: %w: %s", err, firstLine(exitErr.Stderr))
	case err != nil:
		return "", fmt.Errorf("claude --version: %w", err)
	}
	m := versionRE.FindStringSubmatch(string(out))
	if m == nil {
		return "", fmt.Errorf("claude --version printed no version: %q", strings.TrimSpace(string(out)))
	}
	return m[1] + "." + m[2] + "." + m[3], nil
}

// stderrChars is how many characters of claude's error output a failure quotes.
const stderrChars = 200

// firstLine returns the first line of b, trimmed and at most stderrChars characters long.
func firstLine(b []byte) string {
	line, _, _ := strings.Cut(strings.TrimSpace(string(b)), "\n")
	if r := []rune(line); len(r) > stderrChars {
		line = string(r[:stderrChars])
	}
	return line
}

// ReportsCredentials reports whether Claude Code of version tells credential failures apart,
// so its StopFailure hooks are safe; a text with no version counts as safe.
func ReportsCredentials(version string) bool {
	m := versionRE.FindStringSubmatch(version)
	if m == nil {
		return true
	}
	since := versionRE.FindStringSubmatch(StopFailureSince)
	for i := 1; i <= 3; i++ {
		a, errA := strconv.Atoi(m[i])
		b, _ := strconv.Atoi(since[i])
		if errA != nil { // too many digits for an int: newer than any version we know
			return true
		}
		if a != b {
			return a > b
		}
	}
	return true
}
