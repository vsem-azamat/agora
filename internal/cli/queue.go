package cli

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/durationpb"

	agorav1 "github.com/vsem-azamat/agora/gen/agora/v1"
	"github.com/vsem-azamat/agora/internal/queue"
)

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
			req := &agorav1.JoinRequest{Key: args[0], Agent: agent, Note: strings.Join(args[1:], " ")}
			if cmd.Flags().Changed("lease") { // unset: the default for a new place, unchanged for a re-join
				if err := checkLease("--lease", lease); err != nil {
					return err
				}
				req.Lease = durationpb.New(lease)
			}
			resp, err := o.resources().Join(cmd.Context(), connect.NewRequest(req))
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
	join.Flags().DurationVar(&lease, "lease", queue.DefaultLease, "how long a held slot lasts without renewal; joining again with it changes your lease")
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
			resp, err := o.resources().SetSlots(cmd.Context(), connect.NewRequest(&agorav1.SetSlotsRequest{Key: args[0], Slots: int32(n)})) //nolint:gosec // n is checked against MaxSlots above
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

// checkLease refuses a lease outside the bounds the hub accepts.
func checkLease(flag string, d time.Duration) error {
	if d < queue.MinLease || d > queue.MaxLease {
		return fmt.Errorf("%s must be between %s and %s, like 90s, 30m or 2h", flag, queue.MinLease, queue.MaxLease)
	}
	return nil
}

// hubGiveUp is how long a wait for a turn tolerates a hub that does not answer.
var hubGiveUp = 30 * time.Second

// waitRetry is the pause before a wait for a turn reconnects to the hub.
const waitRetry = time.Second

// waitTurn streams the agent's position until it holds the slot. When the hub restarts, the
// queue survives, so the wait reconnects instead of failing; once the hub has not answered for
// hubGiveUp, the wait fails as unreachable.
func (o *options) waitTurn(ctx context.Context, key, agent string) error {
	contact := time.Now()
	for {
		err := o.waitOnce(ctx, key, agent, &contact)
		if err == nil || ctx.Err() != nil {
			return err
		}
		code := connect.CodeOf(err)
		if code != connect.CodeUnavailable && code != connect.CodeCanceled && code != connect.CodeUnknown {
			return err
		}
		if time.Since(contact) >= hubGiveUp {
			return err // Unavailable reads as "cannot reach the hub"; other errors stay as they are
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(waitRetry):
		}
	}
}

// waitOnce runs one wait stream. The hub answers at the start of a stream, and a stream it
// answered keeps contact until it ends, so contact is set then.
func (o *options) waitOnce(ctx context.Context, key, agent string, contact *time.Time) error {
	stream, err := o.resources().Wait(ctx, connect.NewRequest(&agorav1.WaitRequest{Key: key, Agent: agent}))
	if err != nil {
		return err
	}
	defer stream.Close()
	held, answered := false, false
	defer func() {
		if answered {
			*contact = time.Now()
		}
	}()
	for stream.Receive() {
		answered = true
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
			if err := checkLease("--ttl", ttl); err != nil {
				return err
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
