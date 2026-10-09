package claudecode_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"connectrpc.com/connect"

	agorav1 "github.com/vsem-azamat/agora/gen/agora/v1"
	"github.com/vsem-azamat/agora/gen/agora/v1/agorav1connect"
	"github.com/vsem-azamat/agora/internal/connector/claudecode"
)

// fakeHub answers Report with a fixed reply and records the request.
type fakeHub struct {
	reply *agorav1.ReportResponse
	err   error
	got   *agorav1.ReportRequest
}

func (f *fakeHub) Report(_ context.Context, req *connect.Request[agorav1.ReportRequest]) (*connect.Response[agorav1.ReportResponse], error) {
	f.got = req.Msg
	if f.err != nil {
		return nil, f.err
	}
	return connect.NewResponse(f.reply), nil
}

func (f *fakeHub) JoinName(context.Context, *connect.Request[agorav1.JoinNameRequest]) (*connect.Response[agorav1.JoinNameResponse], error) {
	return nil, errors.New("unused")
}

func (f *fakeHub) Resolve(context.Context, *connect.Request[agorav1.ResolveRequest]) (*connect.Response[agorav1.ResolveResponse], error) {
	return nil, errors.New("unused")
}

func (f *fakeHub) ListSessions(context.Context, *connect.Request[agorav1.ListSessionsRequest]) (*connect.Response[agorav1.ListSessionsResponse], error) {
	return nil, errors.New("unused")
}

func (f *fakeHub) WaitWake(context.Context, *connect.Request[agorav1.WaitWakeRequest]) (*connect.ServerStreamForClient[agorav1.WaitWakeResponse], error) {
	return nil, errors.New("unused")
}

var _ agorav1connect.SessionServiceClient = (*fakeHub)(nil)

func run(t *testing.T, hub *fakeHub, input string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := claudecode.Hook(context.Background(), hub, strings.NewReader(input), &out)
	return out.String(), err
}

func TestContextIsAddedForPrompts(t *testing.T) {
	hub := &fakeHub{reply: &agorav1.ReportResponse{Context: "Agora: you are builder."}}
	t.Setenv("AGORA_TERMINAL", "pane-7")
	out, err := run(t, hub, `{"session_id":"session-1","hook_event_name":"UserPromptSubmit","cwd":"/src/example-app"}`)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output %q: %v", out, err)
	}
	if got.HookSpecificOutput.HookEventName != "UserPromptSubmit" || got.HookSpecificOutput.AdditionalContext != "Agora: you are builder." {
		t.Fatalf("output %+v", got)
	}
	if hub.got.GetEvent() != agorav1.SessionEvent_SESSION_EVENT_PROMPT || hub.got.GetCwd() != "/src/example-app" ||
		hub.got.GetTerminal() != "pane-7" || hub.got.GetKind() != "claude-code" {
		t.Fatalf("report %+v", hub.got)
	}
}

func TestStopCanBlock(t *testing.T) {
	hub := &fakeHub{reply: &agorav1.ReportResponse{Block: true, BlockReason: "your turn on example-app/merge"}}
	out, err := run(t, hub, `{"session_id":"session-1","hook_event_name":"Stop","stop_hook_active":true}`)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	json.Unmarshal([]byte(out), &got)
	if got["decision"] != "block" || got["reason"] != "your turn on example-app/merge" {
		t.Fatalf("output %q", out)
	}
	if !hub.got.GetStopActive() {
		t.Fatal("stop_hook_active was not passed on")
	}
}

func TestStopWithoutBlockPrintsNothing(t *testing.T) {
	hub := &fakeHub{reply: &agorav1.ReportResponse{Context: "ignored at stop"}}
	if out, err := run(t, hub, `{"session_id":"session-1","hook_event_name":"Stop"}`); err != nil || out != "" {
		t.Fatalf("out %q err %v", out, err)
	}
}

func TestFailuresPrintNothing(t *testing.T) {
	for name, tc := range map[string]struct {
		hub   *fakeHub
		input string
	}{
		"hub down":        {&fakeHub{err: connect.NewError(connect.CodeUnavailable, errors.New("down"))}, `{"session_id":"session-1","hook_event_name":"PostToolUse"}`},
		"malformed input": {&fakeHub{}, `not json`},
	} {
		out, err := run(t, tc.hub, tc.input)
		if out != "" || err == nil {
			t.Errorf("%s: out %q err %v", name, out, err)
		}
	}
}

func TestUnknownEventsAreIgnored(t *testing.T) {
	hub := &fakeHub{}
	if out, err := run(t, hub, `{"session_id":"session-1","hook_event_name":"PreCompact"}`); out != "" || err != nil || hub.got != nil {
		t.Fatalf("out %q err %v report %v", out, err, hub.got)
	}
}
