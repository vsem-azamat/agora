package cli

import (
	"context"
	"fmt"
	"strings"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	agorav1 "github.com/vsem-azamat/agora/gen/agora/v1"
)

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

// roomArg is a room name as typed, with or without its leading #.
func roomArg(s string) string { return strings.TrimPrefix(s, "#") }

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
			room := roomArg(args[0])
			if _, err := o.rooms().CreateRoom(cmd.Context(), connect.NewRequest(&agorav1.CreateRoomRequest{
				Name: room, Purpose: strings.Join(args[1:], " "), Agent: name,
			})); err != nil {
				return err
			}
			fmt.Fprintf(o.out, "created #%s; you follow it\n", room)
			return nil
		},
	}
}

// subscriptionModes are the modes `agora subscribe --mode` takes.
var subscriptionModes = map[string]agorav1.SubscriptionMode{
	"all":      agorav1.SubscriptionMode_SUBSCRIPTION_MODE_ALL,
	"mentions": agorav1.SubscriptionMode_SUBSCRIPTION_MODE_MENTIONS,
	"wake":     agorav1.SubscriptionMode_SUBSCRIPTION_MODE_WAKE,
}

// modeLabel is how a subscription mode is shown after its room: nothing for all.
func modeLabel(m agorav1.SubscriptionMode) string {
	for name, v := range subscriptionModes {
		if v == m && m != agorav1.SubscriptionMode_SUBSCRIPTION_MODE_ALL {
			return " (" + name + ")"
		}
	}
	return ""
}

func subscribeCmd(o *options, follow bool) *cobra.Command {
	use, short := "subscribe <room...>", "Follow rooms: their new messages count as unread for you (--mode mentions: only those addressed to you; --mode wake: they also wake you)"
	if !follow {
		use, short = "unsubscribe <room...>", "Stop following rooms (#general stays)"
	}
	var mode string
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := &agorav1.SubscribeRequest{Rooms: args, Follow: follow}
			if mode != "" {
				m, ok := subscriptionModes[mode]
				if !ok {
					return fmt.Errorf("--mode %q: all, mentions or wake", mode)
				}
				req.Mode = m
			}
			name, err := o.agent(cmd.Context())
			if err != nil {
				return err
			}
			req.Agent = name
			resp, err := o.rooms().Subscribe(cmd.Context(), connect.NewRequest(req))
			if err != nil {
				return err
			}
			var rooms []string
			for _, s := range resp.Msg.GetSubscriptions() {
				rooms = append(rooms, "#"+s.GetRoom()+modeLabel(s.GetMode()))
			}
			fmt.Fprintf(o.out, "%s follows: %s\n", name, strings.Join(rooms, " "))
			return nil
		},
	}
	if follow {
		cmd.Flags().StringVar(&mode, "mode", "", "all (every message is unread; the default for a new room), mentions (only messages addressed to you) or wake (every message is unread and wakes you); a followed room keeps its mode without it")
	}
	return cmd
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
			text, err := textArg(cmd, args[1:], "message")
			if err != nil {
				return err
			}
			resp, err := o.rooms().Post(cmd.Context(), connect.NewRequest(&agorav1.PostRequest{
				Agent: name, Room: roomArg(args[0]), Body: text, ReplyTo: reply,
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
			resp, err := o.rooms().History(cmd.Context(), connect.NewRequest(&agorav1.HistoryRequest{Room: roomArg(args[0]), Last: last}))
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
