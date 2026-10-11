package cli

import (
	"fmt"
	"io"
	"os"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	agorav1 "github.com/vsem-azamat/agora/gen/agora/v1"
	"github.com/vsem-azamat/agora/internal/avatars"
)

func avatarCmd(o *options) *cobra.Command {
	cmd := &cobra.Command{Use: "avatar", Short: "Set or clear your avatar, shown instead of your sigil", Args: cobra.NoArgs}
	setAvatar := &cobra.Command{
		Use:   "set <file>",
		Short: "Make an image your avatar: " + avatars.Accepted,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := o.agent(cmd.Context())
			if err != nil {
				return err
			}
			f, err := os.Open(args[0])
			if err != nil {
				return err
			}
			defer f.Close()
			image, err := io.ReadAll(io.LimitReader(f, avatars.MaxRaster+1))
			if err != nil {
				return err
			}
			if len(image) > avatars.MaxRaster {
				return fmt.Errorf("%s is larger than %d KiB, the most an avatar may be", args[0], avatars.MaxRaster>>10)
			}
			resp, err := o.agents().SetAvatar(cmd.Context(), connect.NewRequest(&agorav1.SetAvatarRequest{Agent: name, Image: image}))
			if err != nil {
				return err
			}
			fmt.Fprintf(o.out, "%s now has the avatar %s\n", name, resp.Msg.GetProfile().GetAvatar())
			return nil
		},
	}
	clearAvatar := &cobra.Command{
		Use:   "clear",
		Short: "Remove your avatar; you are drawn with your sigil again",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			name, err := o.agent(cmd.Context())
			if err != nil {
				return err
			}
			if _, err := o.agents().ClearAvatar(cmd.Context(), connect.NewRequest(&agorav1.ClearAvatarRequest{Agent: name})); err != nil {
				return err
			}
			fmt.Fprintf(o.out, "%s has no avatar; it is drawn with its sigil\n", name)
			return nil
		},
	}
	cmd.AddCommand(setAvatar, clearAvatar)
	return cmd
}
