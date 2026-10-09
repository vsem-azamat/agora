// Package cli implements the agora command.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/durationpb"

	agorav1 "github.com/vsem-azamat/agora/gen/agora/v1"
	"github.com/vsem-azamat/agora/gen/agora/v1/agorav1connect"
	"github.com/vsem-azamat/agora/internal/connector/claudecode"
	"github.com/vsem-azamat/agora/internal/hub"
	"github.com/vsem-azamat/agora/internal/queue"
	"github.com/vsem-azamat/agora/internal/rooms"
	"github.com/vsem-azamat/agora/internal/store"
)

// ExitRefused is the exit code of a lock that someone else holds.
const ExitRefused = 2

// exitError carries a specific exit code out of a command.
type exitError struct {
	code int
	msg  string
}

func (e *exitError) Error() string { return e.msg }

// Run executes the agora command with args and returns the process exit code.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	return RunWithInput(ctx, args, os.Stdin, stdout, stderr)
}

// RunWithInput is Run with an explicit standard input, which hooks read.
func RunWithInput(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	root := newRoot(stdout, stderr)
	root.SetArgs(args)
	root.SetIn(stdin)
	root.SetOut(stdout)
	root.SetErr(stderr)
	err := root.ExecuteContext(ctx)
	if err == nil {
		return 0
	}
	var ee *exitError
	if errors.As(err, &ee) {
		fmt.Fprintln(stderr, ee.msg)
		return ee.code
	}
	fmt.Fprintf(stderr, "agora: %s\n", message(err))
	return 1
}

func message(err error) string {
	var ce *connect.Error
	if errors.As(err, &ce) {
		if ce.Code() == connect.CodeUnavailable {
			return "cannot reach the hub; start it with `agora hub`"
		}
		return ce.Message()
	}
	return err.Error()
}

type options struct {
	as     string
	socket string
	out    io.Writer
}

func newRoot(stdout, stderr io.Writer) *cobra.Command {
	o := &options{out: stdout}
	root := &cobra.Command{
		Use:           "agora",
		Short:         "Coordinate AI coding agents on your machines",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&o.as, "as", os.Getenv("AGORA_NAME"), "agent name to act as (default $AGORA_NAME)")
	root.PersistentFlags().StringVar(&o.socket, "socket", defaultSocket(), "hub socket (default $AGORA_SOCKET)")

	root.AddCommand(hubCmd(o, stderr), joinCmd(o), setCmd(o), leaveCmd(o), statusCmd(o), whoCmd(o),
		roomsCmd(o), roomCreateCmd(o), subscribeCmd(o, true), subscribeCmd(o, false), postCmd(o), readCmd(o), unreadCmd(o),
		whoamiCmd(o), sessionsCmd(o), hookCmd(o),
		queueCmd(o), lockCmd(o), unlockCmd(o), locksCmd(o))
	return root
}

func defaultSocket() string {
	if s := os.Getenv("AGORA_SOCKET"); s != "" {
		return s
	}
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		return filepath.Join(d, "agora", "hub.sock")
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("agora-%d", os.Getuid()), "hub.sock")
}

func defaultDB() string {
	if s := os.Getenv("AGORA_DB"); s != "" {
		return s
	}
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, "agora", "agora.db")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "agora", "agora.db")
}

// sessionID is the agent session this command runs in, as the agent tool exposes it.
func sessionID() string {
	for _, v := range []string{"CLAUDE_CODE_SESSION_ID", "AGORA_SESSION"} {
		if id := os.Getenv(v); id != "" {
			return id
		}
	}
	return ""
}

// agent resolves who runs the command: --as, else $AGORA_NAME, else the name bound to the
// command's session.
func (o *options) agent(ctx context.Context) (string, error) {
	if strings.TrimSpace(o.as) != "" {
		return o.as, nil
	}
	if id := sessionID(); id != "" {
		resp, err := o.sessions().Resolve(ctx, connect.NewRequest(&agorav1.ResolveRequest{SessionId: id}))
		if err != nil {
			return "", err
		}
		if name := resp.Msg.GetAgent(); name != "" {
			return name, nil
		}
	}
	return "", errors.New("who are you? join first (`agora join <name>`), or pass --as <name> or set AGORA_NAME")
}

