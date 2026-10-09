package install_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/vsem-azamat/agora/internal/install"
)

const bin = "/opt/agora/bin/agora"

var now = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("%s: %v\n%s", path, err, b)
	}
	return v
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// commands lists the hook commands of one event, in order, as "command" or "command async".
func commands(t *testing.T, settings map[string]any, event string) []string {
	t.Helper()
	hooks, _ := settings["hooks"].(map[string]any)
	groups, _ := hooks[event].([]any)
	var out []string
	for _, g := range groups {
		for _, h := range g.(map[string]any)["hooks"].([]any) {
			hm := h.(map[string]any)
			c := hm["command"].(string)
			if hm["async"] == true {
				c += " async"
			}
			out = append(out, c)
		}
	}
	return out
}

func TestInstallIntoMissingSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", "settings.json")
	ch, err := install.InstallClaudeCode(path, bin, "", now)
	if err != nil {
		t.Fatal(err)
	}
	if ch.Outcome != install.Created || ch.Backup != "" {
		t.Fatalf("change: %+v", ch)
	}
	s := readJSON(t, path)
	want := map[string][]string{
		"SessionStart":     {bin + " hook claude-code"},
		"UserPromptSubmit": {bin + " hook claude-code"},
		"PostToolUse":      {bin + " hook claude-code"},
		"Stop":             {bin + " hook claude-code", bin + " hook claude-code-wait async"},
		"SessionEnd":       {bin + " hook claude-code"},
	}
	if len(s["hooks"].(map[string]any)) != len(want) {
		t.Fatalf("events: %v", s["hooks"])
	}
	for ev, w := range want {
		if got := commands(t, s, ev); !reflect.DeepEqual(got, w) {
			t.Errorf("%s: %q, want %q", ev, got, w)
		}
	}
	post := s["hooks"].(map[string]any)["PostToolUse"].([]any)[0].(map[string]any)
	if post["matcher"] != "*" {
		t.Errorf("PostToolUse matcher: %v", post["matcher"])
	}
	wait := s["hooks"].(map[string]any)["Stop"].([]any)[1].(map[string]any)["hooks"].([]any)[0].(map[string]any)
	if wait["asyncRewake"] != true || wait["timeout"] != float64(86400) || wait["type"] != "command" {
		t.Errorf("wait hook: %v", wait)
	}
}

func TestInstallTwiceChangesNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	write(t, path, `{"model": "x"}`)
	if _, err := install.InstallClaudeCode(path, bin, "", now); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	entries, _ := os.ReadDir(filepath.Dir(path))
	ch, err := install.InstallClaudeCode(path, bin, "", now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if ch.Outcome != install.Unchanged || ch.Backup != "" {
		t.Fatalf("second install: %+v", ch)
	}
	after, _ := os.ReadFile(path)
	entries2, _ := os.ReadDir(filepath.Dir(path))
	if string(before) != string(after) || len(entries) != len(entries2) {
		t.Fatalf("second install changed files:\n%s\n%s", before, after)
	}
}

const foreign = `{
  "model": "opus",
  "permissions": {"allow": ["Bash(ls:*)"], "deny": []},
  "zeta": 1.50,
  "hooks": {
    "Stop": [
      {"hooks": [{"type": "command", "command": "notify-done", "timeout": 5}]}
    ],
    "PreToolUse": [
      {"matcher": "Bash", "hooks": [{"type": "command", "command": "guard", "custom": {"x": [1, 2]}}]}
    ]
  },
  "alpha": "last"
}
`

func TestInstallPreservesForeignSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	write(t, path, foreign)
	if _, err := install.InstallClaudeCode(path, bin, "", now); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	s := readJSON(t, path)
	for _, k := range []string{"model", "permissions", "zeta", "alpha"} {
		if _, ok := s[k]; !ok {
			t.Errorf("lost %s", k)
		}
	}
	if !strings.Contains(string(b), "1.50") {
		t.Errorf("number literal changed:\n%s", b)
	}
	if got := commands(t, s, "Stop"); !reflect.DeepEqual(got, []string{"notify-done", bin + " hook claude-code", bin + " hook claude-code-wait async"}) {
		t.Errorf("Stop: %q", got)
	}
	pre := s["hooks"].(map[string]any)["PreToolUse"].([]any)[0].(map[string]any)
	if pre["matcher"] != "Bash" || pre["hooks"].([]any)[0].(map[string]any)["custom"] == nil {
		t.Errorf("PreToolUse changed: %v", pre)
	}
	// keys keep their order
	text := string(b)
	order := []string{`"model"`, `"permissions"`, `"zeta"`, `"hooks"`, `"alpha"`}
	last := -1
	for _, k := range order {
		i := strings.Index(text, k)
		if i < last {
			t.Fatalf("key order changed:\n%s", text)
		}
		last = i
	}
}

func TestInstallBacksUpTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	write(t, path, foreign)
	ch, err := install.InstallClaudeCode(path, bin, "", now)
	if err != nil {
		t.Fatal(err)
	}
	want := path + ".agora-backup-" + now.Local().Format("20060102-150405")
	if ch.Outcome != install.Updated || ch.Backup != want {
		t.Fatalf("change: %+v, want backup %s", ch, want)
	}
	if b, err := os.ReadFile(want); err != nil || string(b) != foreign {
		t.Fatalf("backup: %v %q", err, b)
	}
}

func TestInstallReplacesOutdatedEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	write(t, path, `{"hooks": {
	  "Stop": [
	    {"hooks": [{"type": "command", "command": "agora hook claude-code"}, {"type": "command", "command": "notify-done"}]},
	    {"hooks": [{"type": "command", "command": "AGORA_TERMINAL=\"$X\" '/old place/agora' hook claude-code-wait", "async": true}]}
	  ],
	  "Notification": [{"hooks": [{"type": "command", "command": "/usr/local/bin/agora hook claude-code"}]}]
	}}`)
	if _, err := install.InstallClaudeCode(path, bin, "", now); err != nil {
		t.Fatal(err)
	}
	s := readJSON(t, path)
	if got := commands(t, s, "Stop"); !reflect.DeepEqual(got, []string{"notify-done", bin + " hook claude-code", bin + " hook claude-code-wait async"}) {
		t.Errorf("Stop: %q", got)
	}
	if _, ok := s["hooks"].(map[string]any)["Notification"]; ok {
		t.Errorf("outdated Notification entry kept: %v", s["hooks"])
	}
}

func TestTerminalEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if _, err := install.InstallClaudeCode(path, bin, "TERM_HANDLE", now); err != nil {
		t.Fatal(err)
	}
	s := readJSON(t, path)
	hooked := `AGORA_TERMINAL="${TERM_HANDLE:-$AGORA_TERMINAL}" ` + bin + " hook claude-code"
	if got := commands(t, s, "Stop"); !reflect.DeepEqual(got, []string{hooked, bin + " hook claude-code-wait async"}) {
		t.Errorf("Stop: %q", got)
	}
	if got := commands(t, s, "SessionStart"); !reflect.DeepEqual(got, []string{hooked}) {
		t.Errorf("SessionStart: %q", got)
	}
	ch, err := install.InstallClaudeCode(path, bin, "", now)
	if err != nil || ch.Outcome != install.Updated {
		t.Fatalf("reinstall without terminal env: %+v %v", ch, err)
	}
	if got := commands(t, readJSON(t, path), "SessionStart"); !reflect.DeepEqual(got, []string{bin + " hook claude-code"}) {
		t.Errorf("SessionStart after reinstall: %q", got)
	}
}

func TestInvalidTerminalEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	write(t, path, `{}`)
	for _, v := range []string{"1BAD;rm", "A-B", "$X", "A B"} {
		if _, err := install.InstallClaudeCode(path, bin, v, now); err == nil {
			t.Errorf("%q accepted", v)
		}
	}
	if b, _ := os.ReadFile(path); string(b) != `{}` {
		t.Fatalf("file changed: %s", b)
	}
}

func TestBinaryPathIsQuotedForTheShell(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	odd := "/opt/my tools/agora"
	if _, err := install.InstallClaudeCode(path, odd, "", now); err != nil {
		t.Fatal(err)
	}
	if got := commands(t, readJSON(t, path), "SessionEnd"); !reflect.DeepEqual(got, []string{"'/opt/my tools/agora' hook claude-code"}) {
		t.Errorf("SessionEnd: %q", got)
	}
	ch, err := install.InstallClaudeCode(path, odd, "", now)
	if err != nil || ch.Outcome != install.Unchanged {
		t.Fatalf("quoted path not recognised as installed: %+v %v", ch, err)
	}
}

func TestInvalidSettingsAreRefused(t *testing.T) {
	for _, content := range []string{`{"hooks": `, `[1, 2]`, `{"hooks": []}`, `{} {}`} {
		path := filepath.Join(t.TempDir(), "settings.json")
		write(t, path, content)
		_, err := install.InstallClaudeCode(path, bin, "", now)
		if err == nil || !strings.Contains(err.Error(), path) {
			t.Errorf("%q: error %v", content, err)
		}
		if b, _ := os.ReadFile(path); string(b) != content {
			t.Errorf("%q: file changed to %s", content, b)
		}
		if _, err := install.UninstallClaudeCode(path, bin, now); err == nil {
			t.Errorf("%q: uninstall accepted it", content)
		}
	}
}

func TestUninstallRestores(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	write(t, path, foreign)
	if _, err := install.InstallClaudeCode(path, bin, "X", now); err != nil {
		t.Fatal(err)
	}
	ch, err := install.UninstallClaudeCode(path, bin, now.Add(time.Second))
	if err != nil || ch.Outcome != install.Removed || ch.Backup == "" {
		t.Fatalf("uninstall: %+v %v", ch, err)
	}
	var want map[string]any
	if err := json.Unmarshal([]byte(foreign), &want); err != nil {
		t.Fatal(err)
	}
	if got := readJSON(t, path); !reflect.DeepEqual(got, want) {
		t.Fatalf("after uninstall:\n%v\nwant\n%v", got, want)
	}
	before, _ := os.ReadFile(path)
	ch, err = install.UninstallClaudeCode(path, bin, now.Add(2*time.Second))
	if err != nil || ch.Outcome != install.Absent || ch.Backup != "" {
		t.Fatalf("second uninstall: %+v %v", ch, err)
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Fatal("second uninstall changed the file")
	}
}

