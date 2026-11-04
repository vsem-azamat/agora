package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/vsem-azamat/agora/internal/bridges"
	"github.com/vsem-azamat/agora/internal/hub"
	"github.com/vsem-azamat/agora/internal/sessions"
)

// hubConfig is the hub's settings as the user gives them: the flags of agora hub, each defaulting
// to its environment variable. agora hub runs with them and agora install service writes the ones
// that are set into the unit, so one declaration serves both and a setting cannot reach only one.
// Every value stays the text the user wrote; resolve checks them.
type hubConfig struct {
	db, wakeCommand, compactCommand, interruptCommand string
	watchPRs, web, webAs, join, bridgePolicy          string
	bridgeDelay                                       string
}

// hubSettings is a hubConfig with its defaults applied and its values checked.
type hubSettings struct {
	db, webAs, join string
	policy          bridges.Policy
	bridgeDelay     time.Duration
	watchPRs        bool
}

// register adds the hub's flags to cmd.
func (c *hubConfig) register(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringVar(&c.db, "db", os.Getenv("AGORA_DB"), "database file (default $AGORA_DB, else the hub's default)")
	f.StringVar(&c.wakeCommand, "wake-command", os.Getenv("AGORA_WAKE_COMMAND"),
		"shell command that wakes an idle session without a waiting connector; gets $AGORA_TERMINAL and $AGORA_WAKE_TEXT (default $AGORA_WAKE_COMMAND)")
	f.StringVar(&c.compactCommand, "compact-command", os.Getenv("AGORA_COMPACT_COMMAND"),
		"shell command that compacts a session with a terminal when it is idle; gets $AGORA_TERMINAL and the text to type, /compact and a focus, in $AGORA_WAKE_TEXT (default $AGORA_COMPACT_COMMAND)")
	f.StringVar(&c.interruptCommand, "interrupt-command", os.Getenv("AGORA_INTERRUPT_COMMAND"),
		"shell command that interrupts the turn of a busy session with a terminal; gets $AGORA_TERMINAL (default $AGORA_INTERRUPT_COMMAND)")
	f.StringVar(&c.watchPRs, "watch-prs", os.Getenv("AGORA_WATCH_PRS"),
		"follow agents' pull requests and report when CI turns green or red; GitHub needs gh; --watch-prs=false turns it off (default $AGORA_WATCH_PRS, else true)")
	f.Lookup("watch-prs").NoOptDefVal = "true"
	f.StringVar(&c.web, "web", os.Getenv("AGORA_WEB"),
		"also serve the web app on host:port, or on 127.0.0.1 with only a port; off when empty (default $AGORA_WEB)")
	f.StringVar(&c.join, "join", os.Getenv("AGORA_JOIN"),
		"how sessions that have not joined are invited: offer (joining is optional) or expect (every session joins before it works) (default $AGORA_JOIN, else offer)")
	f.StringVar(&c.bridgePolicy, "bridge-policy", os.Getenv("AGORA_BRIDGE_POLICY"),
		"outbound policy a bridge gets when it is added: open, delay or read (default $AGORA_BRIDGE_POLICY, else delay)")
	f.StringVar(&c.bridgeDelay, "bridge-delay", os.Getenv("AGORA_BRIDGE_DELAY"),
		"how long an agent's message in a bridged room under delay waits before it goes out, such as 60s (default $AGORA_BRIDGE_DELAY, else 1m)")
	f.StringVar(&c.webAs, "web-as", os.Getenv("AGORA_WEB_AS"), "the name the web app acts under (default $AGORA_WEB_AS, else operator)")
}

// resolve checks the settings and applies the defaults of the ones that are not set. cmd is the
// command that registered c; it tells a value given as a flag from one taken from the environment.
func (c *hubConfig) resolve(cmd *cobra.Command) (hubSettings, error) {
	from := func(flag, env, value string) string {
		if cmd.Flags().Changed(flag) {
			return fmt.Sprintf("--%s %q", flag, value)
		}
		return fmt.Sprintf("%s=%q", env, value)
	}
	s := hubSettings{db: c.db, webAs: c.webAs, join: c.join, policy: bridges.Policy(c.bridgePolicy), bridgeDelay: bridges.DefaultDelay, watchPRs: true}
	if c.watchPRs != "" {
		var err error
		if s.watchPRs, err = strconv.ParseBool(c.watchPRs); err != nil {
			return s, fmt.Errorf("%s: use true or false", from("watch-prs", "AGORA_WATCH_PRS", c.watchPRs))
		}
	}
	if c.bridgeDelay != "" {
		var err error
		if s.bridgeDelay, err = time.ParseDuration(c.bridgeDelay); err != nil {
			return s, fmt.Errorf("%s: a duration such as 60s", from("bridge-delay", "AGORA_BRIDGE_DELAY", c.bridgeDelay))
		}
		if err := hub.ValidBridgeDelay(s.bridgeDelay); err != nil {
			return s, fmt.Errorf("--bridge-delay: %w", err)
		}
	}
	if s.policy == "" {
		s.policy = bridges.Delay
	}
	if err := bridges.ValidPolicy(s.policy); err != nil {
		return s, fmt.Errorf("--bridge-policy: %w", err)
	}
	if s.join == "" {
		s.join = string(sessions.JoinOffer)
	}
	if err := sessions.ValidJoinMode(sessions.JoinMode(s.join)); err != nil {
		return s, fmt.Errorf("--join: %w", err)
	}
	if c.web != "" {
		if _, err := webAddress(c.web); err != nil {
			return s, err
		}
	}
	if s.webAs == "" {
		s.webAs = "operator"
	}
	if s.db == "" {
		s.db = defaultDB()
	}
	return s, nil
}

// args renders the settings that are set as the arguments of agora hub, for the service unit; the
// database path is made absolute. Settings that are not set are left to the hub's defaults.
func (c *hubConfig) args() ([]string, error) {
	var argv []string
	add := func(flag, value string) {
		if value != "" {
			argv = append(argv, "--"+flag, value)
		}
	}
	if c.db != "" {
		abs, err := filepath.Abs(expandHome(c.db))
		if err != nil {
			return nil, err
		}
		add("db", abs)
	}
	add("wake-command", c.wakeCommand)
	add("compact-command", c.compactCommand)
	add("interrupt-command", c.interruptCommand)
	if c.watchPRs != "" {
		// a bool flag takes its value only after "=", so the unit says it in one argument
		argv = append(argv, "--watch-prs="+c.watchPRs)
	}
	add("web", c.web)
	if c.web != "" {
		add("web-as", c.webAs)
	}
	add("join", c.join)
	add("bridge-policy", c.bridgePolicy)
	add("bridge-delay", c.bridgeDelay)
	return argv, nil
}
