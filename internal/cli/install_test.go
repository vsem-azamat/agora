package cli_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/vsem-azamat/agora/internal/cli"
	"github.com/vsem-azamat/agora/internal/install"
)

// run runs agora without a hub, as a user would run install commands.
func run(args ...string) result {
	var out, errOut bytes.Buffer
	code := cli.Run(context.Background(), args, &out, &errOut)
	return result{code, out.String(), errOut.String()}
}

// isolate points every location install uses at a fresh temporary home.
func isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, v := range []string{"CLAUDE_CONFIG_DIR", "XDG_CONFIG_HOME", "AGORA_SOCKET", "AGORA_DB", "AGORA_WAKE_COMMAND"} {
		t.Setenv(v, "")
	}
	return home
}

// fakeBin is an executable file named agora to pass with --bin.
func fakeBin(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "bin", "agora")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestTemporaryBinaryIsRefused(t *testing.T) {
	home := isolate(t)
	t.Setenv("PATH", t.TempDir())
	r := run("install", "claude-code")
	if r.code == 0 || !strings.Contains(r.stderr, "--bin") {
		t.Fatalf("test binary accepted: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude")); !os.IsNotExist(err) {
		t.Fatalf("wrote files: %v", err)
	}
	if r := run("install", "claude-code", "--bin", filepath.Join(home, "nothing")); r.code == 0 {
		t.Fatalf("missing --bin accepted: %+v", r)
	}
}

