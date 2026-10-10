package install

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// WaitTimeout is the timeout, in seconds, of the asynchronous wake hook. Claude Code enforces
// it even for asynchronous hooks, so it bounds how long an idle agent can be woken.
const WaitTimeout = 86400

// ClaudeCodeSettings is Claude Code's user settings file: $CLAUDE_CONFIG_DIR/settings.json, else
// ~/.claude/settings.json.
func ClaudeCodeSettings() (string, error) {
	dir, err := claudeConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "settings.json"), nil
}

func claudeConfigDir() (string, error) {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude"), nil
}

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ValidEnvName reports whether s can name an environment variable in a shell.
func ValidEnvName(s string) bool { return envName.MatchString(s) }

type hookEntry struct {
	Type        string `json:"type"`
	Command     string `json:"command"`
	Async       bool   `json:"async,omitempty"`
	AsyncRewake bool   `json:"asyncRewake,omitempty"`
	Timeout     int    `json:"timeout,omitempty"`
}

type hookGroup struct {
	Matcher string      `json:"matcher,omitempty"`
	Hooks   []hookEntry `json:"hooks"`
}

type eventGroup struct {
	event string
	group hookGroup
}

// claudeCodeHooks are the hook groups Agora adds, in the order they are added.
func claudeCodeHooks(bin, terminalEnv string) []eventGroup {
	hook := shellQuote(bin) + " hook claude-code"
	if terminalEnv != "" {
		hook = `AGORA_TERMINAL="${` + terminalEnv + `:-$AGORA_TERMINAL}" ` + hook
	}
	plain := hookGroup{Hooks: []hookEntry{{Type: "command", Command: hook}}}
	return []eventGroup{
		{"SessionStart", plain},
		{"UserPromptSubmit", plain},
		{"PostToolUse", hookGroup{Matcher: "*", Hooks: plain.Hooks}},
		{"Stop", plain},
		{"Stop", hookGroup{Hooks: []hookEntry{{
			Type: "command", Command: shellQuote(bin) + " hook claude-code-wait",
			Async: true, AsyncRewake: true, Timeout: WaitTimeout,
		}}}},
		{"SessionEnd", plain},
	}
}

// InstallClaudeCode puts Agora's hooks into the Claude Code settings file at path: it removes
// every Agora hook entry, adds the current ones, and writes the file unless that changes
// nothing. Everything else in the file is kept, in its order.
func InstallClaudeCode(path, bin, terminalEnv string, now time.Time) (Change, error) {
	if terminalEnv != "" && !ValidEnvName(terminalEnv) {
		return Change{}, fmt.Errorf("--terminal-env %q: give the name of an environment variable, like TERM_HANDLE", terminalEnv)
	}
	return editSettings(path, now, func(hooks *object) ([]string, error) {
		foreign := removeAgoraHooks(hooks, bin)
		for _, eg := range claudeCodeHooks(bin, terminalEnv) {
			if err := appendGroup(hooks, eg.event, eg.group); err != nil {
				return nil, err
			}
		}
		return foreign, nil
	})
}

// UninstallClaudeCode removes every Agora hook entry from the settings file at path, including
// those that run bin.
func UninstallClaudeCode(path, bin string, now time.Time) (Change, error) {
	ch, err := editSettings(path, now, func(hooks *object) ([]string, error) {
		return removeAgoraHooks(hooks, bin), nil
	})
	switch ch.Outcome {
	case Unchanged:
		ch.Outcome = Absent
	case Updated:
		ch.Outcome = Removed
	}
	return ch, err
}

// beforeWrite, when set by tests, runs just before the file is checked for changes.
var beforeWrite func()

// errChanged reports that the settings file changed between reading and writing it.
var errChanged = errors.New("settings changed while editing; run again")

// editSettings reads the settings at path, lets edit change its hooks object, and writes the
// result, with a backup, when it differs from what is there. When the file changes while it
// is being edited, it starts over once.
func editSettings(path string, now time.Time, edit func(hooks *object) ([]string, error)) (Change, error) {
	target, err := resolve(path)
	if err != nil {
		return Change{}, err
	}
	ch, err := editOnce(path, target, now, edit)
	if errors.Is(err, errChanged) {
		ch, err = editOnce(path, target, now, edit)
	}
	return ch, err
}