func (o *options) httpClient() *http.Client {
	socket := o.socket
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
	}
	return &http.Client{Transport: transport}
}

func (o *options) resources() agorav1connect.ResourceServiceClient {
	return agorav1connect.NewResourceServiceClient(o.httpClient(), "http://agora")
}

func (o *options) sessions() agorav1connect.SessionServiceClient {
	return agorav1connect.NewSessionServiceClient(o.httpClient(), "http://agora")
}

func (o *options) agents() agorav1connect.AgentServiceClient {
	return agorav1connect.NewAgentServiceClient(o.httpClient(), "http://agora")
}

func (o *options) rooms() agorav1connect.RoomServiceClient {
	return agorav1connect.NewRoomServiceClient(o.httpClient(), "http://agora")
}

// --- hub ------------------------------------------------------------------------

func hubCmd(o *options, stderr io.Writer) *cobra.Command {
	var dbPath string
	cmd := &cobra.Command{
		Use:   "hub",
		Short: "Run the hub",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			log := slog.New(slog.NewTextHandler(stderr, nil))
			db, err := store.Open(ctx, dbPath)
			if err != nil {
				return err
			}
			defer db.Close()
			l, err := hub.Listen(o.socket)
			if err != nil {
				return err
			}
			log.Info("hub listening", "socket", o.socket, "db", dbPath)
			return hub.Open(db, nil, log).Serve(ctx, l)
		},
	}
	cmd.Flags().StringVar(&dbPath, "db", defaultDB(), "database file (default $AGORA_DB)")
	return cmd
}

// --- identity and sessions ----------------------------------------------------------

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
	req := &agorav1.UpdateProfileRequest{Name: name}
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
				return fmt.Errorf("--status: give a status like working or reviewing; to leave, use `agora leave`")
			}
			if req.Cwd == nil {
				if wd, err := os.Getwd(); err == nil {
					req.Cwd = &wd
				}
			}
			if req.Kind == nil && os.Getenv("CLAUDE_CODE_SESSION_ID") != "" {
				kind := "claude-code"
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
			name, err := o.agent(cmd.Context())
			if err != nil {
				return err
			}
			if cmd.Flags().NFlag() == 0 {
				return fmt.Errorf("nothing to change; pass at least one flag, e.g. --task")
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
			resp, err := o.agents().Leave(cmd.Context(), connect.NewRequest(&agorav1.LeaveRequest{Name: name}))
			if err != nil {
				return err
			}
			for _, k := range resp.Msg.GetReleased() {
				fmt.Fprintf(o.out, "released %s\n", k)
			}
			fmt.Fprintf(o.out, "%s marked as left\n", name)
			return nil
		},
	}
}

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
				fmt.Fprintf(o.out, "  %-16s %-9s %-7s %-14s %-12s %4s  %s\n", p.GetName(), p.GetStatus(), p.GetSessionState(),
					p.GetProject(), prList(p.GetPrs()), age(p.GetUpdatedAt().AsTime()), p.GetTask())
			}
			if err := o.printRooms(cmd.Context()); err != nil {
				return err
			}
			res, err := o.resources().List(cmd.Context(), connect.NewRequest(&agorav1.ListRequest{}))
			if err != nil {
				return err
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
	fmt.Fprintf(w, "%s · %s · %s · %s · %s\n", p.GetName(), p.GetStatus(), p.GetSessionState(), orDash(p.GetProject()), p.GetTask())
	var where []string
	if p.GetBranch() != "" {
		where = append(where, "["+p.GetBranch()+"]")
	}
	if len(p.GetPrs()) > 0 {
		where = append(where, "PR "+prList(p.GetPrs()))
	}
	if p.GetCwd() != "" {
		where = append(where, "in "+p.GetCwd())
	}
	if len(where) > 0 {
		fmt.Fprintf(w, "    %s\n", strings.Join(where, " "))
	}
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

func age(t time.Time) string {
	m := int(time.Since(t).Minutes())
	switch {
	case m < 60:
		return fmt.Sprintf("%dm", max(m, 0))
	case m < 48*60:
		return fmt.Sprintf("%dh", m/60)
	}
	return fmt.Sprintf("%dd", m/1440)
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
				state := strings.ToLower(strings.TrimPrefix(x.GetState().String(), "SESSION_STATE_"))
				fmt.Fprintf(o.out, "%-16s %-5s since %s  %-12s %s  %s\n", agent, state, clock(x.GetStateAt().AsTime()), x.GetKind(), x.GetId(), x.GetCwd())
			}
			return nil
		},
	}
}

