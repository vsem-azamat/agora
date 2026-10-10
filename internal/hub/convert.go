package hub

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	agorav1 "github.com/vsem-azamat/agora/gen/agora/v1"
	"github.com/vsem-azamat/agora/internal/agents"
	"github.com/vsem-azamat/agora/internal/governance"
	"github.com/vsem-azamat/agora/internal/pullrequests"
	"github.com/vsem-azamat/agora/internal/queue"
	"github.com/vsem-azamat/agora/internal/rooms"
	"github.com/vsem-azamat/agora/internal/sessions"
	"github.com/vsem-azamat/agora/internal/store"
)

func toConnect(err error) error {
	var forbidden *queue.ForbiddenError
	var former *agents.FormerNameError
	switch {
	case errors.As(err, &former): // before ErrTaken and ErrUnknown, which it wraps
		return connect.NewError(connect.CodeFailedPrecondition, err)
	case errors.Is(err, store.ErrInvalid):
		return connect.NewError(connect.CodeInvalidArgument, err)
	case errors.Is(err, queue.ErrNotQueued):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, queue.ErrNotYourTurn):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	case errors.As(err, &forbidden), errors.Is(err, rooms.ErrBoardOnly):
		return connect.NewError(connect.CodePermissionDenied, err)
	case errors.Is(err, sessions.ErrNameTaken), errors.Is(err, agents.ErrTaken):
		return connect.NewError(connect.CodeAlreadyExists, err)
	case errors.Is(err, agents.ErrUnknown), errors.Is(err, rooms.ErrNotFound), errors.Is(err, governance.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, rooms.ErrExists):
		return connect.NewError(connect.CodeAlreadyExists, err)
	case errors.Is(err, governance.ErrClosed):
		return connect.NewError(connect.CodeFailedPrecondition, err)
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

var sessionStates = map[sessions.State]agorav1.SessionState{
	sessions.Busy:  agorav1.SessionState_SESSION_STATE_BUSY,
	sessions.Idle:  agorav1.SessionState_SESSION_STATE_IDLE,
	sessions.Ended: agorav1.SessionState_SESSION_STATE_ENDED,
}

// profileSessions maps the session state of a profile to the API's.
var profileSessions = map[string]agorav1.SessionState{
	string(sessions.Busy): agorav1.SessionState_SESSION_STATE_BUSY,
	string(sessions.Idle): agorav1.SessionState_SESSION_STATE_IDLE,
	agents.Offline:        agorav1.SessionState_SESSION_STATE_OFFLINE,
}

var ciStates = map[string]agorav1.CiState{
	string(pullrequests.Green):    agorav1.CiState_CI_STATE_GREEN,
	string(pullrequests.Red):      agorav1.CiState_CI_STATE_RED,
	string(pullrequests.Conflict): agorav1.CiState_CI_STATE_CONFLICT,
}

func profilePB(p agents.Profile) *agorav1.Profile {
	out := &agorav1.Profile{
		Name: p.Name, Kind: p.Kind, Project: p.Project, Task: p.Task, Status: p.Status, Cwd: p.CWD, Branch: p.Branch,
		About: p.About, Icon: p.Icon, Pigment: p.Pigment, JoinedAt: timestamppb.New(p.JoinedAt), UpdatedAt: timestamppb.New(p.UpdatedAt),
		Session: profileSessions[p.SessionState], Active: p.Active,
	}
	for _, n := range p.PRs {
		out.Prs = append(out.Prs, int32(n))
	}
	for _, n := range p.FoundPRs {
		out.FoundPrs = append(out.FoundPrs, int32(n))
	}
	for _, f := range p.Formerly {
		out.Formerly = append(out.Formerly, &agorav1.FormerName{Name: f.Name, RenamedAt: timestamppb.New(f.At)})
	}
	if len(p.CI) > 0 {
		out.CiState = make(map[int32]agorav1.CiState, len(p.CI))
		for n, state := range p.CI {
			out.CiState[int32(n)] = ciStates[state]
		}
	}
	return out
}

func messagesPB(msgs []rooms.Message) []*agorav1.Message {
	out := make([]*agorav1.Message, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, &agorav1.Message{Id: m.ID, Room: m.Room, Author: m.Author, Body: m.Body, ReplyTo: m.ReplyTo, At: timestamppb.New(m.At), Addressed: m.Addressed})
	}
	return out
}

var proposalStates = map[governance.State]agorav1.ProposalState{
	governance.Open:      agorav1.ProposalState_PROPOSAL_STATE_OPEN,
	governance.Accepted:  agorav1.ProposalState_PROPOSAL_STATE_ACCEPTED,
	governance.Rejected:  agorav1.ProposalState_PROPOSAL_STATE_REJECTED,
	governance.Withdrawn: agorav1.ProposalState_PROPOSAL_STATE_WITHDRAWN,
}

var voteChoices = map[governance.Choice]agorav1.VoteChoice{
	governance.Yes:     agorav1.VoteChoice_VOTE_CHOICE_YES,
	governance.No:      agorav1.VoteChoice_VOTE_CHOICE_NO,
	governance.Abstain: agorav1.VoteChoice_VOTE_CHOICE_ABSTAIN,
}

// choiceName is the governance choice for an API vote choice, "" for an unknown one.
func choiceName(c agorav1.VoteChoice) string {
	for name, v := range voteChoices {
		if v == c {
			return string(name)
		}
	}
	return ""
}

// stateName is the governance state for an API proposal state, "" for an unknown one.
func stateName(s agorav1.ProposalState) string {
	for name, v := range proposalStates {
		if v == s {
			return string(name)
		}
	}
	return ""
}

func proposalPB(p governance.Proposal) *agorav1.Proposal {
	out := &agorav1.Proposal{
		Id: p.ID, Title: p.Title, Body: p.Body, Author: p.Author, CreatedAt: timestamppb.New(p.CreatedAt), ClosedBy: p.ClosedBy,
		ProposalState: proposalStates[p.State],
	}
	if !p.ClosedAt.IsZero() {
		out.ClosedAt = timestamppb.New(p.ClosedAt)
	}
	for _, v := range p.Votes {
		out.Votes = append(out.Votes, &agorav1.ProposalVote{
			Agent: v.Agent, Reason: v.Reason, At: timestamppb.New(v.At), VoteChoice: voteChoices[v.Choice],
		})
	}
	return out
}
