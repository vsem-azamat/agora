package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/vsem-azamat/agora/internal/install"
)

// goos is the operating system install targets check; tests replace it.
var goos = runtime.GOOS

// systemctl runs `systemctl --user args...` and returns its combined output; tests replace it.
var systemctl = func(ctx context.Context, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, "systemctl", append([]string{"--user"}, args...)...).CombinedOutput() //nolint:gosec // fixed binary; arguments come from the install targets
}

var targets = []struct{ name, what string }{
	{"claude-code", "the Claude Code hooks that report sessions, deliver messages and wake idle agents"},
	{"service", "a systemd user service that runs the hub (Linux)"},
	{"skill", "the agent guide, as a skill for agent tools that read skills"},
}

func listTargets(w io.Writer, verb string) {
	fmt.Fprintf(w, "Usage: agora %s <target>\n\nTargets:\n", verb)
	for _, t := range targets {
		fmt.Fprintf(w, "  %-12s %s\n", t.name, t.what)
	}
	fmt.Fprintf(w, "\nRun `agora %s <target> --help` for its options.\n", verb)
}

// agoraBinary is the absolute path hooks and the service call: --bin when given, else the
// running binary, by its path on PATH when that is the same file (so a link such as one in a
// package manager's bin directory survives upgrades). A temporary build (`go run`, a test
// binary) is refused, since it disappears.
func agoraBinary(flag string) (string, error) {
	if flag != "" {
		abs, err := filepath.Abs(expandHome(flag))
		if err != nil {
			return "", err
		}
		if fi, err := os.Stat(abs); err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm()&0o111 == 0 {
			return "", fmt.Errorf("--bin %s: not an executable file", flag)
		}
		return abs, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("cannot find the agora binary: %w; pass --bin <path>", err)
	}
	if exe, err = filepath.Abs(exe); err != nil {
		return "", err
	}
	if p, err := exec.LookPath("agora"); err == nil {
		if abs, err := filepath.Abs(p); err == nil && sameFile(abs, exe) {
			return abs, nil
		}
	}
	if temporaryPath(exe) {
		return "", fmt.Errorf("%s is a temporary build that will disappear; install agora (go install) and run that, or pass --bin <path>", exe)
	}
	return exe, nil
}

func sameFile(a, b string) bool {
	fa, err := os.Stat(a)
	if err != nil {
		return false
	}
	fb, err := os.Stat(b)
	return err == nil && os.SameFile(fa, fb)
}

// temporaryPath reports whether p lies in the temporary directory or a Go build directory.
func temporaryPath(p string) bool {
	if strings.Contains(p, string(filepath.Separator)+"go-build") {
		return true
	}
	tmp, err := filepath.Abs(os.TempDir())
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(tmp, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

const settingsHelp = "Claude Code settings file (default $CLAUDE_CONFIG_DIR/settings.json, else ~/.claude/settings.json)"

const binHelp = "agora binary the integration runs (default: the running binary, by its PATH location when that is the same file)"

// printWritten reports what writing the file at path of what (a hub service, an agent skill) did.
func printWritten(w io.Writer, outcome install.Outcome, what, path string) {
	switch outcome {
	case install.Unchanged:
		fmt.Fprintf(w, "%s already installed: %s\n", what, path)
	case install.Updated:
		fmt.Fprintf(w, "updated the %s: %s\n", what, path)
	default:
		fmt.Fprintf(w, "wrote the %s: %s\n", what, path)
	}
}

func printForeign(w io.Writer, cmds []string) {
	for _, c := range cmds {
		fmt.Fprintf(w, "warning: found an Agora-like hook it did not write, left as it is: %s\n", c)
	}
}

func installCmd(o *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "install [claude-code|service|skill]",
		Short: "Install Agora's hooks, hub service or agent skill; safe to run again",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			listTargets(o.out, "install")
			return nil
		},
	}
	cmd.AddCommand(installClaudeCodeCmd(o), installServiceCmd(o), installSkillCmd(o))
	return cmd
}

func uninstallCmd(o *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "uninstall [claude-code|service|skill]",
		Short: "Remove what `agora install` added",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			listTargets(o.out, "uninstall")
			return nil
		},
	}
	cmd.AddCommand(uninstallClaudeCodeCmd(o), uninstallServiceCmd(o), uninstallSkillCmd(o))
	return cmd
}

// --- claude-code ---------------------------------------------------------------------

func settingsPath(flag string) (string, error) {
	if flag != "" {
		return filepath.Abs(expandHome(flag))
	}
	return install.ClaudeCodeSettings()
}