func hookCmd(o *options) *cobra.Command {
	return &cobra.Command{
		Use:    "hook <tool>",
		Short:  "Run as an agent tool's hook (claude-code)",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if args[0] != "claude-code" {
				return fmt.Errorf("unknown tool %q", args[0])
			}
			err := claudecode.Hook(cmd.Context(), o.sessions(), cmd.InOrStdin(), o.out)
			if err != nil && os.Getenv("AGORA_DEBUG") != "" {
				return err
			}
			return nil // a board problem must never disturb the agent's session
		},
	}
}

// --- rooms and messages -------------------------------------------------------------

func (o *options) printRooms(ctx context.Context) error {
	resp, err := o.rooms().ListRooms(ctx, connect.NewRequest(&agorav1.ListRoomsRequest{}))
	if err != nil {
		return err
	}
	fmt.Fprintln(o.out, "ROOMS")
	for _, r := range resp.Msg.GetRooms() {
		last := "-"
		if r.GetLastAt() != nil {
			last = age(r.GetLastAt().AsTime())
		}
		purpose, _, _ := strings.Cut(r.GetPurpose(), "\n")
		fmt.Fprintf(o.out, "  #%-20s %5d msgs  last %4s  %s\n", r.GetName(), r.GetMessages(), last, purpose)
	}
	return nil
}

func roomsCmd(o *options) *cobra.Command {
	return &cobra.Command{
		Use:   "rooms",
		Short: "List rooms",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return o.printRooms(cmd.Context()) },
	}
}

func roomCreateCmd(o *options) *cobra.Command {
	return &cobra.Command{
		Use:   "room-create <room> <purpose...>",
		Short: "Create a room with a purpose and follow it",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := o.agent(cmd.Context())
			if err != nil {
				return err
			}
			room := strings.TrimPrefix(args[0], "#")
			if _, err := o.rooms().CreateRoom(cmd.Context(), connect.NewRequest(&agorav1.CreateRoomRequest{
				Name: room, Purpose: strings.Join(args[1:], " "), Creator: name,
			})); err != nil {
				return err
			}
			fmt.Fprintf(o.out, "created #%s; you follow it\n", room)
			return nil
		},
	}
}

func subscribeCmd(o *options, follow bool) *cobra.Command {
	use, short := "subscribe <room...>", "Follow rooms: their new messages count as unread for you"
	if !follow {
		use, short = "unsubscribe <room...>", "Stop following rooms (#general stays)"
	}
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := o.agent(cmd.Context())
			if err != nil {
				return err
			}
			resp, err := o.rooms().Subscribe(cmd.Context(), connect.NewRequest(&agorav1.SubscribeRequest{Agent: name, Rooms: args, Follow: follow}))
			if err != nil {
				return err
			}
			fmt.Fprintf(o.out, "%s follows: #%s\n", name, strings.Join(resp.Msg.GetRooms(), " #"))
			return nil
		},
	}
}

