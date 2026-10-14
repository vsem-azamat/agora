package hub

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	agorav1 "github.com/vsem-azamat/agora/gen/agora/v1"
	"github.com/vsem-azamat/agora/internal/sessions"
)

type sessionService struct{ h *Hub }

var events = map[agorav1.SessionEvent]sessions.Event{
	agorav1.SessionEvent_SESSION_EVENT_START:   sessions.Start,
	agorav1.SessionEvent_SESSION_EVENT_PROMPT:  sessions.Prompt,
	agorav1.SessionEvent_SESSION_EVENT_TOOL:    sessions.Tool,
	agorav1.SessionEvent_SESSION_EVENT_STOP:    sessions.Stop,
	agorav1.SessionEvent_SESSION_EVENT_END:     sessions.End,
	agorav1.SessionEvent_SESSION_EVENT_WAITING: sessions.Waiting,
	agorav1.SessionEvent_SESSION_EVENT_COMPACT: sessions.Compact,
}

func (s *sessionService) Report(ctx context.Context, req *connect.Request[agorav1.ReportRequest]) (*connect.Response[agorav1.ReportResponse], error) {
	m := req.Msg
	ev, ok := events[m.GetEvent()]
	if !ok {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("unknown session event"))
	}
	if ev == sessions.End {
		ctx = context.WithoutCancel(ctx) // finish giving back places even if the hook is cut short
	}
	reply, err := s.h.sessions.Report(ctx, sessions.Report{
		SessionID: m.GetSessionId(), Kind: m.GetKind(), Event: ev, PID: int(m.GetPid()), PIDStart: m.GetPidStart(),
		CWD: m.GetCwd(), Terminal: m.GetTerminal(), StopActive: m.GetStopActive(), Reason: m.GetReason(),
	})
	if err != nil {
		return nil, toConnect(err)
	}
	s.h.changes.fire() // an ended session may have given back places
	return connect.NewResponse(&agorav1.ReportResponse{Context: reply.Context, Block: reply.Block, BlockReason: reply.BlockReason}), nil
}

func (s *sessionService) JoinName(ctx context.Context, req *connect.Request[agorav1.JoinNameRequest]) (*connect.Response[agorav1.JoinNameResponse], error) {
	bound, err := s.h.sessions.Join(ctx, req.Msg.GetName(), req.Msg.GetSessionId(), req.Msg.GetForce())
	if err != nil {
		return nil, toConnect(err)
	}
	s.h.changes.fire()
	return connect.NewResponse(&agorav1.JoinNameResponse{Bound: bound}), nil
}

func (s *sessionService) Resolve(ctx context.Context, req *connect.Request[agorav1.ResolveRequest]) (*connect.Response[agorav1.ResolveResponse], error) {
	agent, err := s.h.sessions.Resolve(ctx, req.Msg.GetSessionId())
	if err != nil {
		return nil, toConnect(err)
	}
	return connect.NewResponse(&agorav1.ResolveResponse{Agent: agent}), nil
}

func (s *sessionService) ListSessions(ctx context.Context, _ *connect.Request[agorav1.ListSessionsRequest]) (*connect.Response[agorav1.ListSessionsResponse], error) {
	list, err := s.h.sessions.List(ctx)
	if err != nil {
		return nil, toConnect(err)
	}
	h := s.h
	out := &agorav1.ListSessionsResponse{}
	for _, x := range list {
		out.Sessions = append(out.Sessions, &agorav1.Session{
			Id: x.ID, Kind: x.Kind, Agent: x.Agent, State: sessionStates[x.State],
			StateAt: timestamppb.New(x.StateAt), StartedAt: timestamppb.New(x.StartedAt),
			Pid: int32(x.PID), Cwd: x.CWD, Terminal: x.Terminal, Capabilities: capabilitiesPB(h.commands(), x),
		})
	}
	return connect.NewResponse(out), nil
}

var mechanisms = map[sessions.Mechanism]agorav1.SessionMechanism{
	sessions.ViaNothing:  agorav1.SessionMechanism_SESSION_MECHANISM_NONE,
	sessions.ViaHook:     agorav1.SessionMechanism_SESSION_MECHANISM_HOOK,
	sessions.ViaTerminal: agorav1.SessionMechanism_SESSION_MECHANISM_TERMINAL,
}

func capabilitiesPB(c sessions.Commands, x sessions.Session) *agorav1.SessionCapabilities {
	caps := sessions.CapabilitiesOf(c, x.Kind, x.Terminal, x.PID)
	return &agorav1.SessionCapabilities{Wake: mechanisms[caps.Wake], Compact: mechanisms[caps.Compact], Interrupt: mechanisms[caps.Interrupt]}
}

func (s *sessionService) Compact(ctx context.Context, req *connect.Request[agorav1.CompactRequest]) (*connect.Response[agorav1.CompactResponse], error) {
	m := req.Msg
	how, err := s.h.sessions.Compact(ctx, s.h.commands(), m.GetAgent(), m.GetTarget(), m.GetFocus())
	if err != nil {
		return nil, toConnect(err)
	}
	s.h.changes.fire()
	return connect.NewResponse(&agorav1.CompactResponse{Mechanism: mechanisms[how]}), nil
}

func (s *sessionService) Interrupt(ctx context.Context, req *connect.Request[agorav1.InterruptRequest]) (*connect.Response[agorav1.InterruptResponse], error) {
	m := req.Msg
	h := s.h
	// one interrupt per session at a time: a second Esc would reach a session that stopped
	key, ok := h.takeSession("interrupt:" + m.GetTarget())
	if !ok {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("an interrupt of %s is already running", m.GetTarget()))
	}
	defer key()
	t, err := h.sessions.PrepareInterrupt(ctx, h.commands(), m.GetAgent(), m.GetTarget())
	if err != nil {
		return nil, toConnect(err)
	}
	if !h.alive(t.PID, t.PIDStart) {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("%s cannot be interrupted: its process no longer runs", m.GetTarget()))
	}
	// the command and its announcement go on when the caller goes away: an Esc that was sent is
	// never left unannounced
	ctx = context.WithoutCancel(ctx)
	unlock := h.lockTerminal(t.Terminal)
	defer unlock()
	// the turn may have ended, or another begun, while another command typed into the terminal
	again, err := h.sessions.PrepareInterrupt(ctx, h.commands(), m.GetAgent(), m.GetTarget())
	if err != nil {
		return nil, toConnect(err)
	}
	if again.ID != t.ID || again.Turn != t.Turn || !h.alive(again.PID, again.PIDStart) {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("the turn of %s that was to be interrupted has ended", m.GetTarget()))
	}
	result, ok := runTerminalCommand(ctx, h.InterruptCommand, t.Terminal, "")
	h.log.Info("interrupted a session", "session", t.ID, "agent", m.GetTarget(), "by", m.GetAgent(), "result", result)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("the interrupt command failed: %s", result))
	}
	if err := h.sessions.FinishInterrupt(ctx, m.GetAgent(), m.GetTarget(), t); err != nil {
		return nil, toConnect(err)
	}
	h.changes.fire()
	return connect.NewResponse(&agorav1.InterruptResponse{Mechanism: mechanisms[sessions.ViaTerminal]}), nil
}
