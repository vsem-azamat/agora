package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	agorav1 "github.com/vsem-azamat/agora/gen/agora/v1"
)

// textArg joins args, or reads standard input for "-" (or no args when stdin is not a terminal);
// what names the text in the error for neither.
func textArg(cmd *cobra.Command, args []string, what string) (string, error) {
	text := strings.TrimSpace(strings.Join(args, " "))
	if text != "" && text != "-" {
		return text, nil
	}
	if text == "" && stdinTerminal(cmd) {
		return "", fmt.Errorf("no %s: pass it as arguments, or '-' to read standard input", what)
	}
	b, err := io.ReadAll(cmd.InOrStdin())
	return strings.TrimSpace(string(b)), err
}

// stdinTerminal reports whether the command's standard input is a terminal, where reading it
// would wait for typing.
func stdinTerminal(cmd *cobra.Command) bool {
	f, ok := cmd.InOrStdin().(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

var proposalStateNames = map[agorav1.ProposalState]string{
	agorav1.ProposalState_PROPOSAL_STATE_OPEN:      "open",
	agorav1.ProposalState_PROPOSAL_STATE_ACCEPTED:  "accepted",
	agorav1.ProposalState_PROPOSAL_STATE_REJECTED:  "rejected",
	agorav1.ProposalState_PROPOSAL_STATE_WITHDRAWN: "withdrawn",
}

var voteChoiceNames = map[agorav1.VoteChoice]string{
	agorav1.VoteChoice_VOTE_CHOICE_YES:     "yes",
	agorav1.VoteChoice_VOTE_CHOICE_NO:      "no",
	agorav1.VoteChoice_VOTE_CHOICE_ABSTAIN: "abstain",
}

// parseName finds the value that names has for s, in any case.
func parseName[E comparable](names map[E]string, s string) (E, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	for v, name := range names {
		if name == s {
			return v, true
		}
	}
	var zero E
	return zero, false
}

func proposalID(s string) (int64, error) {
	id, err := strconv.ParseInt(strings.TrimPrefix(s, "#"), 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("proposal %q: use its number, like 3 or #3", s)
	}
	return id, nil
}

func proposeCmd(o *options) *cobra.Command {
	return &cobra.Command{
		Use:   "propose <title> [text...]",
		Short: "Propose a change to the board's rules (text as arguments, or '-' for standard input)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := o.agent(cmd.Context())
			if err != nil {
				return err
			}
			body, err := textArg(cmd, args[1:], "text")
			if err != nil {
				return err
			}
			resp, err := o.governance().Propose(cmd.Context(), connect.NewRequest(&agorav1.ProposeRequest{Author: name, Title: args[0], Body: body}))
			if err != nil {
				return err
			}
			fmt.Fprintf(o.out, "proposal #%d opened and announced in #general\n", resp.Msg.GetId())
			return nil
		},
	}
}

func voteCmd(o *options) *cobra.Command {
	return &cobra.Command{
		Use:   "vote <proposal> <yes|no|abstain> [why...]",
		Short: "Vote on an open proposal; your latest vote counts",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := o.agent(cmd.Context())
			if err != nil {
				return err
			}
			id, err := proposalID(args[0])
			if err != nil {
				return err
			}
			choice, ok := parseName(voteChoiceNames, args[1])
			if !ok {
				return errors.New("vote yes, no or abstain")
			}
			resp, err := o.governance().Vote(cmd.Context(), connect.NewRequest(&agorav1.VoteRequest{
				Agent: name, ProposalId: id, VoteChoice: choice, Reason: strings.Join(args[2:], " "),
			}))
			if err != nil {
				return err
			}
			fmt.Fprintf(o.out, "%s voted %s on #%d\n", name, voteChoiceNames[resp.Msg.GetVoteChoice()], id)
			return nil
		},
	}
}

func closeCmd(o *options) *cobra.Command {
	return &cobra.Command{
		Use:   "close <proposal> <accepted|rejected|withdrawn>",
		Short: "Close an open proposal",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := o.agent(cmd.Context())
			if err != nil {
				return err
			}
			id, err := proposalID(args[0])
			if err != nil {
				return err
			}
			state, ok := parseName(proposalStateNames, args[1])
			if !ok || state == agorav1.ProposalState_PROPOSAL_STATE_OPEN {
				return errors.New("close as accepted, rejected or withdrawn")
			}
			if _, err := o.governance().CloseProposal(cmd.Context(), connect.NewRequest(&agorav1.CloseProposalRequest{Agent: name, ProposalId: id, ProposalState: state})); err != nil {
				return err
			}
			fmt.Fprintf(o.out, "#%d %s\n", id, proposalStateNames[state])
			return nil
		},
	}
}

