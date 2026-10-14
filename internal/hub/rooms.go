package hub

import (
	"context"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	agorav1 "github.com/vsem-azamat/agora/gen/agora/v1"
	"github.com/vsem-azamat/agora/internal/rooms"
)

type roomService struct{ h *Hub }

func (s *roomService) CreateRoom(ctx context.Context, req *connect.Request[agorav1.CreateRoomRequest]) (*connect.Response[agorav1.CreateRoomResponse], error) {
	if err := s.h.rooms.Create(ctx, req.Msg.GetName(), req.Msg.GetPurpose(), req.Msg.GetAgent()); err != nil {
		return nil, toConnect(err)
	}
	s.h.changes.fire()
	return connect.NewResponse(&agorav1.CreateRoomResponse{}), nil
}

func (s *roomService) ListRooms(ctx context.Context, req *connect.Request[agorav1.ListRoomsRequest]) (*connect.Response[agorav1.ListRoomsResponse], error) {
	list, err := s.h.rooms.List(ctx, req.Msg.GetAll())
	if err != nil {
		return nil, toConnect(err)
	}
	out := &agorav1.ListRoomsResponse{}
	for _, r := range list {
		pb := &agorav1.Room{Name: r.Name, Purpose: r.Purpose, CreatedBy: r.CreatedBy, CreatedAt: timestamppb.New(r.CreatedAt), Messages: int32(r.Messages), Scheduled: int32(r.Scheduled)}
		if !r.LastAt.IsZero() {
			pb.LastAt = timestamppb.New(r.LastAt)
		}
		if r.ArchivedBy != "" {
			pb.ArchivedBy, pb.ArchivedAt = r.ArchivedBy, timestamppb.New(r.ArchivedAt)
		}
		out.Rooms = append(out.Rooms, pb)
	}
	return connect.NewResponse(out), nil
}

func (s *roomService) ArchiveRoom(ctx context.Context, req *connect.Request[agorav1.ArchiveRoomRequest]) (*connect.Response[agorav1.ArchiveRoomResponse], error) {
	m := req.Msg
	if err := s.h.rooms.Archive(ctx, m.GetAgent(), m.GetRoom(), m.GetArchived()); err != nil {
		return nil, toConnect(err)
	}
	s.h.changes.fire()
	return connect.NewResponse(&agorav1.ArchiveRoomResponse{}), nil
}

func (s *roomService) Subscribe(ctx context.Context, req *connect.Request[agorav1.SubscribeRequest]) (*connect.Response[agorav1.SubscribeResponse], error) {
	var mode rooms.Mode
	if req.Msg.GetFollow() { // the mode is ignored when unfollowing
		var err error
		if mode, err = modeName(req.Msg.GetMode()); err != nil {
			return nil, toConnect(err)
		}
	}
	subs, err := s.h.rooms.Subscribe(ctx, req.Msg.GetAgent(), req.Msg.GetRooms(), req.Msg.GetFollow(), mode)
	if err != nil {
		return nil, toConnect(err)
	}
	s.h.changes.fire()
	names, pb := subscriptionsPB(subs)
	return connect.NewResponse(&agorav1.SubscribeResponse{Rooms: names, Subscriptions: pb}), nil
}

func (s *roomService) ListSubscriptions(ctx context.Context, req *connect.Request[agorav1.ListSubscriptionsRequest]) (*connect.Response[agorav1.ListSubscriptionsResponse], error) {
	subs, err := s.h.rooms.Subscriptions(ctx, req.Msg.GetAgent())
	if err != nil {
		return nil, toConnect(err)
	}
	names, pb := subscriptionsPB(subs)
	return connect.NewResponse(&agorav1.ListSubscriptionsResponse{Rooms: names, Subscriptions: pb}), nil
}

func (s *roomService) Post(ctx context.Context, req *connect.Request[agorav1.PostRequest]) (*connect.Response[agorav1.PostResponse], error) {
	m := req.Msg
	id, err := s.h.rooms.Send(ctx, rooms.Posting{
		Author: m.GetAgent(), Room: m.GetRoom(), Body: m.GetBody(), ReplyTo: m.GetReplyTo(),
		Thread: m.GetThread(), Also: m.GetAlso(), Note: m.GetNote(), Operator: fromOperator(ctx),
	})
	if err != nil {
		return nil, toConnect(err)
	}
	s.h.changes.fire()
	return connect.NewResponse(&agorav1.PostResponse{Id: id}), nil
}

