package proc_test

import (
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"github.com/vsem-azamat/agora/internal/proc"
)

func TestEnvReadsTheEnvironmentAProcessStartedWith(t *testing.T) {
	if _, err := os.Stat("/proc/self/environ"); err != nil {
		t.Skip("no /proc")
	}
	cmd := exec.Command("sleep", "10")
	cmd.Env = []string{"AGORA_PROC_TEST=session-a", "AGORA_PROC_TEST_EMPTY="}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	pid := cmd.Process.Pid
	// the kernel names the new program before it sets up its environment
	for deadline := time.Now().Add(5 * time.Second); !started(pid); time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the child never ran sleep")
		}
	}
	for name, want := range map[string]string{"AGORA_PROC_TEST": "session-a", "AGORA_PROC_TEST_EMPTY": "", "AGORA_PROC": "", "MISSING": ""} {
		if got := proc.Env(pid, name); got != want {
			t.Errorf("%s: got %q, want %q", name, got, want)
		}
	}
	if got := proc.Env(-1, "AGORA_PROC_TEST"); got != "" {
		t.Errorf("no process: got %q", got)
	}
}

// started reports whether pid runs sleep with its environment in place.
func started(pid int) bool {
	b, _ := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/environ")
	return proc.Command(pid) == "sleep" && len(b) > 0
}
