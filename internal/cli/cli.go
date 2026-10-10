// Package cli implements the agora command.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
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
	root := newRoot(stdout, stderr) //nolint:contextcheck // commands receive ctx through ExecuteContext below
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
	client *http.Client // over the socket; built on first use
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

	root.AddCommand(hubCmd(o, stderr), joinCmd(o), setCmd(o), renameCmd(o), leaveCmd(o), statusCmd(o), whoCmd(o),
		proposeCmd(o), voteCmd(o), closeCmd(o), proposalsCmd(o), charterCmd(o),
		roomsCmd(o), roomCreateCmd(o), subscribeCmd(o, true), subscribeCmd(o, false), postCmd(o), readCmd(o), unreadCmd(o),
		whoamiCmd(o), sessionsCmd(o), hookCmd(o), installCmd(o), uninstallCmd(o),
		queueCmd(o), lockCmd(o), unlockCmd(o), locksCmd(o), webCmd(o))
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
