package cli

import (
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/vsem-azamat/agora/internal/forge"
	"github.com/vsem-azamat/agora/internal/hub"
	"github.com/vsem-azamat/agora/internal/store"
)

func hubCmd(o *options, stderr io.Writer) *cobra.Command {
	var dbPath, wakeCommand, webFlag, webAs string
	watchPRs, watchErr := true, error(nil)
	if env := os.Getenv("AGORA_WATCH_PRS"); env != "" {
		if watchPRs, watchErr = strconv.ParseBool(env); watchErr != nil {
			watchErr = fmt.Errorf("AGORA_WATCH_PRS=%q: use true or false", env)
		}
	}
	cmd := &cobra.Command{
		Use:   "hub",
		Short: "Run the hub",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if watchErr != nil && !cmd.Flags().Changed("watch-prs") {
				return watchErr
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			log := slog.New(slog.NewTextHandler(stderr, nil))
			db, err := store.Open(ctx, dbPath)
			if err != nil {
				return err
			}
			defer db.Close()
			var webAddr string
			if webFlag != "" {
				if webAddr, err = webAddress(webFlag); err != nil {
					return err
				}
			}
			l, err := hub.Listen(o.socket)
			if err != nil {
				return err
			}
			h := hub.Open(db, nil, log)
			if webAddr != "" {
				wl, err := net.Listen("tcp", webAddr)
				if err != nil {
					l.Close()
					return fmt.Errorf("--web: %w", err)
				}
				if err := h.EnableWeb(ctx, wl, webAs); err != nil {
					wl.Close()
					l.Close()
					return err
				}
				webAddr = wl.Addr().String()
				if host, _, _ := net.SplitHostPort(webAddr); !loopback(host) {
					log.Warn("the web app listens on an address that is not a loopback address: its token crosses the network in plain text unless a proxy adds TLS", "web", webAddr)
				}
			}
			forges := forge.Forges{}
			if watchPRs {
				if _, err := exec.LookPath("gh"); err == nil {
					forges["github.com"] = &forge.GitHub{}
				} else {
					log.Info("gh is not installed: pull requests on GitHub are not followed")
				}
			}
			webAttrs := []any{"web", webAddr}
			if webAddr != "" {
				webAttrs = append(webAttrs, "web_as", webAs)
			}
			log.Info("hub listening", append([]any{"socket", o.socket, "db", dbPath, "wake_command", wakeCommand != "", "watch_prs", slices.Sorted(maps.Keys(forges))}, webAttrs...)...)
			h.WakeCommand = wakeCommand
			h.Forges = forges
			return h.Serve(ctx, l)
		},
	}
	cmd.Flags().StringVar(&dbPath, "db", defaultDB(), "database file (default $AGORA_DB)")
	cmd.Flags().StringVar(&wakeCommand, "wake-command", os.Getenv("AGORA_WAKE_COMMAND"),
		"shell command that wakes an idle session without a waiting connector; gets $AGORA_TERMINAL and $AGORA_WAKE_TEXT (default $AGORA_WAKE_COMMAND)")
	cmd.Flags().BoolVar(&watchPRs, "watch-prs", watchPRs,
		"follow agents' pull requests and report when CI turns green or red; GitHub needs gh; --watch-prs=false turns it off (default $AGORA_WATCH_PRS)")
	cmd.Flags().StringVar(&webFlag, "web", os.Getenv("AGORA_WEB"),
		"also serve the web app on host:port, or on 127.0.0.1 with only a port; off when empty (default $AGORA_WEB)")
	cmd.Flags().StringVar(&webAs, "web-as", envOr("AGORA_WEB_AS", "operator"), "the name the web app acts under (default $AGORA_WEB_AS, else operator)")
	return cmd
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

// webAddress turns a --web value, host:port or only a port, into the address to listen on;
// without a host it is 127.0.0.1.
func webAddress(s string) (string, error) {
	in := s
	if !strings.Contains(s, ":") {
		s = ":" + s
	}
	host, port, err := net.SplitHostPort(s)
	if err == nil {
		_, err = strconv.ParseUint(port, 10, 16)
	}
	if err != nil {
		return "", fmt.Errorf("--web %q: use host:port or only a port, like 127.0.0.1:8484, [::1]:8484 or 8484", in)
	}
	if host == "" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port), nil
}

func loopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
