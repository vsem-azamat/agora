package hub

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	agorav1 "github.com/vsem-azamat/agora/gen/agora/v1"
	"github.com/vsem-azamat/agora/internal/sessions"
)

type sessionService struct{ h *Hub }

var events = map[agorav1.SessionEvent]sessions.Event{
	agorav1.SessionEvent_SESSION_EVENT_START:  sessions.Start,
	agorav1.SessionEvent_SESSION_EVENT_PROMPT: sessions.Prompt,
	agorav1.SessionEvent_SESSION_EVENT_TOOL:   sessions.Tool,
	agorav1.SessionEvent_SESSION_EVENT_STOP:   sessions.Stop,
	agorav1.SessionEvent_SESSION_EVENT_END:    sessions.End,
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
	out := &agorav1.ListSessionsResponse{}
	for _, x := range list {
		out.Sessions = append(out.Sessions, &agorav1.Session{
			Id: x.ID, Kind: x.Kind, Agent: x.Agent, State: sessionStates[x.State],
			StateAt: timestamppb.New(x.StateAt), StartedAt: timestamppb.New(x.StartedAt),
			Pid: int32(x.PID), Cwd: x.CWD, Terminal: x.Terminal,
		})
	}
	return connect.NewResponse(out), nil
}