func postCmd(o *options) *cobra.Command {
	var reply int64
	cmd := &cobra.Command{
		Use:   "post <room> [text...]",
		Short: "Post to a room (text as arguments, or '-' / nothing to read standard input); @name or @all to address",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := o.agent(cmd.Context())
			if err != nil {
				return err
			}
			text := strings.TrimSpace(strings.Join(args[1:], " "))
			if text == "" || text == "-" {
				b, err := io.ReadAll(cmd.InOrStdin())
				if err != nil {
					return err
				}
				text = strings.TrimSpace(string(b))
			}
			resp, err := o.rooms().Post(cmd.Context(), connect.NewRequest(&agorav1.PostRequest{
				Author: name, Room: strings.TrimPrefix(args[0], "#"), Body: text, ReplyTo: reply,
			}))
			if err != nil {
				return err
			}
			fmt.Fprintln(o.out, resp.Msg.GetId())
			return nil
		},
	}
	cmd.Flags().Int64Var(&reply, "reply", 0, "message id this replies to")
	return cmd
}

func readCmd(o *options) *cobra.Command {
	var last int32
	cmd := &cobra.Command{
		Use:   "read <room>",
		Short: "Show a room's recent messages (does not mark anything read)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := o.rooms().History(cmd.Context(), connect.NewRequest(&agorav1.HistoryRequest{Room: strings.TrimPrefix(args[0], "#"), Last: last}))
			if err != nil {
				return err
			}
			for _, m := range resp.Msg.GetMessages() {
				fmt.Fprintln(o.out, formatMessage(m))
			}
			return nil
		},
	}
	cmd.Flags().Int32Var(&last, "last", 20, "how many messages")
	return cmd
}

