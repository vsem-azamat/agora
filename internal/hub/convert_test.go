package hub

import (
	"testing"

	agorav1 "github.com/vsem-azamat/agora/gen/agora/v1"
	"github.com/vsem-azamat/agora/internal/agents"
	"github.com/vsem-azamat/agora/internal/governance"
)

// TestEveryEnumValueIsMapped fails when an API enum gains a value the hub does not map, or a
// mapping does not lead back to the same value.
func TestEveryEnumValueIsMapped(t *testing.T) {
	for n := range agorav1.VoteChoice_name {
		c := agorav1.VoteChoice(n)
		if c == agorav1.VoteChoice_VOTE_CHOICE_UNSPECIFIED {
			continue
		}
		name := choiceName(c)
		if name == "" || voteChoices[governance.Choice(name)] != c {
			t.Errorf("vote choice %v: name %q", c, name)
		}
	}
	for n := range agorav1.ProposalState_name {
		s := agorav1.ProposalState(n)
		if s == agorav1.ProposalState_PROPOSAL_STATE_UNSPECIFIED {
			continue
		}
		name := stateName(s)
		if name == "" || proposalStates[governance.State(name)] != s {
			t.Errorf("proposal state %v: name %q", s, name)
		}
	}
	for n := range agorav1.SubscriptionMode_name {
		m := agorav1.SubscriptionMode(n)
		name, err := modeName(m)
		switch {
		case m == agorav1.SubscriptionMode_SUBSCRIPTION_MODE_UNSPECIFIED:
			if name != "" || err != nil {
				t.Errorf("unspecified mode: %q %v", name, err)
			}
		case err != nil || subscriptionModes[name] != m:
			t.Errorf("subscription mode %v: name %q err %v", m, name, err)
		}
	}
	if _, err := modeName(agorav1.SubscriptionMode(99)); err == nil {
		t.Error("unknown subscription mode accepted")
	}
	for n := range agorav1.CiState_name {
		s := agorav1.CiState(n)
		if s == agorav1.CiState_CI_STATE_UNSPECIFIED {
			continue
		}
		found := 0
		for _, v := range ciStates {
			if v == s {
				found++
			}
		}
		if found != 1 {
			t.Errorf("CI state %v is mapped %d times", s, found)
		}
	}
	for n := range agorav1.SessionState_name {
		s := agorav1.SessionState(n)
		if s == agorav1.SessionState_SESSION_STATE_UNSPECIFIED || s == agorav1.SessionState_SESSION_STATE_ENDED {
			continue // a profile never shows an ended session
		}
		found := 0
		for _, v := range profileSessions {
			if v == s {
				found++
			}
		}
		if found != 1 {
			t.Errorf("profile session state %v is mapped %d times", s, found)
		}
	}
}

func TestProfilesAndProposalsCarryEveryState(t *testing.T) {
	p := profilePB(agents.Profile{SessionState: "busy", CI: map[int]string{57: "red", 58: "conflict", 59: "green"}})
	if p.GetSession() != agorav1.SessionState_SESSION_STATE_BUSY {
		t.Errorf("session %v", p.GetSession())
	}
	want := map[int32]agorav1.CiState{57: agorav1.CiState_CI_STATE_RED, 58: agorav1.CiState_CI_STATE_CONFLICT, 59: agorav1.CiState_CI_STATE_GREEN}
	for n, s := range want {
		if p.GetCiState()[n] != s {
			t.Errorf("#%d: %v, want %v", n, p.GetCiState()[n], s)
		}
	}
	for state, pb := range map[governance.State]agorav1.ProposalState{
		governance.Rejected:  agorav1.ProposalState_PROPOSAL_STATE_REJECTED,
		governance.Withdrawn: agorav1.ProposalState_PROPOSAL_STATE_WITHDRAWN,
	} {
		out := proposalPB(governance.Proposal{State: state, Votes: []governance.Vote{{Agent: "builder", Choice: governance.Abstain}}})
		if out.GetProposalState() != pb || out.GetVotes()[0].GetVoteChoice() != agorav1.VoteChoice_VOTE_CHOICE_ABSTAIN {
			t.Errorf("%s: %v", state, out)
		}
	}
}
