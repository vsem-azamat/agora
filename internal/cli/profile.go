package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	agorav1 "github.com/vsem-azamat/agora/gen/agora/v1"
	"github.com/vsem-azamat/agora/internal/connector/claudecode"
)

// profileFlags are the profile fields that join and set accept.
type profileFlags struct {
	kind, project, task, status, cwd, about string
	addPRs, dropPRs                         []string
}

func (f *profileFlags) register(cmd *cobra.Command, withDrop bool) {
	cmd.Flags().StringVar(&f.kind, "kind", "", "agent tool: claude-code, codex, ...")
	cmd.Flags().StringVar(&f.project, "project", "", "project you work on")
	cmd.Flags().StringVar(&f.task, "task", "", "what you are doing right now")
	cmd.Flags().StringVar(&f.status, "status", "", "your status (joining sets working)")
	cmd.Flags().StringVar(&f.cwd, "cwd", "", "directory you work in, e.g. a worktree")
	cmd.Flags().StringVar(&f.about, "about", "", "free text about you")
	cmd.Flags().StringArrayVar(&f.addPRs, "pr", nil, "pull request you work on (repeatable)")
	if withDrop {
		cmd.Flags().StringArrayVar(&f.dropPRs, "drop-pr", nil, "pull request you no longer work on (repeatable)")
	}
}

// request builds a profile update from the flags the user set.
func (f *profileFlags) request(cmd *cobra.Command, name string) (*agorav1.UpdateProfileRequest, error) {
	req := &agorav1.UpdateProfileRequest{Agent: name}
	str := func(flag string, v string) *string {
		if !cmd.Flags().Changed(flag) {
			return nil
		}
		return &v
	}
	req.Kind, req.Project, req.Task, req.Status, req.About = str("kind", f.kind), str("project", f.project), str("task", f.task), str("status", f.status), str("about", f.about)
	if cmd.Flags().Changed("cwd") {
		abs, err := filepath.Abs(f.cwd)
		if err != nil {
			return nil, err
		}
		req.Cwd = &abs
	}
	var err error
	if req.AddPrs, err = prNumbers(f.addPRs); err != nil {
		return nil, err
	}
	if req.DropPrs, err = prNumbers(f.dropPRs); err != nil {
		return nil, err
	}
	return req, nil
}

func prNumbers(in []string) ([]int32, error) {
	var out []int32
	for _, s := range in {
		n, err := strconv.ParseInt(strings.TrimPrefix(strings.TrimSpace(s), "#"), 10, 32)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("pull request %q: use a number like 57 or #57", s)
		}
		out = append(out, int32(n))
	}
	return out, nil
}

func joinCmd(o *options) *cobra.Command {
	var force bool
	var f profileFlags
	cmd := &cobra.Command{
		Use:   "join <name>",
		Short: "Take a name, bind it to this session and publish what you work on",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			req, err := f.request(cmd, name) // validate the flags before taking the name
			if err != nil {
				return err
			}
			if req.Status == nil {
				working := "working"
				req.Status = &working
			}
			if strings.TrimSpace(*req.Status) == "" || *req.Status == "left" {
				return errors.New("--status: give a status like working or reviewing; to leave, use `agora leave`")
			}
			if req.Cwd == nil {
				if wd, err := os.Getwd(); err == nil {
					req.Cwd = &wd
				}
			}
			if req.Kind == nil && claudecode.SessionID() != "" {
				kind := claudecode.Kind
				req.Kind = &kind
			}
			resp, err := o.sessions().JoinName(cmd.Context(), connect.NewRequest(&agorav1.JoinNameRequest{Name: name, SessionId: sessionID(), Force: force}))
			if err != nil {
				return err
			}
			if _, err := o.agents().UpdateProfile(cmd.Context(), connect.NewRequest(req)); err != nil {
				return err
			}
			if resp.Msg.GetBound() {
				fmt.Fprintf(o.out, "joined as %s; commands from this session now act as %s\n", name, name)
			} else {
				fmt.Fprintf(o.out, "joined as %s; no session found, so pass --as %s or set AGORA_NAME=%s\n", name, name, name)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "take the name from another live session")
	f.register(cmd, false)
	return cmd
}

func setCmd(o *options) *cobra.Command {
	var f profileFlags
	cmd := &cobra.Command{
		Use:   "set",
		Short: "Update your task, status, directory, pull requests or description",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			changed := false // only set's own flags count, not the inherited --as or --socket
			cmd.LocalNonPersistentFlags().VisitAll(func(f *pflag.Flag) { changed = changed || f.Changed })
			if !changed {
				return errors.New("nothing to change; pass at least one flag, e.g. --task")
			}
			name, err := o.agent(cmd.Context())
			if err != nil {
				return err
			}
			req, err := f.request(cmd, name)
			if err != nil {
				return err
			}
			resp, err := o.agents().UpdateProfile(cmd.Context(), connect.NewRequest(req))
			if err != nil {
				return err
			}
			printProfile(o.out, resp.Msg.GetProfile())
			return nil
		},
	}
	f.register(cmd, true)
	return cmd
}