func unreadCmd(o *options) *cobra.Command {
	var peek bool
	cmd := &cobra.Command{
		Use:   "unread",
		Short: "New messages in your rooms and mentions anywhere; marks them read",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			name, err := o.agent(cmd.Context())
			if err != nil {
				return err
			}
			resp, err := o.rooms().Unread(cmd.Context(), connect.NewRequest(&agorav1.UnreadRequest{Agent: name, Peek: peek}))
			if err != nil {
				return err
			}
			if len(resp.Msg.GetMessages()) == 0 {
				fmt.Fprintln(o.out, "no unread messages")
			}
			for _, m := range resp.Msg.GetMessages() {
				fmt.Fprintln(o.out, formatMessage(m))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&peek, "peek", false, "show without marking read")
	return cmd
}

func formatMessage(m *agorav1.Message) string {
	return rooms.Format(rooms.Message{
		ID: m.GetId(), Room: m.GetRoom(), Author: m.GetAuthor(), Body: m.GetBody(),
		ReplyTo: m.GetReplyTo(), At: m.GetAt().AsTime(), Addressed: m.GetAddressed(),
	}, 0)
}

// --- queue ----------------------------------------------------------------------

func queueCmd(o *options) *cobra.Command {
	cmd := &cobra.Command{Use: "queue", Short: "Queue for resources with a limited number of slots"}

	var lease time.Duration
	var wait bool
	join := &cobra.Command{
		Use:   "join <key> [note...]",
		Short: "Join a resource's queue; holds a slot at once when one is free",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			agent, err := o.agent(cmd.Context())
			if err != nil {
				return err
			}
			if lease <= 0 {
				return fmt.Errorf("--lease must be positive, like 90s, 30m or 2h")
			}
			resp, err := o.resources().Join(cmd.Context(), connect.NewRequest(&agorav1.JoinRequest{
				Key: args[0], Agent: agent, Note: strings.Join(args[1:], " "), Lease: durationpb.New(lease),
			}))
			if err != nil {
				return err
			}
			printEntry(o.out, resp.Msg.GetEntry())
			if wait && resp.Msg.GetEntry().GetState() != agorav1.EntryState_ENTRY_STATE_HELD {
				return o.waitTurn(cmd.Context(), args[0], agent)
			}
			return nil
		},
	}
	join.Flags().DurationVar(&lease, "lease", queue.DefaultLease, "how long a held slot lasts without renewal")
	join.Flags().BoolVar(&wait, "wait", false, "wait until the slot is yours")

	waitCmd := &cobra.Command{
		Use:   "wait <key>",
		Short: "Wait for your turn; ends when you hold the slot",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			agent, err := o.agent(cmd.Context())
			if err != nil {
				return err
			}
			return o.waitTurn(cmd.Context(), args[0], agent)
		},
	}

	renew := &cobra.Command{
		Use:   "renew <key>",
		Short: "Claim an offered slot or extend your lease",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			agent, err := o.agent(cmd.Context())
			if err != nil {
				return err
			}
			resp, err := o.resources().Renew(cmd.Context(), connect.NewRequest(&agorav1.RenewRequest{Key: args[0], Agent: agent}))
			if err != nil {
				return err
			}
			printEntry(o.out, resp.Msg.GetEntry())
			return nil
		},
	}

	var other string
	var force bool
	release := &cobra.Command{
		Use:   "release <key>",
		Short: "Leave a resource's queue, freeing your slot",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			agent, err := o.agent(cmd.Context())
			if err != nil {
				return err
			}
			target := agent
			if other != "" {
				target = other
			}
			return o.release(cmd.Context(), args[0], target, agent, force)
		},
	}
	release.Flags().StringVar(&other, "agent", "", "remove this agent instead of yourself (needs --force)")
	release.Flags().BoolVar(&force, "force", false, "remove another agent; recorded by the hub")

	ls := &cobra.Command{
		Use:   "ls [key]",
		Short: "List resources with holders and waiting agents",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			key := ""
			if len(args) == 1 {
				key = args[0]
			}
			resp, err := o.resources().List(cmd.Context(), connect.NewRequest(&agorav1.ListRequest{Key: key}))
			if err != nil {
				return err
			}
			if len(resp.Msg.GetResources()) == 0 {
				fmt.Fprintln(o.out, "no queued resources")
			}
			for _, r := range resp.Msg.GetResources() {
				printResource(o.out, r)
			}
			return nil
		},
	}

	slots := &cobra.Command{
		Use:   "slots <key> <n>",
		Short: "Set how many agents may hold a resource at once",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			n, err := strconv.Atoi(args[1])
			if err != nil || n < 1 || n > queue.MaxSlots {
				return fmt.Errorf("slots: %q must be a number from 1 to %d", args[1], queue.MaxSlots)
			}
			resp, err := o.resources().SetSlots(cmd.Context(), connect.NewRequest(&agorav1.SetSlotsRequest{Key: args[0], Slots: int32(n)}))
			if err != nil {
				return err
			}
			fmt.Fprintf(o.out, "%s has %d slots\n", resp.Msg.GetResource().GetKey(), resp.Msg.GetResource().GetSlots())
			return nil
		},
	}

	cmd.AddCommand(join, waitCmd, renew, release, ls, slots)
	return cmd
}