func installClaudeCodeCmd(o *options) *cobra.Command {
	var settings, terminalEnv, binFlag string
	cmd := &cobra.Command{
		Use:   "claude-code",
		Short: "Add the Agora hooks to Claude Code's user settings",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := settingsPath(settings)
			if err != nil {
				return err
			}
			bin, err := agoraBinary(binFlag)
			if err != nil {
				return err
			}
			ch, err := install.InstallClaudeCode(path, bin, terminalEnv, time.Now())
			if err != nil {
				return err
			}
			printForeign(cmd.ErrOrStderr(), ch.Foreign)
			if ch.Outcome == install.Unchanged {
				fmt.Fprintf(o.out, "Claude Code hooks already installed in %s\n", path)
				return nil
			}
			fmt.Fprintf(o.out, "installed Claude Code hooks in %s\n", path)
			printBackup(o.out, ch.Backup)
			fmt.Fprintln(o.out, "new Claude Code sessions use them; the hub must be running (`agora hub`, or `agora install service`)")
			return nil
		},
	}
	cmd.Flags().StringVar(&settings, "settings", "", settingsHelp)
	cmd.Flags().StringVar(&terminalEnv, "terminal-env", "",
		"environment variable that holds the session's terminal handle, passed to the hub as $AGORA_TERMINAL for its wake command")
	cmd.Flags().StringVar(&binFlag, "bin", "", binHelp)
	return cmd
}

func uninstallClaudeCodeCmd(o *options) *cobra.Command {
	var settings, binFlag string
	cmd := &cobra.Command{
		Use:   "claude-code",
		Short: "Remove the Agora hooks from Claude Code's user settings",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := settingsPath(settings)
			if err != nil {
				return err
			}
			bin, err := agoraBinary(binFlag)
			if err != nil {
				bin = "" // entries are still recognised by a binary named agora
			}
			ch, err := install.UninstallClaudeCode(path, bin, time.Now())
			if err != nil {
				return err
			}
			printForeign(cmd.ErrOrStderr(), ch.Foreign)
			if ch.Outcome == install.Absent {
				fmt.Fprintf(o.out, "Claude Code hooks not installed in %s\n", path)
				return nil
			}
			fmt.Fprintf(o.out, "removed Claude Code hooks from %s\n", path)
			printBackup(o.out, ch.Backup)
			return nil
		},
	}
	cmd.Flags().StringVar(&settings, "settings", "", settingsHelp)
	cmd.Flags().StringVar(&binFlag, "bin", "", "also remove hooks that run this binary, whatever its name")
	return cmd
}

func printBackup(w io.Writer, path string) {
	if path != "" {
		fmt.Fprintf(w, "backup of the previous file: %s\n", path)
	}
}

// --- service -------------------------------------------------------------------------

func needLinux() error {
	if goos != "linux" {
		return fmt.Errorf("the hub service is a systemd user unit and needs Linux; on %s, run `agora hub` under your system's service manager", goos)
	}
	return nil
}