func TestBinaryOnPathIsPreferred(t *testing.T) {
	home := isolate(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(exe, filepath.Join(dir, "agora")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	if r := run("install", "claude-code"); r.code != 0 {
		t.Fatalf("install: %+v", r)
	}
	b, _ := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if !strings.Contains(string(b), `"`+filepath.Join(dir, "agora")+` hook claude-code"`) {
		t.Fatalf("hooks do not use the PATH location:\n%s", b)
	}
}

func TestInstallListsTargets(t *testing.T) {
	for _, cmd := range []string{"install", "uninstall"} {
		r := run(cmd)
		if r.code != 0 {
			t.Fatalf("%s: %+v", cmd, r)
		}
		for _, target := range []string{"claude-code", "service", "skill"} {
			if !strings.Contains(r.stdout, target) {
				t.Errorf("%s does not list %s:\n%s", cmd, target, r.stdout)
			}
		}
	}
	if r := run("install", "nothing"); r.code == 0 {
		t.Errorf("unknown target accepted: %+v", r)
	}
}

func TestInstallClaudeCodeCommand(t *testing.T) {
	home := isolate(t)
	exe := fakeBin(t)
	settings := filepath.Join(home, ".claude", "settings.json")
	r := run("install", "claude-code", "--bin", exe)
	if r.code != 0 || !strings.Contains(r.stdout, settings) {
		t.Fatalf("install: %+v", r)
	}
	b, err := os.ReadFile(settings)
	if err != nil || !strings.Contains(string(b), exe+" hook claude-code") {
		t.Fatalf("settings: %v\n%s", err, b)
	}
	if r := run("install", "claude-code", "--bin", exe); r.code != 0 || !strings.Contains(r.stdout, "already installed") {
		t.Fatalf("second install: %+v", r)
	}
	if r := run("install", "claude-code", "--bin", exe, "--terminal-env", "1BAD;rm"); r.code == 0 {
		t.Fatalf("invalid --terminal-env accepted: %+v", r)
	}
	if after, _ := os.ReadFile(settings); string(after) != string(b) {
		t.Fatal("refused install changed the settings")
	}
	r = run("install", "claude-code", "--bin", exe, "--terminal-env", "TERM_HANDLE")
	if r.code != 0 || !strings.Contains(r.stdout, "backup") {
		t.Fatalf("terminal-env install: %+v", r)
	}
	if b, _ := os.ReadFile(settings); !strings.Contains(string(b), `AGORA_TERMINAL=\"${TERM_HANDLE:-$AGORA_TERMINAL}\" `+exe+" hook claude-code") {
		t.Fatalf("terminal env not in hooks:\n%s", b)
	}
	if r := run("uninstall", "claude-code"); r.code != 0 || !strings.Contains(r.stdout, "removed") {
		t.Fatalf("uninstall: %+v", r)
	}
	if r := run("uninstall", "claude-code"); r.code != 0 || !strings.Contains(r.stdout, "not installed") {
		t.Fatalf("second uninstall: %+v", r)
	}
}

func TestInstallClaudeCodeFollowsConfigDir(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	bin := fakeBin(t)
	if r := run("install", "claude-code", "--bin", bin); r.code != 0 {
		t.Fatalf("install: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(dir, "settings.json")); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(t.TempDir(), "s.json")
	if r := run("install", "claude-code", "--bin", bin, "--settings", other); r.code != 0 {
		t.Fatalf("install --settings: %+v", r)
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatal(err)
	}
}

func TestInstallService(t *testing.T) {
	home := isolate(t)
	defer cli.SetGOOS("linux")()
	var calls []string
	defer cli.SetSystemctl(func(_ context.Context, args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, " "))
		return nil, nil
	})()
	exe := fakeBin(t)
	unit := filepath.Join(home, ".config", "systemd", "user", "agora-hub.service")

	r := run("install", "service", "--bin", exe)
	if r.code != 0 || !strings.Contains(r.stdout, unit) ||
		!strings.Contains(r.stdout, "systemctl --user daemon-reload && systemctl --user enable --now agora-hub") || len(calls) != 0 {
		t.Fatalf("install: %+v, calls %q", r, calls)
	}
	b, _ := os.ReadFile(unit)
	if !strings.Contains(string(b), "ExecStart="+exe+" hub\n") {
		t.Fatalf("unit:\n%s", b)
	}

	t.Setenv("AGORA_WAKE_COMMAND", `notify "$AGORA_TERMINAL"`)
	r = run("install", "service", "--bin", exe, "--db", "/srv/agora/agora.db", "--now")
	if r.code != 0 {
		t.Fatalf("install --now: %+v", r)
	}
	if want := []string{"show-environment", "daemon-reload", "enable --now agora-hub", "restart agora-hub"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("systemctl calls %q, want %q", calls, want)
	}
	b, _ = os.ReadFile(unit)
	if !strings.Contains(string(b), `hub --db /srv/agora/agora.db --wake-command "notify \"$$AGORA_TERMINAL\""`) {
		t.Fatalf("unit:\n%s", b)
	}

	calls = nil
	if r := run("uninstall", "service", "--now"); r.code != 0 {
		t.Fatalf("uninstall --now: %+v", r)
	}
	if want := []string{"show-environment", "disable --now agora-hub", "show-environment", "daemon-reload"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("systemctl calls %q, want %q", calls, want)
	}
	if _, err := os.Stat(unit); !os.IsNotExist(err) {
		t.Fatalf("unit left: %v", err)
	}
	run("install", "service", "--bin", exe)
	calls = nil
	r = run("uninstall", "service")
	if r.code != 0 || !strings.Contains(r.stdout, "systemctl --user stop agora-hub && systemctl --user daemon-reload") || len(calls) != 0 {
		t.Fatalf("uninstall: %+v, calls %q", r, calls)
	}
}

func TestNowNeedsTheUserManager(t *testing.T) {
	home := isolate(t)
	defer cli.SetGOOS("linux")()
	var calls []string
	defer cli.SetSystemctl(func(_ context.Context, args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, " "))
		return []byte("Failed to connect to bus"), errors.New("exit status 1")
	})()
	r := run("install", "service", "--bin", fakeBin(t), "--now")
	if r.code == 0 || !strings.Contains(r.stderr, "user manager is not reachable") || !reflect.DeepEqual(calls, []string{"show-environment"}) {
		t.Fatalf("install --now without a manager: %+v, calls %q", r, calls)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "systemd", "user", "agora-hub.service")); err != nil {
		t.Fatalf("unit not written: %v", err)
	}
}