func TestUninstallRemovesEmptyHooks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	write(t, path, `{"model": "opus"}`)
	if _, err := install.InstallClaudeCode(path, bin, "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := install.UninstallClaudeCode(path, bin, now); err != nil {
		t.Fatal(err)
	}
	if got := readJSON(t, path); !reflect.DeepEqual(got, map[string]any{"model": "opus"}) {
		t.Fatalf("after uninstall: %v", got)
	}
}

func TestSymlinkedSettingsStayALink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "dotfiles", "settings.json")
	write(t, real, `{"model": "opus"}`)
	if err := os.Chmod(real, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if _, err := install.InstallClaudeCode(link, bin, "", now); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("link replaced: %v", err)
	}
	if got := commands(t, readJSON(t, real), "SessionEnd"); len(got) != 1 {
		t.Fatalf("linked file not updated: %q", got)
	}
	if fi, _ := os.Stat(real); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode changed to %v", fi.Mode().Perm())
	}
}

func TestOnlyPlainAgoraCommandsAreRecognised(t *testing.T) {
	foreignCmds := []string{
		"notify-send done; agora hook claude-code",
		"echo agora hook claude-code",
		"agora hook claude-code 2>/dev/null",
		"agora hook claude-code --debug",
		"agora hook claude-code && notify-send x",
		"agora hook claude-code | tee log",
		"$(agora) hook claude-code",
	}
	ours := []string{
		"agora hook claude-code",
		"$HOME/.local/bin/agora hook claude-code",
		`AGORA_TERMINAL="$X" '/old place/agora' hook claude-code-wait`,
		`A=1 B="x y" /usr/local/bin/agora   hook claude-code`,
		"/opt/agora/bin/agora-renamed hook claude-code", // the installing binary, whatever its name
	}
	var groups []string
	for _, c := range append(append([]string{}, foreignCmds...), ours...) {
		b, _ := json.Marshal(c)
		groups = append(groups, `{"hooks": [{"type": "command", "command": `+string(b)+`}]}`)
	}
	path := filepath.Join(t.TempDir(), "settings.json")
	write(t, path, `{"hooks": {"Notification": [`+strings.Join(groups, ",")+`]}}`)
	ch, err := install.UninstallClaudeCode(path, "/opt/agora/bin/agora-renamed", now)
	if err != nil {
		t.Fatal(err)
	}
	if got := commands(t, readJSON(t, path), "Notification"); !reflect.DeepEqual(got, foreignCmds) {
		t.Errorf("kept %q, want %q", got, foreignCmds)
	}
	if !reflect.DeepEqual(ch.Foreign, foreignCmds) {
		t.Errorf("warned about %q, want %q", ch.Foreign, foreignCmds)
	}
}

func TestDanglingSymlinkIsFollowed(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../dotfiles/claude/settings.json", link); err != nil {
		t.Fatal(err)
	}
	if _, err := install.InstallClaudeCode(link, bin, "", now); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("link replaced: %v", err)
	}
	if got := commands(t, readJSON(t, filepath.Join(dir, "dotfiles", "claude", "settings.json")), "SessionEnd"); len(got) != 1 {
		t.Fatalf("target not written: %q", got)
	}
}

func TestConcurrentChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	write(t, path, `{"model": "opus"}`)
	n := 0
	defer install.BeforeWrite(func() {
		n++
		if n == 1 {
			write(t, path, `{"model": "opus", "theme": "dark"}`)
		}
	})()
	if _, err := install.InstallClaudeCode(path, bin, "", now); err != nil {
		t.Fatal(err)
	}
	s := readJSON(t, path)
	if s["theme"] != "dark" || len(commands(t, s, "Stop")) != 2 {
		t.Fatalf("change made meanwhile lost or hooks missing: %v", s)
	}

	write(t, path, `{"model": "opus"}`)
	defer install.BeforeWrite(func() { write(t, path, fmt.Sprintf(`{"n": %d}`, time.Now().UnixNano())) })()
	if _, err := install.InstallClaudeCode(path, bin, "", now); err == nil || !strings.Contains(err.Error(), "changed while editing") {
		t.Fatalf("error: %v", err)
	}
}

func TestReadOnlyAndDuplicateKeysAreRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	write(t, path, `{"model": "opus"}`)
	if err := os.Chmod(path, 0o444); err != nil {
		t.Fatal(err)
	}
	if _, err := install.InstallClaudeCode(path, bin, "", now); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Errorf("read-only file: %v", err)
	}
	for _, content := range []string{`{"hooks": {}, "hooks": {}}`, `{"hooks": {"Stop": [], "Stop": []}}`} {
		path := filepath.Join(t.TempDir(), "settings.json")
		write(t, path, content)
		if _, err := install.InstallClaudeCode(path, bin, "", now); err == nil || !strings.Contains(err.Error(), "more than one") {
			t.Errorf("%s: %v", content, err)
		}
	}
}