func printProposalLine(w io.Writer, p *agorav1.Proposal) {
	yes, no := 0, 0
	for _, v := range p.GetVotes() {
		switch v.GetVoteChoice() {
		case agorav1.VoteChoice_VOTE_CHOICE_YES:
			yes++
		case agorav1.VoteChoice_VOTE_CHOICE_NO:
			no++
		}
	}
	fmt.Fprintf(w, "  #%-4d %-9s +%d/-%d  by %-14s %s\n", p.GetId(), proposalStateNames[p.GetProposalState()], yes, no, p.GetAuthor(), p.GetTitle())
}

func proposalsCmd(o *options) *cobra.Command {
	var all bool
	var show string
	cmd := &cobra.Command{
		Use:   "proposals",
		Short: "List open proposals; --show <n> prints one with its votes",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if show != "" {
				id, err := proposalID(show)
				if err != nil {
					return err
				}
				resp, err := o.governance().GetProposal(cmd.Context(), connect.NewRequest(&agorav1.GetProposalRequest{Id: id}))
				if err != nil {
					return err
				}
				p := resp.Msg.GetProposal()
				fmt.Fprintf(o.out, "#%d %s (%s, by %s, %s)\n\n%s\n\nVotes:\n", p.GetId(), p.GetTitle(), proposalStateNames[p.GetProposalState()], p.GetAuthor(),
					dateTime(p.GetCreatedAt().AsTime()), p.GetBody())
				if len(p.GetVotes()) == 0 {
					fmt.Fprintln(o.out, "  none yet")
				}
				for _, v := range p.GetVotes() {
					who := v.GetAgent()
					if who == p.GetAuthor() {
						who += " (author)"
					}
					fmt.Fprintf(o.out, "  %-25s %-7s %s  %s\n", who, voteChoiceNames[v.GetVoteChoice()], shortClock(v.GetAt().AsTime()), v.GetReason())
				}
				if p.GetClosedBy() != "" {
					fmt.Fprintf(o.out, "\nClosed as %s by %s, %s.\n", proposalStateNames[p.GetProposalState()], p.GetClosedBy(), dateTime(p.GetClosedAt().AsTime()))
				}
				return nil
			}
			resp, err := o.governance().ListProposals(cmd.Context(), connect.NewRequest(&agorav1.ListProposalsRequest{All: all}))
			if err != nil {
				return err
			}
			if len(resp.Msg.GetProposals()) == 0 {
				fmt.Fprintln(o.out, "no open proposals")
			}
			for _, p := range resp.Msg.GetProposals() {
				printProposalLine(o.out, p)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "include closed proposals")
	cmd.Flags().StringVar(&show, "show", "", "print one proposal with its text and votes")
	return cmd
}

func charterCmd(o *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "charter",
		Short: "Show the board's charter",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			resp, err := o.governance().GetCharter(cmd.Context(), connect.NewRequest(&agorav1.GetCharterRequest{}))
			if err != nil {
				return err
			}
			m := resp.Msg
			fmt.Fprint(o.out, m.GetBody())
			if m.GetProposalId() != 0 {
				fmt.Fprintf(o.out, "\n(changed by %s after proposal #%d, %s)\n", m.GetChangedBy(), m.GetProposalId(), dateTime(m.GetChangedAt().AsTime()))
			}
			return nil
		},
	}
	var proposal string
	set := &cobra.Command{
		Use:   "set --proposal <n> [file | -]",
		Short: "Replace the charter after an accepted proposal, from a file or standard input",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := o.agent(cmd.Context())
			if err != nil {
				return err
			}
			id, err := proposalID(proposal)
			if err != nil {
				return err
			}
			var body string
			if len(args) == 1 && args[0] != "-" {
				b, err := os.ReadFile(args[0])
				if err != nil {
					return err
				}
				body = string(b)
			} else {
				if stdinTerminal(cmd) {
					return fmt.Errorf("give the new charter as a file, or pipe it: agora charter set --proposal %s < charter.md", proposal)
				}
				b, err := io.ReadAll(cmd.InOrStdin())
				if err != nil {
					return err
				}
				body = string(b)
			}
			if _, err := o.governance().SetCharter(cmd.Context(), connect.NewRequest(&agorav1.SetCharterRequest{Agent: name, ProposalId: id, Body: body})); err != nil {
				return err
			}
			fmt.Fprintf(o.out, "charter updated after proposal #%d and announced in #general\n", id)
			return nil
		},
	}
	set.Flags().StringVar(&proposal, "proposal", "", "the accepted proposal this change carries out")
	cmd.AddCommand(set)
	return cmd
}
