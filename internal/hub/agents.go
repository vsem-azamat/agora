package hub

import (
	"context"

	"connectrpc.com/connect"

	agorav1 "github.com/vsem-azamat/agora/gen/agora/v1"
	"github.com/vsem-azamat/agora/internal/agents"
)

type agentService struct{ h *Hub }

func (s *agentService) UpdateProfile(ctx context.Context, req *connect.Request[agorav1.UpdateProfileRequest]) (*connect.Response[agorav1.UpdateProfileResponse], error) {
	m := req.Msg
	u := agents.Update{Kind: m.Kind, Project: m.Project, Task: m.Task, Status: m.Status, CWD: m.Cwd, About: m.About}
	for _, n := range m.GetAddPrs() {
		u.AddPRs = append(u.AddPRs, int(n))
	}
	for _, n := range m.GetDropPrs() {
		u.DropPRs = append(u.DropPRs, int(n))
	}
	p, err := s.h.agents.Update(ctx, m.GetAgent(), u)
	if err != nil {
		return nil, toConnect(err)
	}
	s.h.changes.fire()
	return connect.NewResponse(&agorav1.UpdateProfileResponse{Profile: profilePB(p)}), nil
}

func (s *agentService) Leave(ctx context.Context, req *connect.Request[agorav1.LeaveRequest]) (*connect.Response[agorav1.LeaveResponse], error) {
	released, err := s.h.sessions.Leave(ctx, req.Msg.GetAgent())
	if err != nil {
		return nil, toConnect(err)
	}
	s.h.changes.fire()
	return connect.NewResponse(&agorav1.LeaveResponse{Released: released}), nil
}

func (s *agentService) ListAgents(ctx context.Context, req *connect.Request[agorav1.ListAgentsRequest]) (*connect.Response[agorav1.ListAgentsResponse], error) {
	list, err := s.h.agents.List(ctx, req.Msg.GetAll())
	if err != nil {
		return nil, toConnect(err)
	}
	out := &agorav1.ListAgentsResponse{}
	for _, p := range list {
		out.Agents = append(out.Agents, profilePB(p))
	}
	return connect.NewResponse(out), nil
}

func (s *agentService) Who(ctx context.Context, req *connect.Request[agorav1.WhoRequest]) (*connect.Response[agorav1.WhoResponse], error) {
	list, err := s.h.agents.Who(ctx, req.Msg.GetQuery(), req.Msg.GetPath(), req.Msg.GetAll())
	if err != nil {
		return nil, toConnect(err)
	}
	out := &agorav1.WhoResponse{}
	for _, p := range list {
		out.Agents = append(out.Agents, profilePB(p))
	}
	return connect.NewResponse(out), nil
}
