package hub

import (
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"

	agorav1 "github.com/vsem-azamat/agora/gen/agora/v1"
	"github.com/vsem-azamat/agora/internal/agents"
	"github.com/vsem-azamat/agora/internal/queue"
	"github.com/vsem-azamat/agora/internal/store"
)

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
		var renamed *agents.FormerNameError
		if errors.As(err, &renamed) {
			agent = renamed.Current // the agent renamed itself while it waits: its place moved with it
			continue
		}
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
		case <-time.After(sweepEvery): // a safety net: re-check even if no change was signalled
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
	holder := m.GetHolder()
	switch {
	case holder == "":
		holder = m.GetAgent()
	case m.GetAgent() == "":
		return nil, toConnect(fmt.Errorf("%w: releasing %s needs the acting agent", store.ErrInvalid, holder))
	}
	ok, err := s.h.queue.Release(ctx, m.GetKey(), holder, m.GetAgent(), m.GetForce())
	if err != nil {
		return nil, toConnect(err)
	}
	if ok && m.GetAgent() != holder {
		s.h.log.Warn("forced release", "resource", m.GetKey(), "agent", holder, "by", m.GetAgent())
	}
	s.h.changes.fire()
	return connect.NewResponse(&agorav1.ReleaseResponse{Released: ok}), nil
}

func (s *resources) ListResources(ctx context.Context, req *connect.Request[agorav1.ListResourcesRequest]) (*connect.Response[agorav1.ListResourcesResponse], error) {
	rs, settled, err := s.h.queue.List(ctx, req.Msg.GetKey())
	if err != nil {
		return nil, toConnect(err)
	}
	if settled {
		s.h.changes.fire() // listing settled a queue and may have offered a slot
	}
	out := &agorav1.ListResourcesResponse{}
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
