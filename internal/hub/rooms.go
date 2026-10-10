package hub

import (
	"context"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	agorav1 "github.com/vsem-azamat/agora/gen/agora/v1"
)

type roomService struct{ h *Hub }

func (s *roomService) CreateRoom(ctx context.Context, req *connect.Request[agorav1.CreateRoomRequest]) (*connect.Response[agorav1.CreateRoomResponse], error) {
	if err := s.h.rooms.Create(ctx, req.Msg.GetName(), req.Msg.GetPurpose(), req.Msg.GetCreator()); err != nil {
		return nil, toConnect(err)
	}
	s.h.changes.fire()
	return connect.NewResponse(&agorav1.CreateRoomResponse{}), nil
}

func (s *roomService) ListRooms(ctx context.Context, _ *connect.Request[agorav1.ListRoomsRequest]) (*connect.Response[agorav1.ListRoomsResponse], error) {
	list, err := s.h.rooms.List(ctx)
	if err != nil {
		return nil, toConnect(err)
	}
	out := &agorav1.ListRoomsResponse{}
	for _, r := range list {
		pb := &agorav1.Room{Name: r.Name, Purpose: r.Purpose, CreatedBy: r.CreatedBy, CreatedAt: timestamppb.New(r.CreatedAt), Messages: int32(r.Messages)}
		if !r.LastAt.IsZero() {
			pb.LastAt = timestamppb.New(r.LastAt)
		}
		out.Rooms = append(out.Rooms, pb)
	}
	return connect.NewResponse(out), nil
}

func (s *roomService) Subscribe(ctx context.Context, req *connect.Request[agorav1.SubscribeRequest]) (*connect.Response[agorav1.SubscribeResponse], error) {
	followed, err := s.h.rooms.Subscribe(ctx, req.Msg.GetAgent(), req.Msg.GetRooms(), req.Msg.GetFollow())
	if err != nil {
		return nil, toConnect(err)
	}
	if len(req.Msg.GetRooms()) > 0 { // no rooms only asks which rooms are followed
		s.h.changes.fire()
	}
	return connect.NewResponse(&agorav1.SubscribeResponse{Rooms: followed}), nil
}

func (s *roomService) Post(ctx context.Context, req *connect.Request[agorav1.PostRequest]) (*connect.Response[agorav1.PostResponse], error) {
	m := req.Msg
	id, err := s.h.rooms.Post(ctx, m.GetAuthor(), m.GetRoom(), m.GetBody(), m.GetReplyTo())
	if err != nil {
		return nil, toConnect(err)
	}
	s.h.changes.fire()
	return connect.NewResponse(&agorav1.PostResponse{Id: id}), nil
}

func (s *roomService) History(ctx context.Context, req *connect.Request[agorav1.HistoryRequest]) (*connect.Response[agorav1.HistoryResponse], error) {
	msgs, err := s.h.rooms.History(ctx, req.Msg.GetRoom(), int(req.Msg.GetLast()))
	if err != nil {
		return nil, toConnect(err)
	}
	return connect.NewResponse(&agorav1.HistoryResponse{Messages: messagesPB(msgs)}), nil
}

func (s *roomService) Unread(ctx context.Context, req *connect.Request[agorav1.UnreadRequest]) (*connect.Response[agorav1.UnreadResponse], error) {
	m := req.Msg
	read := s.h.rooms.Take
	if m.GetPeek() {
		read = s.h.rooms.Unread
	}
	msgs, total, err := read(ctx, m.GetAgent(), m.GetMentionsOnly(), int(m.GetLimit()))
	if err != nil {
		return nil, toConnect(err)
	}
	return connect.NewResponse(&agorav1.UnreadResponse{Messages: messagesPB(msgs), Total: int32(total)}), nil
}

func (s *roomService) UnreadByRoom(ctx context.Context, req *connect.Request[agorav1.UnreadByRoomRequest]) (*connect.Response[agorav1.UnreadByRoomResponse], error) {
	counts, err := s.h.rooms.UnreadByRoom(ctx, req.Msg.GetAgent())
	if err != nil {
		return nil, toConnect(err)
	}
	out := &agorav1.UnreadByRoomResponse{}
	for _, c := range counts {
		out.Rooms = append(out.Rooms, &agorav1.RoomUnread{Room: c.Room, Unread: int32(c.Unread), Addressed: int32(c.Addressed)})
	}
	return connect.NewResponse(out), nil
}

func (s *roomService) MarkRoomRead(ctx context.Context, req *connect.Request[agorav1.MarkRoomReadRequest]) (*connect.Response[agorav1.MarkRoomReadResponse], error) {
	m := req.Msg
	moved, err := s.h.rooms.MarkRoomRead(ctx, m.GetAgent(), m.GetRoom(), m.GetThroughId())
	if err != nil {
		return nil, toConnect(err)
	}
	if moved {
		s.h.changes.fire()
	}
	return connect.NewResponse(&agorav1.MarkRoomReadResponse{}), nil
}
