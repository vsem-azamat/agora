package cli

import (
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
	cmd.AddCommand(bridgeAddCmd(o), bridgeRemoveCmd(o), bridgeListCmd(o))
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
			fmt.Fprintf(o.out, "%s; its bridge runs with the outbound policy approve: the operator sends or declines agents' messages in the web app\n", room)
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
	agorav1.BridgePolicy_BRIDGE_POLICY_APPROVE: "approve",
	agorav1.BridgePolicy_BRIDGE_POLICY_OPEN:    "open",
	agorav1.BridgePolicy_BRIDGE_POLICY_READ:    "read",
}

func bridgeListCmd(o *options) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List bridges with their state, outbound policy, agents and command",
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
				fmt.Fprintf(o.out, "#%-20s %-12s policy %-8s agents %s\n  %s\n", b.GetName(), state, bridgePolicyNames[b.GetPolicy()], agents, b.GetCommand())
			}
			return nil
		},
	}
}
