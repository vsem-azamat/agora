// Package claudecode is the Claude Code connector: it runs as Claude Code hooks, reports the
// session to the hub and turns the hub's reply into hook output.
package claudecode

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"connectrpc.com/connect"

	agorav1 "github.com/vsem-azamat/agora/gen/agora/v1"
	"github.com/vsem-azamat/agora/gen/agora/v1/agorav1connect"
	"github.com/vsem-azamat/agora/internal/proc"
)

// Kind is how Claude Code sessions are labelled.
const Kind = "claude-code"

// sessionEnv is the environment variable in which Claude Code gives the commands it runs its
// session identifier.
const sessionEnv = "CLAUDE_CODE_SESSION_ID"

// SessionID returns the Claude Code session this process runs in, or "" outside Claude Code.
func SessionID() string { return os.Getenv(sessionEnv) }

// HubTimeout is how long a hook waits for the hub before giving up silently.
const HubTimeout = 2 * time.Second

const (
	// maxAncestors is how far up the process tree a hook looks for the Claude Code process.
	maxAncestors = 6
	// waitRetry is the pause before the waiting hook reconnects to the hub.
	waitRetry = 2 * time.Second
)

// hookInput is the part of Claude Code's hook input the connector uses.
type hookInput struct {
	SessionID      string `json:"session_id"`
	HookEventName  string `json:"hook_event_name"`
	CWD            string `json:"cwd"`
	StopHookActive bool   `json:"stop_hook_active"`
	Reason         string `json:"reason"` // SessionEnd: why the session ends, e.g. "clear"
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
	pid := agentPID()
	var start int64
	if pid > 0 {
		start = proc.StartTime(pid)
	}
	resp, err := client.Report(ctx, connect.NewRequest(&agorav1.ReportRequest{
		SessionId: h.SessionID, Kind: Kind, Event: ev, Pid: int32(pid), PidStart: start,
		Cwd: h.CWD, Terminal: os.Getenv("AGORA_TERMINAL"), StopActive: h.StopHookActive, Reason: h.Reason,
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
	for range maxAncestors {
		if proc.Command(pid) == "claude" {
			return pid
		}
		next, err := proc.Parent(pid)
		if err != nil || next <= 1 {
			break
		}
		pid = next
	}
	return 0
}

// ExitWake is the exit code with which an asyncRewake hook wakes Claude Code.
const ExitWake = 2

// GiveUpAfter is how long the waiting hook keeps trying to reach the hub, since it last
// answered, before it ends quietly.
const GiveUpAfter = 5 * time.Minute

// Wait is the asynchronous wake hook: it waits on behalf of the idle session named in Claude
// Code's hook input and returns the wake text when something needs the agent, or "" when the
// wait ends without a wake (the session got busy, ended, a newer wait took over, or the hub
// stayed unreachable for GiveUpAfter). The caller exits with code 2 and the text to wake
// Claude Code, or 0.
func Wait(ctx context.Context, client agorav1connect.SessionServiceClient, in io.Reader) (string, error) {
	var h hookInput
	if err := json.NewDecoder(in).Decode(&h); err != nil {
		return "", fmt.Errorf("read hook input: %w", err)
	}
	reached := time.Now() // the last time the hub answered
	for {
		text, err := waitOnce(ctx, client, h.SessionID, func() { reached = time.Now() })
		if err == nil {
			return text, nil
		}
		code := connect.CodeOf(err)
		if code != connect.CodeUnavailable && code != connect.CodeCanceled && code != connect.CodeUnknown {
			return "", err
		}
		if time.Since(reached) > GiveUpAfter || ctx.Err() != nil {
			return "", err
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(waitRetry):
		}
	}
}

func waitOnce(ctx context.Context, client agorav1connect.SessionServiceClient, sessionID string, armed func()) (string, error) {
	stream, err := client.WaitWake(ctx, connect.NewRequest(&agorav1.WaitWakeRequest{SessionId: sessionID}))
	if err != nil {
		return "", err
	}
	defer stream.Close()
	for stream.Receive() {
		if stream.Msg().GetArmed() {
			armed()
			continue
		}
		return stream.Msg().GetText(), nil
	}
	return "", stream.Err()
}
