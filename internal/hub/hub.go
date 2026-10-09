// Package hub serves Agora's API: the ConnectRPC services over a local socket, backed by SQLite.
package hub

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	agorav1 "github.com/vsem-azamat/agora/gen/agora/v1"
	"github.com/vsem-azamat/agora/gen/agora/v1/agorav1connect"
	"github.com/vsem-azamat/agora/internal/queue"
)

// SweepEvery is how often the hub applies expired leases and claim deadlines.
const SweepEvery = time.Second

// Hub holds the services and the change signal that wakes streaming waiters.
type Hub struct {
	queue   *queue.Queue
	changes *signal
	log     *slog.Logger
}

// New returns a hub over q.
func New(q *queue.Queue, log *slog.Logger) *Hub {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Hub{queue: q, changes: newSignal(), log: log}
}

// Handler returns the HTTP handler with every service mounted.
func (h *Hub) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle(agorav1connect.NewResourceServiceHandler(&resources{h}))
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
			if err != nil {
				if ctx.Err() == nil {
					h.log.Error("sweep", "err", err)
				}
				continue
			}
			if len(changed) > 0 {
				h.log.Info("leases or offers expired", "resources", changed)
				h.changes.fire()
			}
		}
	}
}

// Listen opens the hub's unix socket at path, readable and writable by the owner only. A stale
// socket left by a hub that is no longer running is replaced; a live one is an error.
func Listen(path string) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); err == nil {
		if c, err := net.DialTimeout("unix", path, time.Second); err == nil {
			c.Close()
			return nil, fmt.Errorf("a hub is already running on %s", path)
		}
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		l.Close()
		return nil, err
	}
	return l, nil
}

// Serve runs the hub on l until ctx ends.
func (h *Hub) Serve(ctx context.Context, l net.Listener) error {
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)
	srv := &http.Server{Handler: h.Handler(), Protocols: protocols, ReadHeaderTimeout: 10 * time.Second}
	sweepCtx, stopSweep := context.WithCancel(ctx)
	defer stopSweep()
	go h.Sweep(sweepCtx)
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
	}()
	if err := srv.Serve(l); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
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
	if e != nil {
		s.h.changes.fire()
	}
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
	if ok {
		if m.GetActor() != "" && m.GetActor() != m.GetAgent() {
			s.h.log.Warn("forced release", "resource", m.GetKey(), "agent", m.GetAgent(), "by", m.GetActor())
		}
		s.h.changes.fire()
	}
	return connect.NewResponse(&agorav1.ReleaseResponse{Released: ok}), nil
}

func (s *resources) List(ctx context.Context, req *connect.Request[agorav1.ListRequest]) (*connect.Response[agorav1.ListResponse], error) {
	rs, err := s.h.queue.List(ctx, req.Msg.GetKey())
	if err != nil {
		return nil, toConnect(err)
	}
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