// waitTurn streams the agent's position until it holds the slot. When the hub restarts, the
// queue survives, so the wait reconnects instead of failing.
func (o *options) waitTurn(ctx context.Context, key, agent string) error {
	for {
		err := o.waitOnce(ctx, key, agent)
		if err == nil || ctx.Err() != nil {
			return err
		}
		code := connect.CodeOf(err)
		if code != connect.CodeUnavailable && code != connect.CodeCanceled && code != connect.CodeUnknown {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func (o *options) waitOnce(ctx context.Context, key, agent string) error {
	stream, err := o.resources().Wait(ctx, connect.NewRequest(&agorav1.WaitRequest{Key: key, Agent: agent}))
	if err != nil {
		return err
	}
	defer stream.Close()
	held := false
	for stream.Receive() {
		e := stream.Msg().GetEntry()
		printEntry(o.out, e)
		held = e.GetState() == agorav1.EntryState_ENTRY_STATE_HELD
	}
	if err := stream.Err(); err != nil {
		return err
	}
	if !held {
		return connect.NewError(connect.CodeUnavailable, errors.New("the hub ended the wait"))
	}
	return nil
}

func (o *options) release(ctx context.Context, key, agent, actor string, force bool) error {
	resp, err := o.resources().Release(ctx, connect.NewRequest(&agorav1.ReleaseRequest{Key: key, Agent: agent, Actor: actor, Force: force}))
	if err != nil {
		return err
	}
	switch {
	case resp.Msg.GetReleased() && agent != actor:
		fmt.Fprintf(o.out, "removed %s from %s\n", agent, key)
	case resp.Msg.GetReleased():
		fmt.Fprintf(o.out, "released %s\n", key)
	default:
		holders, err := o.holders(ctx, key)
		if err != nil {
			return err
		}
		if len(holders) > 0 && agent == actor {
			return fmt.Errorf("%s is held by %s, not by you; release it with --force only if they are gone", key, strings.Join(holders, ", "))
		}
		fmt.Fprintf(o.out, "%s: %s was not queued\n", key, agent)
	}
	return nil
}

// holders describes who holds or is offered key, for refusals.
func (o *options) holders(ctx context.Context, key string) ([]string, error) {
	resp, err := o.resources().List(ctx, connect.NewRequest(&agorav1.ListRequest{Key: key}))
	if err != nil {
		return nil, err
	}
	var out []string
	for _, r := range resp.Msg.GetResources() {
		out = append(out, describeHolders(r)...)
	}
	return out, nil
}

func describeHolders(r *agorav1.Resource) []string {
	var out []string
	waiting := 0
	for _, e := range r.GetEntries() {
		switch e.GetState() {
		case agorav1.EntryState_ENTRY_STATE_HELD:
			out = append(out, fmt.Sprintf("%s until %s%s", e.GetAgent(), clock(e.GetExpiresAt().AsTime()), noteSuffix(e.GetNote())))
		case agorav1.EntryState_ENTRY_STATE_OFFERED:
			out = append(out, fmt.Sprintf("%s (its turn, claim by %s)", e.GetAgent(), clock(e.GetExpiresAt().AsTime())))
		default:
			waiting++
		}
	}
	if waiting > 0 && len(out) > 0 {
		out[len(out)-1] += fmt.Sprintf("; %d waiting", waiting)
	}
	return out
}

// --- locks: queues that do not wait --------------------------------------------------

func lockCmd(o *options) *cobra.Command {
	var ttl time.Duration
	cmd := &cobra.Command{
		Use:   "lock <key> [note...]",
		Short: "Take a lock (a one-slot queue) without waiting; exit code 2 if someone holds it",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			agent, err := o.agent(cmd.Context())
			if err != nil {
				return err
			}
			if ttl <= 0 {
				return fmt.Errorf("--ttl must be positive, like 90s, 30m or 2h")
			}
			resp, err := o.resources().Join(cmd.Context(), connect.NewRequest(&agorav1.JoinRequest{
				Key: args[0], Agent: agent, Note: strings.Join(args[1:], " "), Lease: durationpb.New(ttl), NoWait: true,
			}))
			if err != nil {
				return err
			}
			if e := resp.Msg.GetEntry(); e != nil {
				fmt.Fprintf(o.out, "locked %s until %s\n", e.GetKey(), clock(e.GetExpiresAt().AsTime()))
				return nil
			}
			held := describeHolders(resp.Msg.GetResource())
			if len(held) == 0 {
				return &exitError{code: ExitRefused, msg: fmt.Sprintf("%s: you are queued for it; wait with `agora queue wait %s`", args[0], args[0])}
			}
			return &exitError{code: ExitRefused, msg: fmt.Sprintf("%s is held by %s", args[0], strings.Join(held, ", "))}
		},
	}
	cmd.Flags().DurationVar(&ttl, "ttl", queue.DefaultLease, "how long the lock lasts")
	return cmd
}

func unlockCmd(o *options) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "unlock <key>",
		Short: "Release your lock; with --force, release whoever holds it",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			agent, err := o.agent(cmd.Context())
			if err != nil {
				return err
			}
			if !force {
				return o.release(cmd.Context(), args[0], agent, agent, false)
			}
			resp, err := o.resources().List(cmd.Context(), connect.NewRequest(&agorav1.ListRequest{Key: args[0]}))
			if err != nil {
				return err
			}
			released := 0
			for _, r := range resp.Msg.GetResources() {
				for _, e := range r.GetEntries() {
					if e.GetState() == agorav1.EntryState_ENTRY_STATE_HELD {
						if err := o.release(cmd.Context(), args[0], e.GetAgent(), agent, true); err != nil {
							return err
						}
						released++
					}
				}
			}
			if released == 0 {
				fmt.Fprintf(o.out, "%s is not locked\n", args[0])
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "release another agent's lock; recorded by the hub")
	return cmd
}

func locksCmd(o *options) *cobra.Command {
	return &cobra.Command{
		Use:   "locks",
		Short: "List held slots of every resource",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			resp, err := o.resources().List(cmd.Context(), connect.NewRequest(&agorav1.ListRequest{}))
			if err != nil {
				return err
			}
			for _, r := range resp.Msg.GetResources() {
				for _, e := range r.GetEntries() {
					if e.GetState() == agorav1.EntryState_ENTRY_STATE_HELD {
						fmt.Fprintf(o.out, "%-28s %-16s until %s%s\n", e.GetKey(), e.GetAgent(), clock(e.GetExpiresAt().AsTime()), noteSuffix(e.GetNote()))
					}
				}
			}
			return nil
		},
	}
}

