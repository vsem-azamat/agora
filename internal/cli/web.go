package cli

import (
	"fmt"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	agorav1 "github.com/vsem-azamat/agora/gen/agora/v1"
)

func webCmd(o *options) *cobra.Command {
	cmd := &cobra.Command{Use: "web", Short: "The web app: its access token", Args: cobra.NoArgs}
	var rotate bool
	token := &cobra.Command{
		Use:   "token",
		Short: "Print the web app's access token and the address to open; --rotate replaces it",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			resp, err := o.web().Token(cmd.Context(), connect.NewRequest(&agorav1.TokenRequest{Rotate: rotate}))
			if err != nil {
				return err
			}
			m := resp.Msg
			fmt.Fprintln(o.out, m.GetToken())
			if m.GetWebAddress() == "" {
				fmt.Fprintln(o.out, "the hub serves no web app; start it with --web <port> to open it in a browser")
				return nil
			}
			fmt.Fprintf(o.out, "open: http://%s/#token=%s\n", m.GetWebAddress(), m.GetToken())
			if rotate {
				fmt.Fprintln(o.out, "the old token no longer works; browsers that used it ask for the new one")
			}
			return nil
		},
	}
	token.Flags().BoolVar(&rotate, "rotate", false, "replace the token; browsers holding the old one must sign in again")
	cmd.AddCommand(token)
	return cmd
}
