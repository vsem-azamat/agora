// Package claudecode is the Claude Code connector: it runs as Claude Code hooks, reports the
// session to the hub and turns the hub's reply into hook output.
package claudecode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"

	agorav1 "github.com/vsem-azamat/agora/gen/agora/v1"
	"github.com/vsem-azamat/agora/gen/agora/v1/agorav1connect"
)

// Kind is how Claude Code sessions are labelled.
const Kind = "claude-code"

// HubTimeout is how long a hook waits for the hub before giving up silently.
const HubTimeout = 2 * time.Second

// hookInput is the part of Claude Code's hook input the connector uses.
type hookInput struct {
	SessionID      string `json:"session_id"`
	HookEventName  string `json:"hook_event_name"`
	CWD            string `json:"cwd"`
	StopHookActive bool   `json:"stop_hook_active"`
}

var events = map[string]agorav1.SessionEvent{
	"SessionStart":     agorav1.SessionEvent_SESSION_EVENT_START,
	"UserPromptSubmit": agorav1.SessionEvent_SESSION_EVENT_PROMPT,
	"PostToolUse":      agorav1.SessionEvent_SESSION_EVENT_TOOL,
	"Stop":             agorav1.SessionEvent_SESSION_EVENT_STOP,
	"SessionEnd":       agorav1.SessionEvent_SESSION_EVENT_END,
}

// Hook handles one hook call: it reads Claude Code's input from in and writes hook output to
// out. It returns an error only for AGORA_DEBUG; callers otherwise ignore it, so a failure
// never disturbs the session.
func Hook(ctx context.Context, client agorav1connect.SessionServiceClient, in io.Reader, out io.Writer) error {
	var h hookInput
	if err := json.NewDecoder(in).Decode(&h); err != nil {
		return fmt.Errorf("read hook input: %w", err)
	}
	ev, ok := events[h.HookEventName]
	if !ok {
		return nil // an event the connector does not handle
	}
	ctx, cancel := context.WithTimeout(ctx, HubTimeout)
	defer cancel()
	resp, err := client.Report(ctx, connect.NewRequest(&agorav1.ReportRequest{
		SessionId: h.SessionID, Kind: Kind, Event: ev, Pid: int32(agentPID()),
		Cwd: h.CWD, Terminal: os.Getenv("AGORA_TERMINAL"), StopActive: h.StopHookActive,
	}))
	if err != nil {
		return fmt.Errorf("report to hub: %w", err)
	}
	reply := resp.Msg
	switch {
	case reply.GetBlock():
		return json.NewEncoder(out).Encode(map[string]any{"decision": "block", "reason": reply.GetBlockReason()})
	case reply.GetContext() != "" && ev != agorav1.SessionEvent_SESSION_EVENT_STOP && ev != agorav1.SessionEvent_SESSION_EVENT_END:
		return json.NewEncoder(out).Encode(map[string]any{"hookSpecificOutput": map[string]any{
			"hookEventName": h.HookEventName, "additionalContext": reply.GetContext(),
		}})
	}
	return nil
}

// agentPID finds the Claude Code process this hook belongs to: hooks run under a shell, so it
// walks up the process tree looking for `claude`. It returns 0 when it cannot tell, so the hub
// never mistakes the short-lived hook shell for the session.
func agentPID() int {
	pid := os.Getppid()
	for range 6 {
		comm, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/comm")
		if err != nil {
			break
		}
		if strings.TrimSpace(string(comm)) == "claude" {
			return pid
		}
		next, err := parentOf(pid)
		if err != nil || next <= 1 {
			break
		}
		pid = next
	}
	return 0
}

func parentOf(pid int) (int, error) {
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, err
	}
	// the command name in field 2 may contain spaces; fields after the last ')' are fixed
	i := strings.LastIndexByte(string(stat), ')')
	if i < 0 {
		return 0, errors.New("unexpected stat format")
	}
	fields := strings.Fields(string(stat)[i+1:])
	if len(fields) < 2 {
		return 0, errors.New("unexpected stat format")
	}
	return strconv.Atoi(fields[1])
}