// runSystemctl runs each systemctl command in order and stops at the first failure, after
// checking that the user's service manager answers.
func (o *options) runSystemctl(ctx context.Context, cmds [][]string) error {
	if out, err := systemctl(ctx, "show-environment"); err != nil {
		return fmt.Errorf("the systemd user manager is not reachable (systemctl --user: %w %s); run the commands yourself where it is, or leave out --now",
			err, strings.TrimSpace(string(out)))
	}
	for _, args := range cmds {
		fmt.Fprintf(o.out, "running systemctl --user %s\n", strings.Join(args, " "))
		if out, err := systemctl(ctx, args...); err != nil {
			return fmt.Errorf("systemctl --user %s: %w\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

func printSystemctl(w io.Writer, cmds [][]string) {
	parts := make([]string, len(cmds))
	for i, args := range cmds {
		parts[i] = "systemctl --user " + strings.Join(args, " ")
	}
	fmt.Fprintf(w, "  %s\n", strings.Join(parts, " && "))
}

func installServiceCmd(o *options) *cobra.Command {
	var dbPath, wakeCommand, webFlag, webAs, binFlag string
	var now bool
	cmd := &cobra.Command{
		Use:   "service",
		Short: "Write a systemd user unit that runs the hub (Linux)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := needLinux(); err != nil {
				return err
			}
			bin, err := agoraBinary(binFlag)
			if err != nil {
				return err
			}
			argv := []string{bin, "hub"}
			if cmd.Flags().Changed("socket") || os.Getenv("AGORA_SOCKET") != "" {
				abs, err := filepath.Abs(o.socket)
				if err != nil {
					return err
				}
				argv = append(argv, "--socket", abs)
			}
			if dbPath != "" {
				abs, err := filepath.Abs(expandHome(dbPath))
				if err != nil {
					return err
				}
				argv = append(argv, "--db", abs)
			}
			if wakeCommand != "" {
				argv = append(argv, "--wake-command", wakeCommand)
			}
			if webFlag != "" {
				if _, err := webAddress(webFlag); err != nil {
					return err
				}
				argv = append(argv, "--web", webFlag)
			}
			if webFlag != "" && webAs != "" {
				argv = append(argv, "--web-as", webAs)
			}
			dir, err := install.ServiceDir()
			if err != nil {
				return err
			}
			outcome, err := install.InstallService(dir, install.Unit(argv))
			if err != nil {
				return err
			}
			printWritten(o.out, outcome, "hub service", install.ServicePath(dir))
			cmds := [][]string{{"daemon-reload"}, {"enable", "--now", install.ServiceName}}
			if outcome == install.Updated {
				cmds = append(cmds, []string{"restart", install.ServiceName})
			}
			if now {
				return o.runSystemctl(cmd.Context(), cmds)
			}
			fmt.Fprintln(o.out, "start it, and with every login, with:")
			printSystemctl(o.out, cmds[:2])
			if outcome == install.Updated {
				fmt.Fprintln(o.out, "and restart it to apply the change:")
				printSystemctl(o.out, cmds[2:])
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&dbPath, "db", os.Getenv("AGORA_DB"), "database file for the hub (default $AGORA_DB, else the hub's default)")
	cmd.Flags().StringVar(&wakeCommand, "wake-command", os.Getenv("AGORA_WAKE_COMMAND"), "the hub's wake command (default $AGORA_WAKE_COMMAND)")
	cmd.Flags().StringVar(&webFlag, "web", os.Getenv("AGORA_WEB"), "serve the web app on host:port or a port (default $AGORA_WEB)")
	cmd.Flags().StringVar(&webAs, "web-as", os.Getenv("AGORA_WEB_AS"), "the name the web app acts under (default $AGORA_WEB_AS)")
	cmd.Flags().BoolVar(&now, "now", false, "also run systemctl to load, enable and start the service")
	cmd.Flags().StringVar(&binFlag, "bin", "", binHelp)
	return cmd
}

func uninstallServiceCmd(o *options) *cobra.Command {
	var now bool
	cmd := &cobra.Command{
		Use:   "service",
		Short: "Remove the hub's systemd user unit",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := needLinux(); err != nil {
				return err
			}
			dir, err := install.ServiceDir()
			if err != nil {
				return err
			}
			path := install.ServicePath(dir)
			installed, err := install.ServiceInstalled(dir)
			if err != nil {
				return err
			}
			if installed && now {
				if err := o.runSystemctl(cmd.Context(), [][]string{{"disable", "--now", install.ServiceName}}); err != nil {
					return err
				}
			}
			outcome, err := install.UninstallService(dir)
			if err != nil {
				return err
			}
			if outcome == install.Absent {
				fmt.Fprintf(o.out, "hub service not installed: no %s\n", path)
				return nil
			}
			fmt.Fprintf(o.out, "removed the hub service: %s\n", path)
			if now {
				return o.runSystemctl(cmd.Context(), [][]string{{"daemon-reload"}})
			}
			fmt.Fprintln(o.out, "stop the running hub and forget the unit with:")
			printSystemctl(o.out, [][]string{{"stop", install.ServiceName}, {"daemon-reload"}})
			return nil
		},
	}
	cmd.Flags().BoolVar(&now, "now", false, "also run systemctl to stop and disable the service")
	return cmd
}

// --- skill ---------------------------------------------------------------------------

func skillDirs(flags []string) ([]string, error) {
	if len(flags) == 0 {
		d, err := install.SkillDir()
		if err != nil {
			return nil, err
		}
		return []string{d}, nil
	}
	dirs := make([]string, len(flags))
	for i, f := range flags {
		abs, err := filepath.Abs(expandHome(f))
		if err != nil {
			return nil, err
		}
		dirs[i] = abs
	}
	return dirs, nil
}

const skillDirHelp = "skills directory to use (repeatable; default $CLAUDE_CONFIG_DIR/skills, else ~/.claude/skills)"

func installSkillCmd(o *options) *cobra.Command {
	var flags []string
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "Write the agent guide as agora/SKILL.md into skills directories",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dirs, err := skillDirs(flags)
			if err != nil {
				return err
			}
			for _, d := range dirs {
				outcome, err := install.InstallSkill(d)
				if err != nil {
					return err
				}
				printWritten(o.out, outcome, "agent skill", install.SkillPath(d))
			}
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&flags, "dir", nil, skillDirHelp)
	return cmd
}

func uninstallSkillCmd(o *options) *cobra.Command {
	var flags []string
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "Remove the agent guide from skills directories",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dirs, err := skillDirs(flags)
			if err != nil {
				return err
			}
			for _, d := range dirs {
				outcome, err := install.UninstallSkill(d)
				if err != nil {
					return err
				}
				if outcome == install.Absent {
					fmt.Fprintf(o.out, "agent skill not installed: no %s\n", install.SkillPath(d))
				} else {
					fmt.Fprintf(o.out, "removed the agent skill: %s\n", install.SkillPath(d))
				}
			}
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&flags, "dir", nil, skillDirHelp)
	return cmd
}
