// Package hub serves Agora's API: the ConnectRPC services over a local socket, backed by SQLite.
package hub

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	agorav1 "github.com/vsem-azamat/agora/gen/agora/v1"
	"github.com/vsem-azamat/agora/gen/agora/v1/agorav1connect"
	"github.com/vsem-azamat/agora/internal/agents"
	"github.com/vsem-azamat/agora/internal/proc"
	"github.com/vsem-azamat/agora/internal/queue"
	"github.com/vsem-azamat/agora/internal/rooms"
	"github.com/vsem-azamat/agora/internal/sessions"
)

// SweepEvery is how often the hub applies expired leases and claim deadlines.
const SweepEvery = time.Second

// Hub holds the services and the change signal that wakes streaming waiters.
type Hub struct {
	queue    *queue.Queue
	sessions *sessions.Sessions
	agents   *agents.Agents
	rooms    *rooms.Rooms
	alive    func(pid int, start int64) bool
	changes  *signal
	log      *slog.Logger
}

// New returns a hub over the given domain services.
func New(q *queue.Queue, s *sessions.Sessions, a *agents.Agents, r *rooms.Rooms, log *slog.Logger) *Hub {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Hub{queue: q, sessions: s, agents: a, rooms: r, alive: proc.Alive, changes: newSignal(), log: log}
}

// Open builds a hub over db with the given clock (time.Now when nil).
func Open(db *sql.DB, now func() time.Time, log *slog.Logger) *Hub {
	q := queue.New(db, now)
	r := rooms.New(db, now)
	return New(q, sessions.New(db, q, r, now), agents.New(db, q, now), r, log)
}

// Handler returns the HTTP handler with every service mounted.
func (h *Hub) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle(agorav1connect.NewResourceServiceHandler(&resources{h}))
	mux.Handle(agorav1connect.NewSessionServiceHandler(&sessionService{h}))
	mux.Handle(agorav1connect.NewAgentServiceHandler(&agentService{h}))
	mux.Handle(agorav1connect.NewRoomServiceHandler(&roomService{h}))
	return mux
}

// Sweep applies expired leases and claim deadlines every SweepEvery until ctx ends.
func (h *Hub) Sweep(ctx context.Context) {
	t := time.NewTicker(SweepEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			changed, err := h.queue.Sweep(ctx)
			if err != nil && ctx.Err() == nil {
				h.log.Error("sweep", "err", err)
			}
			ended, err := h.sessions.EndDead(ctx, h.alive)
			if err != nil && ctx.Err() == nil {
				h.log.Error("sweep sessions", "err", err)
			}
			if len(ended) > 0 {
				h.log.Info("sessions ended: process gone", "sessions", ended)
			}
			if len(changed) > 0 {
				h.log.Info("leases or offers expired", "resources", changed)
			}
			if len(changed) > 0 || len(ended) > 0 {
				h.changes.fire()
			}
		}
	}
}

// MaxSocketPath is the longest unix socket path that works on every supported system.
const MaxSocketPath = 104

// Listen opens the hub's unix socket at path, readable and writable by the owner only. An
// exclusive lock on path+".lock" keeps a second hub away, so a socket left by a hub that no
// longer runs can be replaced safely; a path that is not a socket is never removed.
func Listen(path string) (net.Listener, error) {
	if len(path) > MaxSocketPath {
		return nil, fmt.Errorf("socket path is %d bytes; unix sockets allow at most %d: %s", len(path), MaxSocketPath, path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, fmt.Errorf("a hub is already running on %s", path)
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			lock.Close()
			return nil, fmt.Errorf("%s exists and is not a socket; choose another --socket", path)
		}
		if err := os.Remove(path); err != nil { // a socket nobody serves: we hold the lock
			lock.Close()
			return nil, err
		}
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		lock.Close()
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		l.Close()
		lock.Close()
		return nil, err
	}
	return &lockedListener{Listener: l, lock: lock}, nil
}

// lockedListener releases the hub lock when the listener closes.
type lockedListener struct {
	net.Listener
	lock *os.File
	once sync.Once
}

func (l *lockedListener) Close() error {
	err := l.Listener.Close()
	l.once.Do(func() { l.lock.Close() })
	return err
}

// Serve runs the hub on l until ctx ends. On shutdown it ends open streams, lets unary calls
// finish for up to 5 seconds, and returns only after the server has stopped.
func (h *Hub) Serve(ctx context.Context, l net.Listener) error {
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)
	base, endStreams := context.WithCancel(context.Background())
	defer endStreams()
	srv := &http.Server{
		Handler:           h.Handler(),
		Protocols:         protocols,
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return base },
	}
	sweepCtx, stopSweep := context.WithCancel(ctx)
	defer stopSweep()
	go h.Sweep(sweepCtx)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		<-ctx.Done()
		endStreams() // waiting streams return; clients reconnect to the next hub
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
	}()
	err := srv.Serve(l)
	if errors.Is(err, http.ErrServerClosed) {
		<-stopped
		return nil
	}
	return err
}