// --- output ---------------------------------------------------------------------------

func clock(t time.Time) string { return t.Local().Format("15:04:05") }

func noteSuffix(note string) string {
	if note == "" {
		return ""
	}
	return ": " + note
}

func printEntry(w io.Writer, e *agorav1.Entry) {
	switch e.GetState() {
	case agorav1.EntryState_ENTRY_STATE_HELD:
		fmt.Fprintf(w, "holding %s until %s\n", e.GetKey(), clock(e.GetExpiresAt().AsTime()))
	case agorav1.EntryState_ENTRY_STATE_OFFERED:
		fmt.Fprintf(w, "your turn on %s: claim it by %s with `agora queue renew %s`\n", e.GetKey(), clock(e.GetExpiresAt().AsTime()), e.GetKey())
	case agorav1.EntryState_ENTRY_STATE_WAITING:
		fmt.Fprintf(w, "waiting for %s at position %d\n", e.GetKey(), e.GetPosition())
	}
}

func printResource(w io.Writer, r *agorav1.Resource) {
	fmt.Fprintf(w, "%s (%d slot%s)\n", r.GetKey(), r.GetSlots(), plural(r.GetSlots()))
	for _, e := range r.GetEntries() {
		switch e.GetState() {
		case agorav1.EntryState_ENTRY_STATE_HELD:
			fmt.Fprintf(w, "  held     %-16s until %s%s\n", e.GetAgent(), clock(e.GetExpiresAt().AsTime()), noteSuffix(e.GetNote()))
		case agorav1.EntryState_ENTRY_STATE_OFFERED:
			fmt.Fprintf(w, "  offered  %-16s claim by %s%s\n", e.GetAgent(), clock(e.GetExpiresAt().AsTime()), noteSuffix(e.GetNote()))
		default:
			fmt.Fprintf(w, "  #%-7d %-16s since %s%s\n", e.GetPosition(), e.GetAgent(), clock(e.GetJoinedAt().AsTime()), noteSuffix(e.GetNote()))
		}
	}
}

func plural(n int32) string {
	if n == 1 {
		return ""
	}
	return "s"
}