func TestServiceNeedsLinux(t *testing.T) {
	home := isolate(t)
	defer cli.SetGOOS("darwin")()
	for _, cmd := range []string{"install", "uninstall"} {
		r := run(cmd, "service")
		if r.code == 0 || !strings.Contains(r.stderr, "agora hub") {
			t.Errorf("%s on darwin: %+v", cmd, r)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".config")); !os.IsNotExist(err) {
		t.Fatalf("wrote files: %v", err)
	}
}

func TestInstallSkill(t *testing.T) {
	home := isolate(t)
	def := filepath.Join(home, ".claude", "skills", "agora", "SKILL.md")
	if r := run("install", "skill"); r.code != 0 || !strings.Contains(r.stdout, def) {
		t.Fatalf("install: %+v", r)
	}
	if b, _ := os.ReadFile(def); string(b) != install.Skill {
		t.Fatal("default skill not written")
	}
	if r := run("uninstall", "skill"); r.code != 0 {
		t.Fatalf("uninstall: %+v", r)
	}
	a, b := t.TempDir(), t.TempDir()
	if r := run("install", "skill", "--dir", a, "--dir", b); r.code != 0 {
		t.Fatalf("install --dir: %+v", r)
	}
	for _, d := range []string{a, b} {
		if _, err := os.Stat(filepath.Join(d, "agora", "SKILL.md")); err != nil {
			t.Error(err)
		}
	}
	if _, err := os.Stat(def); !os.IsNotExist(err) {
		t.Fatalf("default directory touched with --dir: %v", err)
	}
}

// guideCommands finds every agora command line the guide shows, in code blocks and inline code.
func guideCommands(guide string) [][]string {
	var spans []string
	inBlock := false
	for _, line := range strings.Split(guide, "\n") {
		if strings.HasPrefix(line, "```") {
			inBlock = !inBlock
			continue
		}
		if inBlock {
			spans = append(spans, line)
			continue
		}
		for _, m := range regexp.MustCompile("`([^`]+)`").FindAllStringSubmatch(line, -1) {
			spans = append(spans, m[1])
		}
	}
	var out [][]string
	for _, s := range spans {
		words := shellWords(s)
		if len(words) < 2 || words[0] != "agora" {
			continue
		}
		out = append(out, words[1:])
	}
	return out
}

// shellWords splits a command line like a shell would, stopping at a comment or redirection.
func shellWords(s string) []string {
	var words []string
	var cur strings.Builder
	quote, started := byte(0), false
	flush := func() {
		if started {
			words = append(words, cur.String())
		}
		cur.Reset()
		started = false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			} else {
				cur.WriteByte(c)
			}
		case c == '"' || c == '\'':
			quote, started = c, true
		case c == ' ' || c == '\t':
			flush()
		case !started && (c == '#' && (i+1 == len(s) || s[i+1] == ' ') || c == '<' && (i+1 == len(s) || s[i+1] == ' ') || c == '|'):
			flush()
			return words
		default:
			cur.WriteByte(c)
			started = true
		}
	}
	flush()
	return words
}

func TestGuideMatchesCommands(t *testing.T) {
	cmds := guideCommands(install.Skill)
	if len(cmds) < 30 {
		t.Fatalf("found only %d commands in the guide", len(cmds))
	}
	for _, args := range cmds {
		root := cli.NewRoot()
		cmd, _, err := root.Find(args)
		if err != nil || cmd == root || !cmd.Runnable() {
			t.Errorf("agora %s: no such command (%v)", strings.Join(args, " "), err)
			continue
		}
		for _, a := range args {
			if !strings.HasPrefix(a, "--") {
				continue
			}
			name, _, _ := strings.Cut(strings.TrimPrefix(a, "--"), "=")
			if cmd.Flags().Lookup(name) == nil && cmd.InheritedFlags().Lookup(name) == nil {
				t.Errorf("agora %s: %s has no flag --%s", strings.Join(args, " "), cmd.CommandPath(), name)
			}
		}
	}
}