// signal lets any number of goroutines wait for the next change.
type signal struct {
	mu sync.Mutex
	ch chan struct{}
}

func newSignal() *signal { return &signal{ch: make(chan struct{})} }

func (s *signal) wait() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ch
}

func (s *signal) fire() {
	s.mu.Lock()
	close(s.ch)
	s.ch = make(chan struct{})
	s.mu.Unlock()
}

// --- ResourceService ----------------------------------------------------------

type resources struct{ h *Hub }

func (s *resources) Join(ctx context.Context, req *connect.Request[agorav1.JoinRequest]) (*connect.Response[agorav1.JoinResponse], error) {
	m := req.Msg
	e, res, err := s.h.queue.Join(ctx, m.GetKey(), m.GetAgent(), m.GetNote(), m.GetLease().AsDuration(), m.GetNoWait())
	if err != nil {
		return nil, toConnect(err)
	}
	s.h.changes.fire() // even a refused lock may have settled the queue
	return connect.NewResponse(&agorav1.JoinResponse{Entry: entryPB(e), Resource: resourcePB(res)}), nil
}

func (s *resources) Wait(ctx context.Context, req *connect.Request[agorav1.WaitRequest], stream *connect.ServerStream[agorav1.WaitResponse]) error {
	key, agent := req.Msg.GetKey(), req.Msg.GetAgent()
	var last *queue.Entry
	for {
		changed := s.h.changes.wait() // before reading state, so no change is missed
		e, err := s.h.queue.Claim(ctx, key, agent)
		if err != nil {
			return toConnect(err)
		}
		if last == nil || e.State != last.State || e.Position != last.Position {
			if err := stream.Send(&agorav1.WaitResponse{Entry: entryPB(e)}); err != nil {
				return err
			}
		}
		if e.State == queue.Held {
			return nil
		}
		last = e
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		case <-time.After(SweepEvery): // a safety net: re-check even if no change was signalled
		}
	}
}

func (s *resources) Renew(ctx context.Context, req *connect.Request[agorav1.RenewRequest]) (*connect.Response[agorav1.RenewResponse], error) {
	e, err := s.h.queue.Renew(ctx, req.Msg.GetKey(), req.Msg.GetAgent())
	if err != nil {
		return nil, toConnect(err)
	}
	s.h.changes.fire()
	return connect.NewResponse(&agorav1.RenewResponse{Entry: entryPB(e)}), nil
}

func (s *resources) Release(ctx context.Context, req *connect.Request[agorav1.ReleaseRequest]) (*connect.Response[agorav1.ReleaseResponse], error) {
	m := req.Msg
	ok, err := s.h.queue.Release(ctx, m.GetKey(), m.GetAgent(), m.GetActor(), m.GetForce())
	if err != nil {
		return nil, toConnect(err)
	}
	if ok && m.GetActor() != "" && m.GetActor() != m.GetAgent() {
		s.h.log.Warn("forced release", "resource", m.GetKey(), "agent", m.GetAgent(), "by", m.GetActor())
	}
	s.h.changes.fire()
	return connect.NewResponse(&agorav1.ReleaseResponse{Released: ok}), nil
}

func (s *resources) List(ctx context.Context, req *connect.Request[agorav1.ListRequest]) (*connect.Response[agorav1.ListResponse], error) {
	rs, err := s.h.queue.List(ctx, req.Msg.GetKey())
	if err != nil {
		return nil, toConnect(err)
	}
	s.h.changes.fire() // listing settles queues and may have offered a slot
	out := &agorav1.ListResponse{}
	for _, r := range rs {
		out.Resources = append(out.Resources, resourcePB(r))
	}
	return connect.NewResponse(out), nil
}

func (s *resources) SetSlots(ctx context.Context, req *connect.Request[agorav1.SetSlotsRequest]) (*connect.Response[agorav1.SetSlotsResponse], error) {
	r, err := s.h.queue.SetSlots(ctx, req.Msg.GetKey(), int(req.Msg.GetSlots()))
	if err != nil {
		return nil, toConnect(err)
	}
	s.h.changes.fire()
	return connect.NewResponse(&agorav1.SetSlotsResponse{Resource: resourcePB(r)}), nil
}

func toConnect(err error) error {
	var forbidden *queue.ForbiddenError
	switch {
	case errors.Is(err, queue.ErrInvalid):
		return connect.NewError(connect.CodeInvalidArgument, err)
	case errors.Is(err, queue.ErrNotQueued):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, queue.ErrNotYourTurn):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	case errors.As(err, &forbidden):
		return connect.NewError(connect.CodePermissionDenied, err)
	case errors.Is(err, sessions.ErrInvalid):
		return connect.NewError(connect.CodeInvalidArgument, err)
	case errors.Is(err, sessions.ErrNameTaken):
		return connect.NewError(connect.CodeAlreadyExists, err)
	case errors.Is(err, agents.ErrInvalid):
		return connect.NewError(connect.CodeInvalidArgument, err)
	case errors.Is(err, agents.ErrUnknown), errors.Is(err, rooms.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, rooms.ErrInvalid):
		return connect.NewError(connect.CodeInvalidArgument, err)
	case errors.Is(err, rooms.ErrExists):
		return connect.NewError(connect.CodeAlreadyExists, err)
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return connect.NewError(connect.CodeCanceled, err)
	}
	return connect.NewError(connect.CodeInternal, err)
}