func leaveCmd(o *options) *cobra.Command {
	return &cobra.Command{
		Use:   "leave",
		Short: "Mark yourself as left and give up every queue place and lock",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			name, err := o.agent(cmd.Context())
			if err != nil {
				return err
			}
			resp, err := o.agents().Leave(cmd.Context(), connect.NewRequest(&agorav1.LeaveRequest{Agent: name}))
			if err != nil {
				return err
			}
			for _, k := range resp.Msg.GetReleased() {
				fmt.Fprintf(o.out, "released %s\n", k)
			}
			fmt.Fprintf(o.out, "%s marked as left\n", name)
			if strings.TrimSpace(o.as) == "" { // the name came from this session, which no longer has one
				fmt.Fprintf(o.out, "commands from this session no longer act as %s; `agora join %s` to come back\n", name, name)
			}
			return nil
		},
	}
}

// statusProposals is how many open proposals status lists.
const statusProposals = 10

func statusCmd(o *options) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show active agents and busy resources",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			agents, err := o.agents().ListAgents(cmd.Context(), connect.NewRequest(&agorav1.ListAgentsRequest{All: all}))
			if err != nil {
				return err
			}
			which := " active"
			if all {
				which = ""
			}
			fmt.Fprintf(o.out, "AGENTS (%d%s)\n", len(agents.Msg.GetAgents()), which)
			for _, p := range agents.Msg.GetAgents() {
				fmt.Fprintf(o.out, "  %-16s %-9s %-7s %-14s %-12s %4s  %s\n", p.GetName(), p.GetStatus(), sessionStateNames[p.GetSession()],
					p.GetProject(), prList(allPRs(p)), age(p.GetUpdatedAt().AsTime()), p.GetTask())
			}
			if err := o.printRooms(cmd.Context()); err != nil {
				return err
			}
			res, err := o.resources().ListResources(cmd.Context(), connect.NewRequest(&agorav1.ListResourcesRequest{}))
			if err != nil {
				return err
			}
			props, err := o.governance().ListProposals(cmd.Context(), connect.NewRequest(&agorav1.ListProposalsRequest{}))
			if err != nil {
				fmt.Fprintf(o.out, "(proposals unavailable: %s)\n", message(err))
			}
			if err == nil && len(props.Msg.GetProposals()) > 0 {
				list := props.Msg.GetProposals()
				fmt.Fprintln(o.out, "OPEN PROPOSALS")
				for _, p := range list[:min(len(list), statusProposals)] {
					printProposalLine(o.out, p)
				}
				if len(list) > statusProposals {
					fmt.Fprintf(o.out, "  +%d more: agora proposals\n", len(list)-statusProposals)
				}
			}
			if len(res.Msg.GetResources()) > 0 {
				fmt.Fprintln(o.out, "RESOURCES")
				for _, r := range res.Msg.GetResources() {
					printResource(o.out, r)
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "include inactive and left agents")
	return cmd
}

func whoCmd(o *options) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "who <pr|branch|directory|name>",
		Short: "Find who works on a pull request (#57), branch, directory or name",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := &agorav1.WhoRequest{Query: args[0], All: all}
			if looksLikePath(args[0]) {
				abs, err := filepath.Abs(expandHome(args[0]))
				if err != nil {
					return err
				}
				req.Query, req.Path = abs, true
			}
			resp, err := o.agents().Who(cmd.Context(), connect.NewRequest(req))
			if err != nil {
				return err
			}
			if len(resp.Msg.GetAgents()) == 0 {
				hint := " (try --all)"
				if all {
					hint = ""
				}
				fmt.Fprintf(o.out, "nobody active matches %q%s\n", args[0], hint)
			}
			for _, p := range resp.Msg.GetAgents() {
				printProfile(o.out, p)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "include inactive and left agents")
	return cmd
}

