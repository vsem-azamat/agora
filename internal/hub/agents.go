package hub

import (
	"context"
	"fmt"

	"connectrpc.com/connect"

	agorav1 "github.com/vsem-azamat/agora/gen/agora/v1"
	"github.com/vsem-azamat/agora/internal/agents"
	"github.com/vsem-azamat/agora/internal/rooms"
	"github.com/vsem-azamat/agora/internal/version"
)

type agentService struct{ h *Hub }

func (s *agentService) UpdateProfile(ctx context.Context, req *connect.Request[agorav1.UpdateProfileRequest]) (*connect.Response[agorav1.UpdateProfileResponse], error) {
	m := req.Msg
	u := agents.Update{Kind: m.Kind, Project: m.Project, Task: m.Task, Status: m.Status, CWD: m.Cwd, About: m.About, Icon: m.Icon, Pigment: m.Pigment}
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
	resp := &agorav1.UpdateProfileResponse{Profile: profilePB(p)}
	if m.Project != nil {
		// the project's room moves with the project, for every client
		switch f, err := s.h.rooms.FollowProject(ctx, m.GetAgent()); {
		case err != nil: // the profile is updated; the output says why the room is not followed
			s.h.log.Error("follow the project's room", "agent", m.GetAgent(), "err", err)
			resp.ProjectRoom = &agorav1.ProjectRoom{Room: rooms.ProjectRoom(p.Project), State: agorav1.ProjectRoomState_PROJECT_ROOM_STATE_FAILED, Detail: "could not follow it; follow it yourself with `agora subscribe " + rooms.ProjectRoom(p.Project) + "`"}
		default:
			resp.ProjectRoom = &agorav1.ProjectRoom{Room: f.Room, State: projectRoomStates[f.State], Unfollowed: f.Unfollowed, Detail: f.Detail}
		}
	}
	s.h.changes.fire()
	return connect.NewResponse(resp), nil
}

var projectRoomStates = map[rooms.ProjectState]agorav1.ProjectRoomState{
	rooms.ProjectNone:      agorav1.ProjectRoomState_PROJECT_ROOM_STATE_NONE,
	rooms.ProjectFollowing: agorav1.ProjectRoomState_PROJECT_ROOM_STATE_FOLLOWING,
	rooms.ProjectNoRoom:    agorav1.ProjectRoomState_PROJECT_ROOM_STATE_MISSING,
	rooms.ProjectFailed:    agorav1.ProjectRoomState_PROJECT_ROOM_STATE_FAILED,
}

func (s *agentService) Rename(ctx context.Context, req *connect.Request[agorav1.RenameRequest]) (*connect.Response[agorav1.RenameResponse], error) {
	if s.h.webAs != "" && req.Msg.GetAgent() == s.h.webAs {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
			"%s is the web app's operator; to rename it, restart the hub with another --web-as (or $AGORA_WEB_AS)", s.h.webAs))
	}
	if err := s.h.sessions.Rename(ctx, req.Msg.GetAgent(), req.Msg.GetName()); err != nil {
		return nil, toConnect(err)
	}
	s.h.changes.fire()
	p, err := s.h.agents.Get(ctx, req.Msg.GetName())
	if err != nil {
		return nil, toConnect(err)
	}
	return connect.NewResponse(&agorav1.RenameResponse{Profile: profilePB(p)}), nil
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
	out := &agorav1.ListAgentsResponse{HubVersion: version.Get().Version, HubCommit: version.Get().Commit}
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
