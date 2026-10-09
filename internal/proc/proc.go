// Package proc identifies processes on the local machine: a pid together with its start time
// names one process, even after the pid is reused.
package proc

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// stat returns the fields of /proc/<pid>/stat after the command name, which may contain
// spaces; the first returned field is the state (field 3 of the file).
func stat(pid int) ([]string, error) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return nil, err
	}
	i := strings.LastIndexByte(string(b), ')')
	if i < 0 {
		return nil, errors.New("unexpected /proc stat format")
	}
	return strings.Fields(string(b)[i+1:]), nil
}

// Parent returns the parent pid of pid.
func Parent(pid int) (int, error) {
	f, err := stat(pid)
	if err != nil || len(f) < 2 {
		return 0, errors.Join(err, errors.New("no parent"))
	}
	return strconv.Atoi(f[1])
}

// StartTime returns when pid started, in clock ticks since boot, or 0 when the platform does
// not say.
func StartTime(pid int) int64 {
	f, err := stat(pid)
	if err != nil || len(f) < 20 {
		return 0
	}
	t, _ := strconv.ParseInt(f[19], 10, 64) // field 22 of the file
	return t
}

// Command returns the command name of pid, or "".
func Command(pid int) string {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/comm")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// Alive reports whether the process pid that started at start still runs. Without /proc, or
// with an unknown start time, it only checks that some process has the pid.
func Alive(pid int, start int64) bool {
	if pid <= 0 {
		return false
	}
	if _, err := os.Stat("/proc/self/stat"); err == nil {
		got := StartTime(pid)
		if got == 0 {
			_, err := os.Stat("/proc/" + strconv.Itoa(pid))
			return err == nil
		}
		return start == 0 || got == start
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
