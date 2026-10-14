package cli

import (
	"errors"
	"fmt"
	"strings"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	agorav1 "github.com/vsem-azamat/agora/gen/agora/v1"
)

func bridgeCmd(o *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bridge",
		Short: "Connect rooms to chats outside Agora through bridge programs the hub runs",
	}
	cmd.AddCommand(bridgeAddCmd(o), bridgeSetCmd(o), bridgeRemoveCmd(o), bridgeListCmd(o))
	return cmd
}

func bridgeAddCmd(o *options) *cobra.Command {
	var command, purpose string
	var addressees []string
	cmd := &cobra.Command{
		Use:   "add <name> --command '<shell command>' [--purpose <text>] [--agent <name>]...",
		Short: "Add a bridge: create or attach the room of its name, run its command, and subscribe --agent agents with the mode wake",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := o.agent(cmd.Context())
			if err != nil {
				return err
			}
			bridge := roomArg(args[0])
			resp, err := o.bridges().AddBridge(cmd.Context(), connect.NewRequest(&agorav1.AddBridgeRequest{
				Agent: name, Name: bridge, Command: command, Purpose: purpose, Addressees: addressees,
			}))
			if err != nil {
				return err
			}
			room := "attached #" + bridge
			if resp.Msg.GetCreatedRoom() {
				room = "created #" + bridge
			}
			fmt.Fprintf(o.out, "%s; its bridge runs with the outbound policy %s%s\n", room, bridgePolicyNames[resp.Msg.GetPolicy()], policyHints[resp.Msg.GetPolicy()])
			if len(addressees) > 0 {
				fmt.Fprintf(o.out, "subscribed %s to #%s with the mode wake; messages from outside marked as addressed address them too\n", strings.Join(addressees, ", "), bridge)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&command, "command", "", "shell command that runs the bridge; it gets $AGORA_BRIDGE and $AGORA_ROOM")
	cmd.Flags().StringVar(&purpose, "purpose", "", "purpose of the room, when the bridge creates it")
	cmd.Flags().StringArrayVar(&addressees, "agent", nil, "agent that follows the room with the mode wake and is addressed by messages addressed to the bridge (repeatable)")
	_ = cmd.MarkFlagRequired("command")
	return cmd
}

func bridgeSetCmd(o *options) *cobra.Command {
	var command string
	var addressees []string
	var none bool
	cmd := &cobra.Command{
		Use:   "set <name> [--agent <name>]... [--no-agents] [--command '<shell command>']",
		Short: "Change a bridge's agents or command in place; it keeps its cursor, and only a new command restarts it",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			setAgents := cmd.Flags().Changed("agent")
			if setAgents && none {
				return errors.New("give --agent or --no-agents, not both")
			}
			if !setAgents && !none && !cmd.Flags().Changed("command") {
				return errors.New("nothing to change: give --agent, --no-agents or --command")
			}
			name, err := o.agent(cmd.Context())
			if err != nil {
				return err
			}
			bridge := roomArg(args[0])
			req := &agorav1.SetBridgeRequest{Agent: name, Name: bridge, SetAddressees: setAgents || none, Addressees: addressees}
			if cmd.Flags().Changed("command") {
				req.Command = &command
			}
			resp, err := o.bridges().SetBridge(cmd.Context(), connect.NewRequest(req))
			if err != nil {
				return err
			}
			agents := "none"
			if len(resp.Msg.GetAddressees()) > 0 {
				agents = strings.Join(resp.Msg.GetAddressees(), ", ")
			}
			fmt.Fprintf(o.out, "#%s: agents %s\n", bridge, agents)
			if d := resp.Msg.GetDropped(); len(d) > 0 {
				fmt.Fprintf(o.out, "no longer the bridge's agents, still subscribed to #%s: %s; each stops with `agora --as <name> unsubscribe %s`\n", bridge, strings.Join(d, ", "), bridge)
			}
			if resp.Msg.GetRestarted() {
				fmt.Fprintln(o.out, "the hub restarted the bridge with the new command; it keeps its cursor")
			}
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&addressees, "agent", nil, "agent that replaces the bridge's agents: it follows the room with the mode wake and is addressed by messages addressed to the bridge (repeatable)")
	cmd.Flags().BoolVar(&none, "no-agents", false, "leave the bridge without agents")
	cmd.Flags().StringVar(&command, "command", "", "new shell command for the bridge; the hub restarts it when the command changed")
	return cmd
}

func bridgeRemoveCmd(o *options) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Stop and remove a bridge; its room and messages stay",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := o.agent(cmd.Context())
			if err != nil {
				return err
			}
			bridge := roomArg(args[0])
			if _, err := o.bridges().RemoveBridge(cmd.Context(), connect.NewRequest(&agorav1.RemoveBridgeRequest{Agent: name, Name: bridge})); err != nil {
				return err
			}
			fmt.Fprintf(o.out, "removed the bridge of #%s; the room stays\n", bridge)
			return nil
		},
	}
}

var bridgeStateNames = map[agorav1.BridgeState]string{
	agorav1.BridgeState_BRIDGE_STATE_RUNNING:    "running",
	agorav1.BridgeState_BRIDGE_STATE_RESTARTING: "restarting",
	agorav1.BridgeState_BRIDGE_STATE_STOPPED:    "stopped",
}

var bridgePolicyNames = map[agorav1.BridgePolicy]string{
	agorav1.BridgePolicy_BRIDGE_POLICY_OPEN:  "open",
	agorav1.BridgePolicy_BRIDGE_POLICY_DELAY: "delay",
	agorav1.BridgePolicy_BRIDGE_POLICY_READ:  "read",
}

// policyHints say after a policy's name what it means for agents.
var policyHints = map[agorav1.BridgePolicy]string{
	agorav1.BridgePolicy_BRIDGE_POLICY_OPEN:  ": agents' messages go out at once",
	agorav1.BridgePolicy_BRIDGE_POLICY_DELAY: ": agents' messages go out after the hub's delay unless the operator cancels them in the web app",
	agorav1.BridgePolicy_BRIDGE_POLICY_READ:  ": nothing goes out and agents post only notes",
}

func bridgeListCmd(o *options) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List bridges with their state, outbound policy, agents, what their platform can do and command",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			resp, err := o.bridges().ListBridges(cmd.Context(), connect.NewRequest(&agorav1.ListBridgesRequest{}))
			if err != nil {
				return err
			}
			if len(resp.Msg.GetBridges()) == 0 {
				fmt.Fprintln(o.out, "no bridges")
			}
			for _, b := range resp.Msg.GetBridges() {
				state := bridgeStateNames[b.GetState()]
				if b.GetLastExit() != "" {
					state += " (" + b.GetLastExit() + ")"
				}
				agents := "-"
				if len(b.GetAddressees()) > 0 {
					agents = strings.Join(b.GetAddressees(), ",")
				}
				var pairs []string
				if c := b.GetCaps().GetPersonas(); c != "" {
					pairs = append(pairs, "personas="+c)
				}
				if c := b.GetCaps().GetThreads(); c != "" {
					pairs = append(pairs, "threads="+c)
				}
				caps := ""
				if len(pairs) > 0 {
					caps = "  caps " + strings.Join(pairs, " ")
				}
				fmt.Fprintf(o.out, "#%-20s %-12s policy %-8s agents %s%s\n  %s\n", b.GetName(), state, bridgePolicyNames[b.GetPolicy()], agents, caps, b.GetCommand())
			}
			return nil
		},
	}
}
