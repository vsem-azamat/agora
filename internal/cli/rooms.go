package cli

import (
	"context"
	"fmt"
	"strings"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	agorav1 "github.com/vsem-azamat/agora/gen/agora/v1"
	"github.com/vsem-azamat/agora/internal/rooms"
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
			text, err := textArg(cmd, args[1:], "message")
			if err != nil {
				return err
			}
			resp, err := o.rooms().Post(cmd.Context(), connect.NewRequest(&agorav1.PostRequest{
				Author: name, Room: roomArg(args[0]), Body: text, ReplyTo: reply,
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

func formatMessage(m *agorav1.Message) string {
	return rooms.Format(rooms.Message{
		ID: m.GetId(), Room: m.GetRoom(), Author: m.GetAuthor(), Body: m.GetBody(),
		ReplyTo: m.GetReplyTo(), At: m.GetAt().AsTime(), Addressed: m.GetAddressed(),
	}, 0)
}
