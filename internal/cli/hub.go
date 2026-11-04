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
	"github.com/vsem-azamat/agora/internal/web"
)

func hubCmd(o *options, stderr io.Writer) *cobra.Command {
	var cfg hubConfig
	cmd := &cobra.Command{
		Use:   "hub",
		Short: "Run the hub",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			set, err := cfg.resolve(cmd)
			if err != nil {
				return err
			}
			dbPath, wakeCommand, compactCommand, interruptCommand := set.db, cfg.wakeCommand, cfg.compactCommand, cfg.interruptCommand
			webFlag, webAs, join, policy, bridgeDelay, watchPRs := cfg.web, set.webAs, set.join, set.policy, set.bridgeDelay, set.watchPRs
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
			if err := h.SetJoinMode(join); err != nil {
				l.Close()
				return fmt.Errorf("--join: %w", err)
			}
			if err := h.SetBridgeDefaults(policy, bridgeDelay); err != nil {
				l.Close()
				return fmt.Errorf("--bridge-delay: %w", err)
			}
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
				if !webBuilt() {
					log.Warn("the web app is not built into this binary: the web listener serves only the API; install a release archive, or run make build-web before building", "web", webAddr)
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
			log.Info("hub listening", append([]any{
				"socket", o.socket, "db", dbPath, "wake_command", wakeCommand != "", "compact_command", compactCommand != "", "interrupt_command", interruptCommand != "", "watch_prs", slices.Sorted(maps.Keys(forges)),
				"join", join, "bridge_policy", policy, "bridge_delay", bridgeDelay,
			}, webAttrs...)...)
			h.WakeCommand, h.CompactCommand, h.InterruptCommand = wakeCommand, compactCommand, interruptCommand
			h.Forges = forges
			return h.Serve(ctx, l)
		},
	}
	cfg.register(cmd)
	return cmd
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

// webBuilt reports whether the web app is built into this binary; tests replace it.
var webBuilt = web.Built

func loopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