// looksLikePath treats a query as a directory only when it is written like a path, so a name,
// branch or number never turns into a path because a directory happens to have that name.
func looksLikePath(q string) bool {
	return q == "~" || q == "." || q == ".." ||
		strings.HasPrefix(q, "/") || strings.HasPrefix(q, "~/") || strings.HasPrefix(q, "./") || strings.HasPrefix(q, "../")
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

func printProfile(w io.Writer, p *agorav1.Profile) {
	fmt.Fprintf(w, "%s · %s · %s · %s · %s\n", p.GetName(), p.GetStatus(), sessionStateNames[p.GetSession()], orDash(p.GetProject()), p.GetTask())
	var where []string
	if p.GetBranch() != "" {
		where = append(where, "["+p.GetBranch()+"]")
	}
	if prs := allPRs(p); len(prs) > 0 {
		where = append(where, "PR "+prList(prs))
	}
	if p.GetCwd() != "" {
		where = append(where, "in "+p.GetCwd())
	}
	if len(where) > 0 {
		fmt.Fprintf(w, "    %s\n", strings.Join(where, " "))
	}
}

// allPRs returns the declared and found pull requests of a profile, ascending.
func allPRs(p *agorav1.Profile) []int32 {
	all := append(slices.Clone(p.GetPrs()), p.GetFoundPrs()...)
	slices.Sort(all)
	return slices.Compact(all)
}

func prList(prs []int32) string {
	parts := make([]string, len(prs))
	for i, n := range prs {
		parts[i] = fmt.Sprintf("#%d", n)
	}
	return strings.Join(parts, ",")
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// age is how long ago t was, short: minutes under an hour, hours under two days, else days.
func age(t time.Time) string {
	const day = 24 * time.Hour
	d := time.Since(t)
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", max(int(d/time.Minute), 0))
	case d < 2*day:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	}
	return fmt.Sprintf("%dd", int(d/day))
}

func whoamiCmd(o *options) *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Show the name and session this command resolves to",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			name, err := o.agent(cmd.Context())
			if err != nil {
				name = "-"
			}
			id := sessionID()
			if id == "" {
				id = "-"
			}
			fmt.Fprintf(o.out, "name: %s\nsession: %s\n", name, id)
			return nil
		},
	}
}

func sessionsCmd(o *options) *cobra.Command {
	return &cobra.Command{
		Use:   "sessions",
		Short: "List agent sessions that have not ended",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			resp, err := o.sessions().ListSessions(cmd.Context(), connect.NewRequest(&agorav1.ListSessionsRequest{}))
			if err != nil {
				return err
			}
			if len(resp.Msg.GetSessions()) == 0 {
				fmt.Fprintln(o.out, "no live sessions")
			}
			for _, x := range resp.Msg.GetSessions() {
				agent := x.GetAgent()
				if agent == "" {
					agent = "-"
				}
				state, ok := sessionStateNames[x.GetState()]
				if !ok {
					state = "-"
				}
				fmt.Fprintf(o.out, "%-16s %-5s since %s  %-12s %s  %s\n", agent, state, clock(x.GetStateAt().AsTime()), x.GetKind(), x.GetId(), x.GetCwd())
			}
			return nil
		},
	}
}

var sessionStateNames = map[agorav1.SessionState]string{
	agorav1.SessionState_SESSION_STATE_BUSY:    "busy",
	agorav1.SessionState_SESSION_STATE_IDLE:    "idle",
	agorav1.SessionState_SESSION_STATE_ENDED:   "ended",
	agorav1.SessionState_SESSION_STATE_OFFLINE: "offline",
}

func hookCmd(o *options) *cobra.Command {
	return &cobra.Command{
		Use:    "hook <claude-code|claude-code-wait>",
		Short:  "Run as an agent tool's hook",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// A board problem must never disturb the agent's session: errors show only with
			// $AGORA_DEBUG set.
			debug := os.Getenv("AGORA_DEBUG") != ""
			switch args[0] {
			case "claude-code":
				if err := claudecode.Hook(cmd.Context(), o.sessions(), cmd.InOrStdin(), o.out); err != nil && debug {
					return err
				}
				return nil
			case "claude-code-wait":
				text, err := claudecode.Wait(cmd.Context(), o.sessions(), cmd.InOrStdin())
				if err != nil && debug {
					return err
				}
				if text != "" {
					return &exitError{code: claudecode.ExitWake, msg: text}
				}
				return nil
			default:
				return fmt.Errorf("unknown hook %q", args[0])
			}
		},
	}
}
