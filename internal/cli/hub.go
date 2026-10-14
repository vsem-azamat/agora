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
	"time"

	"github.com/spf13/cobra"

	"github.com/vsem-azamat/agora/internal/bridges"
	"github.com/vsem-azamat/agora/internal/forge"
	"github.com/vsem-azamat/agora/internal/hub"
	"github.com/vsem-azamat/agora/internal/store"
	"github.com/vsem-azamat/agora/internal/web"
)

func hubCmd(o *options, stderr io.Writer) *cobra.Command {
	var dbPath, wakeCommand, compactCommand, interruptCommand, webFlag, webAs, join, bridgePolicy string
	bridgeDelay, delayErr := bridges.DefaultDelay, error(nil)
	if env := os.Getenv("AGORA_BRIDGE_DELAY"); env != "" {
		if bridgeDelay, delayErr = time.ParseDuration(env); delayErr != nil {
			delayErr = fmt.Errorf("AGORA_BRIDGE_DELAY=%q: a duration such as 60s", env)
		}
	}
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
			if delayErr != nil && !cmd.Flags().Changed("bridge-delay") {
				return delayErr
			}
			policy, err := hub.BridgePolicyOf(bridgePolicy)
			if err != nil {
				return fmt.Errorf("--bridge-policy: %w", err)
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
	cmd.Flags().StringVar(&dbPath, "db", defaultDB(), "database file (default $AGORA_DB)")
	cmd.Flags().StringVar(&wakeCommand, "wake-command", os.Getenv("AGORA_WAKE_COMMAND"),
		"shell command that wakes an idle session without a waiting connector; gets $AGORA_TERMINAL and $AGORA_WAKE_TEXT (default $AGORA_WAKE_COMMAND)")
	cmd.Flags().StringVar(&compactCommand, "compact-command", os.Getenv("AGORA_COMPACT_COMMAND"),
		"shell command that compacts a session with a terminal when it is idle; gets $AGORA_TERMINAL and the text to type, /compact and a focus, in $AGORA_WAKE_TEXT (default $AGORA_COMPACT_COMMAND)")
	cmd.Flags().StringVar(&interruptCommand, "interrupt-command", os.Getenv("AGORA_INTERRUPT_COMMAND"),
		"shell command that interrupts the turn of a busy session with a terminal; gets $AGORA_TERMINAL (default $AGORA_INTERRUPT_COMMAND)")
	cmd.Flags().BoolVar(&watchPRs, "watch-prs", watchPRs,
		"follow agents' pull requests and report when CI turns green or red; GitHub needs gh; --watch-prs=false turns it off (default $AGORA_WATCH_PRS)")
	cmd.Flags().StringVar(&webFlag, "web", os.Getenv("AGORA_WEB"),
		"also serve the web app on host:port, or on 127.0.0.1 with only a port; off when empty (default $AGORA_WEB)")
	cmd.Flags().StringVar(&join, "join", envOr("AGORA_JOIN", "offer"),
		"how sessions that have not joined are invited: offer (joining is optional) or expect (every session joins before it works) (default $AGORA_JOIN, else offer)")
	cmd.Flags().StringVar(&bridgePolicy, "bridge-policy", envOr("AGORA_BRIDGE_POLICY", string(bridges.Delay)),
		"outbound policy a bridge gets when it is added: open, delay or read (default $AGORA_BRIDGE_POLICY, else delay)")
	cmd.Flags().DurationVar(&bridgeDelay, "bridge-delay", bridgeDelay,
		"how long an agent's message in a bridged room under delay waits before it goes out (default $AGORA_BRIDGE_DELAY, else 1m)")
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

// webBuilt reports whether the web app is built into this binary; tests replace it.
var webBuilt = web.Built

func loopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