var states = map[queue.State]agorav1.EntryState{
	queue.Waiting: agorav1.EntryState_ENTRY_STATE_WAITING,
	queue.Offered: agorav1.EntryState_ENTRY_STATE_OFFERED,
	queue.Held:    agorav1.EntryState_ENTRY_STATE_HELD,
}

func entryPB(e *queue.Entry) *agorav1.Entry {
	if e == nil {
		return nil
	}
	out := &agorav1.Entry{
		Key: e.Key, Agent: e.Agent, Note: e.Note, State: states[e.State], Position: int32(e.Position),
		JoinedAt: timestamppb.New(e.JoinedAt), Lease: durationpb.New(e.Lease),
	}
	if !e.Expires.IsZero() {
		out.ExpiresAt = timestamppb.New(e.Expires)
	}
	return out
}

func resourcePB(r queue.Resource) *agorav1.Resource {
	if r.Key == "" {
		return nil
	}
	out := &agorav1.Resource{Key: r.Key, Slots: int32(r.Slots)}
	for i := range r.Entries {
		out.Entries = append(out.Entries, entryPB(&r.Entries[i]))
	}
	return out
}

// --- SessionService ---------------------------------------------------------------

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
	return connect.NewResponse(&agorav1.JoinNameResponse{Bound: bound}), nil
}

func (s *sessionService) Resolve(ctx context.Context, req *connect.Request[agorav1.ResolveRequest]) (*connect.Response[agorav1.ResolveResponse], error) {
	agent, err := s.h.sessions.Resolve(ctx, req.Msg.GetSessionId())
	if err != nil {
		return nil, toConnect(err)
	}
	return connect.NewResponse(&agorav1.ResolveResponse{Agent: agent}), nil
}

var sessionStates = map[sessions.State]agorav1.SessionState{
	sessions.Busy:  agorav1.SessionState_SESSION_STATE_BUSY,
	sessions.Idle:  agorav1.SessionState_SESSION_STATE_IDLE,
	sessions.Ended: agorav1.SessionState_SESSION_STATE_ENDED,
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

// --- AgentService -----------------------------------------------------------------

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
	p, err := s.h.agents.Update(ctx, m.GetName(), u)
	if err != nil {
		return nil, toConnect(err)
	}
	return connect.NewResponse(&agorav1.UpdateProfileResponse{Profile: profilePB(p)}), nil
}

func (s *agentService) Leave(ctx context.Context, req *connect.Request[agorav1.LeaveRequest]) (*connect.Response[agorav1.LeaveResponse], error) {
	released, err := s.h.agents.Leave(ctx, req.Msg.GetName())
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

func profilePB(p agents.Profile) *agorav1.Profile {
	out := &agorav1.Profile{
		Name: p.Name, Kind: p.Kind, Project: p.Project, Task: p.Task, Status: p.Status, Cwd: p.CWD, Branch: p.Branch,
		About: p.About, JoinedAt: timestamppb.New(p.JoinedAt), UpdatedAt: timestamppb.New(p.UpdatedAt),
		SessionState: p.SessionState, Active: p.Active,
	}
	for _, n := range p.PRs {
		out.Prs = append(out.Prs, int32(n))
	}
	return out
}

// --- RoomService ------------------------------------------------------------------

type roomService struct{ h *Hub }

func (s *roomService) CreateRoom(ctx context.Context, req *connect.Request[agorav1.CreateRoomRequest]) (*connect.Response[agorav1.CreateRoomResponse], error) {
	if err := s.h.rooms.Create(ctx, req.Msg.GetName(), req.Msg.GetPurpose(), req.Msg.GetCreator()); err != nil {
		return nil, toConnect(err)
	}
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
	return connect.NewResponse(&agorav1.SubscribeResponse{Rooms: followed}), nil
}

func (s *roomService) Post(ctx context.Context, req *connect.Request[agorav1.PostRequest]) (*connect.Response[agorav1.PostResponse], error) {
	m := req.Msg
	if m.GetAuthor() == rooms.Board {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("only the board itself posts as agora"))
	}
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

func messagesPB(msgs []rooms.Message) []*agorav1.Message {
	out := make([]*agorav1.Message, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, &agorav1.Message{Id: m.ID, Room: m.Room, Author: m.Author, Body: m.Body, ReplyTo: m.ReplyTo, At: timestamppb.New(m.At), Addressed: m.Addressed})
	}
	return out
}