// editOnce is one attempt of editSettings; every error it returns names path.
func editOnce(path, target string, now time.Time, edit func(hooks *object) ([]string, error)) (Change, error) {
	fail := func(err error) (Change, error) { return Change{}, fmt.Errorf("%s: %w", path, err) }
	old, mode, exists, err := readIfExists(target)
	if err != nil {
		return fail(err)
	}
	if exists && mode&0o200 == 0 {
		return fail(errors.New("the file is read-only; make it writable or edit it by hand"))
	}
	settings := object{}
	if exists && len(bytes.TrimSpace(old)) > 0 {
		if settings, err = parseObject(old); err != nil {
			return fail(err)
		}
	}
	if settings.count("hooks") > 1 {
		return fail(errors.New(`more than one "hooks" key; merge them first`))
	}
	hooks := object{}
	if raw, ok := settings.get("hooks"); ok {
		if hooks, err = parseObject(raw); err != nil {
			return fail(fmt.Errorf("hooks: %w", err))
		}
	}
	for _, m := range hooks {
		if hooks.count(m.key) > 1 {
			return fail(fmt.Errorf("hooks: more than one %q key; merge them first", m.key))
		}
	}
	before := len(hooks)
	foreign, err := edit(&hooks)
	if err != nil {
		return fail(err)
	}
	if len(hooks) == 0 && before > 0 {
		settings.del("hooks") // emptied by removing Agora's hooks
	} else if len(hooks) > 0 {
		settings.set("hooks", hooks.marshal())
	}
	out, err := indent(settings.marshal())
	if err != nil {
		return fail(err)
	}
	if exists && sameJSON(old, out) || !exists && len(hooks) == 0 {
		return Change{Outcome: Unchanged, Foreign: foreign}, nil
	}
	// Claude Code or another tool may have written the file meanwhile; never overwrite that.
	if beforeWrite != nil {
		beforeWrite()
	}
	cur, _, stillExists, err := readIfExists(target)
	if err != nil {
		return fail(err)
	}
	if stillExists != exists || !bytes.Equal(cur, old) {
		return fail(errChanged)
	}
	ch := Change{Outcome: Created, Foreign: foreign}
	if exists {
		ch.Outcome = Updated
		if ch.Backup, err = backup(target, old, mode, now); err != nil {
			return fail(err)
		}
	} else {
		mode = 0o600
	}
	if err := writeAtomic(target, out, mode); err != nil {
		return fail(err)
	}
	return ch, nil
}

// removeAgoraHooks takes Agora's hook entries out of every event, dropping groups and events
// that are left empty, and returns the commands that look like Agora's hooks but are not
// recognised as its own, which stay. Values it does not understand are kept as they are.
func removeAgoraHooks(hooks *object, bin string) (foreign []string) {
	for i := 0; i < len(*hooks); i++ {
		m := (*hooks)[i]
		var groups []json.RawMessage
		if err := json.Unmarshal(m.value, &groups); err != nil {
			continue // not a list of groups: not Agora's
		}
		var kept []json.RawMessage
		changed := false
		for _, g := range groups {
			group, err := parseObject(g)
			if err != nil {
				kept = append(kept, g)
				continue
			}
			raw, ok := group.get("hooks")
			var entries []json.RawMessage
			if !ok || json.Unmarshal(raw, &entries) != nil {
				kept = append(kept, g)
				continue
			}
			var keptEntries []json.RawMessage
			for _, e := range entries {
				var h struct {
					Command string `json:"command"`
				}
				if json.Unmarshal(e, &h) == nil && isAgoraHook(h.Command, bin) {
					continue
				}
				if looksLikeAgoraHook(h.Command) {
					foreign = append(foreign, h.Command)
				}
				keptEntries = append(keptEntries, e)
			}
			switch {
			case len(keptEntries) == len(entries):
				kept = append(kept, g)
			case len(keptEntries) == 0:
				changed = true
			default:
				changed = true
				group.set("hooks", marshalArray(keptEntries))
				kept = append(kept, group.marshal())
			}
		}
		if !changed {
			continue
		}
		if len(kept) == 0 {
			hooks.del(m.key)
			i--
			continue
		}
		(*hooks)[i].value = marshalArray(kept)
	}
	return foreign
}

// appendGroup adds group at the end of event's list, creating the list when needed.
func appendGroup(hooks *object, event string, group hookGroup) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(group); err != nil {
		return err
	}
	var groups []json.RawMessage
	if raw, ok := hooks.get(event); ok {
		if err := json.Unmarshal(raw, &groups); err != nil {
			return fmt.Errorf("hooks.%s is not a list", event)
		}
	}
	groups = append(groups, bytes.TrimSpace(buf.Bytes()))
	hooks.set(event, marshalArray(groups))
	return nil
}

// shellQuote quotes s for sh when it holds anything but plain path characters.
func shellQuote(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_/.,:+=@%-") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
