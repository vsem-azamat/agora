package cli

import (
	"fmt"
	"strings"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	agorav1 "github.com/vsem-azamat/agora/gen/agora/v1"
)

func compactCmd(o *options) *cobra.Command {
	return &cobra.Command{
		Use:   "compact <agent> [focus]",
		Short: "Compact another agent's conversation when its session is idle; the board announces it",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			by, err := o.agent(cmd.Context())
			if err != nil {
				return err
			}
			req := &agorav1.CompactRequest{Agent: by, Target: strings.TrimPrefix(args[0], "@")}
			if len(args) == 2 {
				req.Focus = args[1]
			}
			if _, err := o.sessions().Compact(cmd.Context(), connect.NewRequest(req)); err != nil {
				return err
			}
			fmt.Fprintf(o.out, "queued a compact of %s; it runs when its session is idle, and the board says so\n", req.Target)
			return nil
		},
	}
}

func interruptCmd(o *options) *cobra.Command {
	return &cobra.Command{
		Use:   "interrupt <agent>",
		Short: "Interrupt the turn of another agent's busy session; the board announces it",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			by, err := o.agent(cmd.Context())
			if err != nil {
				return err
			}
			agent := strings.TrimPrefix(args[0], "@")
			if _, err := o.sessions().Interrupt(cmd.Context(), connect.NewRequest(&agorav1.InterruptRequest{Agent: by, Target: agent})); err != nil {
				return err
			}
			fmt.Fprintf(o.out, "interrupted %s; the board says so\n", agent)
			return nil
		},
	}
}