func (s *roomService) History(ctx context.Context, req *connect.Request[agorav1.HistoryRequest]) (*connect.Response[agorav1.HistoryResponse], error) {
	m := req.Msg
	var msgs []rooms.Message
	var err error
	if m.GetThread() != 0 {
		msgs, err = s.h.rooms.ThreadHistory(ctx, m.GetThread(), int(m.GetLast()), m.GetAgent())
	} else {
		msgs, err = s.h.rooms.HistoryFor(ctx, m.GetRoom(), int(m.GetLast()), m.GetAgent())
	}
	if err != nil {
		return nil, toConnect(err)
	}
	return connect.NewResponse(&agorav1.HistoryResponse{Messages: messagesPB(msgs, s.h.bridges.DueAt)}), nil
}

func (s *roomService) Unread(ctx context.Context, req *connect.Request[agorav1.UnreadRequest]) (*connect.Response[agorav1.UnreadResponse], error) {
	m := req.Msg
	read := s.h.rooms.Take
	if m.GetPeek() {
		read = s.h.rooms.Unread
	}
	filter := rooms.Everything
	if m.GetMentionsOnly() {
		filter = rooms.Addressed
	}
	msgs, total, err := read(ctx, m.GetAgent(), filter, int(m.GetLimit()))
	if err != nil {
		return nil, toConnect(err)
	}
	return connect.NewResponse(&agorav1.UnreadResponse{Messages: messagesPB(msgs, s.h.bridges.DueAt), Total: int32(total)}), nil
}

func (s *roomService) UnreadByRoom(ctx context.Context, req *connect.Request[agorav1.UnreadByRoomRequest]) (*connect.Response[agorav1.UnreadByRoomResponse], error) {
	counts, err := s.h.rooms.UnreadByRoom(ctx, req.Msg.GetAgent())
	if err != nil {
		return nil, toConnect(err)
	}
	out := &agorav1.UnreadByRoomResponse{}
	for _, c := range counts {
		out.Rooms = append(out.Rooms, &agorav1.RoomUnread{Room: c.Room, Unread: int32(c.Unread), Addressed: int32(c.Addressed), Threads: int32(c.Threads)})
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

func (s *roomService) SetThreadTitle(ctx context.Context, req *connect.Request[agorav1.SetThreadTitleRequest]) (*connect.Response[agorav1.SetThreadTitleResponse], error) {
	m := req.Msg
	if err := s.h.rooms.SetThreadTitle(ctx, m.GetAgent(), m.GetMessageId(), m.GetTitle()); err != nil {
		return nil, toConnect(err)
	}
	s.h.changes.fire()
	return connect.NewResponse(&agorav1.SetThreadTitleResponse{}), nil
}

func (s *roomService) LeaveThread(ctx context.Context, req *connect.Request[agorav1.LeaveThreadRequest]) (*connect.Response[agorav1.LeaveThreadResponse], error) {
	m := req.Msg
	if err := s.h.rooms.LeaveThread(ctx, m.GetAgent(), m.GetMessageId()); err != nil {
		return nil, toConnect(err)
	}
	s.h.changes.fire()
	return connect.NewResponse(&agorav1.LeaveThreadResponse{}), nil
}

func (s *roomService) ListThreads(ctx context.Context, req *connect.Request[agorav1.ListThreadsRequest]) (*connect.Response[agorav1.ListThreadsResponse], error) {
	list, err := s.h.rooms.Threads(ctx, req.Msg.GetRoom(), req.Msg.GetAgent(), req.Msg.GetUnread())
	if err != nil {
		return nil, toConnect(err)
	}
	out := &agorav1.ListThreadsResponse{}
	for _, t := range list {
		out.Threads = append(out.Threads, &agorav1.Thread{Root: t.Root, Room: t.Room, Author: t.Author, Title: t.Title, Replies: int32(t.Replies), LastAt: timestamppb.New(t.LastAt), Unread: int32(t.Unread)})
	}
	return connect.NewResponse(out), nil
}
