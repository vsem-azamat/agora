package hub

import (
	"context"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	agorav1 "github.com/vsem-azamat/agora/gen/agora/v1"
)

type governanceService struct{ h *Hub }

func (s *governanceService) Propose(ctx context.Context, req *connect.Request[agorav1.ProposeRequest]) (*connect.Response[agorav1.ProposeResponse], error) {
	id, err := s.h.gov.Propose(ctx, req.Msg.GetAuthor(), req.Msg.GetTitle(), req.Msg.GetBody())
	if err != nil {
		return nil, toConnect(err)
	}
	s.h.changes.fire()
	return connect.NewResponse(&agorav1.ProposeResponse{Id: id}), nil
}

func (s *governanceService) Vote(ctx context.Context, req *connect.Request[agorav1.VoteRequest]) (*connect.Response[agorav1.VoteResponse], error) {
	m := req.Msg
	choice := m.GetChoice() //nolint:staticcheck // older clients send the choice as a string
	if c := m.GetVoteChoice(); c != agorav1.VoteChoice_VOTE_CHOICE_UNSPECIFIED {
		choice = choiceName(c)
	}
	recorded, err := s.h.gov.Cast(ctx, m.GetAgent(), m.GetProposalId(), choice, m.GetReason())
	if err != nil {
		return nil, toConnect(err)
	}
	s.h.changes.fire()
	return connect.NewResponse(&agorav1.VoteResponse{
		Choice:     string(recorded), //nolint:staticcheck // the deprecated string stays filled for older clients
		VoteChoice: voteChoices[recorded],
	}), nil
}

func (s *governanceService) CloseProposal(ctx context.Context, req *connect.Request[agorav1.CloseProposalRequest]) (*connect.Response[agorav1.CloseProposalResponse], error) {
	m := req.Msg
	state := m.GetState() //nolint:staticcheck // older clients send the state as a string
	if st := m.GetProposalState(); st != agorav1.ProposalState_PROPOSAL_STATE_UNSPECIFIED {
		state = stateName(st)
	}
	if err := s.h.gov.Close(ctx, m.GetAgent(), m.GetProposalId(), state); err != nil {
		return nil, toConnect(err)
	}
	s.h.changes.fire()
	return connect.NewResponse(&agorav1.CloseProposalResponse{}), nil
}

func (s *governanceService) ListProposals(ctx context.Context, req *connect.Request[agorav1.ListProposalsRequest]) (*connect.Response[agorav1.ListProposalsResponse], error) {
	list, err := s.h.gov.List(ctx, req.Msg.GetAll())
	if err != nil {
		return nil, toConnect(err)
	}
	out := &agorav1.ListProposalsResponse{}
	for _, p := range list {
		out.Proposals = append(out.Proposals, proposalPB(p))
	}
	return connect.NewResponse(out), nil
}

func (s *governanceService) GetProposal(ctx context.Context, req *connect.Request[agorav1.GetProposalRequest]) (*connect.Response[agorav1.GetProposalResponse], error) {
	p, err := s.h.gov.Get(ctx, req.Msg.GetId())
	if err != nil {
		return nil, toConnect(err)
	}
	return connect.NewResponse(&agorav1.GetProposalResponse{Proposal: proposalPB(p)}), nil
}

func (s *governanceService) GetCharter(ctx context.Context, _ *connect.Request[agorav1.GetCharterRequest]) (*connect.Response[agorav1.GetCharterResponse], error) {
	c, err := s.h.gov.Charter(ctx)
	if err != nil {
		return nil, toConnect(err)
	}
	out := &agorav1.GetCharterResponse{Body: c.Body, ChangedBy: c.ChangedBy, ProposalId: c.ProposalID}
	if !c.ChangedAt.IsZero() {
		out.ChangedAt = timestamppb.New(c.ChangedAt)
	}
	return connect.NewResponse(out), nil
}

func (s *governanceService) SetCharter(ctx context.Context, req *connect.Request[agorav1.SetCharterRequest]) (*connect.Response[agorav1.SetCharterResponse], error) {
	m := req.Msg
	if err := s.h.gov.SetCharter(ctx, m.GetAgent(), m.GetProposalId(), m.GetBody()); err != nil {
		return nil, toConnect(err)
	}
	s.h.changes.fire()
	return connect.NewResponse(&agorav1.SetCharterResponse{}), nil
}
