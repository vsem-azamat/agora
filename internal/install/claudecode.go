package install

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
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

// agoraCommand matches the commands of Agora's own hooks, whatever the binary's path, its
// quoting and leading environment assignments.
var agoraCommand = regexp.MustCompile(`(^|[\s/'"])agora['"]?\s+hook\s+claude-code(-wait)?\s*$`)

// isAgoraHook reports whether a hook command is one of Agora's Claude Code hooks: a binary
// named agora, or bin (the binary installing them, whatever its name), running a hook.
func isAgoraHook(command, bin string) bool {
	if agoraCommand.MatchString(command) {
		return true
	}
	c := strings.TrimSpace(command)
	return strings.HasSuffix(c, shellQuote(bin)+" hook claude-code") || strings.HasSuffix(c, shellQuote(bin)+" hook claude-code-wait")
}

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
		hook = `AGORA_TERMINAL="$` + terminalEnv + `" ` + hook
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
	return editSettings(path, now, func(hooks *object) error {
		removeAgoraHooks(hooks, bin)
		for _, eg := range claudeCodeHooks(bin, terminalEnv) {
			if err := appendGroup(hooks, eg.event, eg.group); err != nil {
				return err
			}
		}
		return nil
	})
}

// UninstallClaudeCode removes every Agora hook entry from the settings file at path, including
// those that run bin.
func UninstallClaudeCode(path, bin string, now time.Time) (Change, error) {
	ch, err := editSettings(path, now, func(hooks *object) error {
		removeAgoraHooks(hooks, bin)
		return nil
	})
	switch ch.Outcome {
	case Unchanged:
		ch.Outcome = Absent
	case Updated:
		ch.Outcome = Removed
	}
	return ch, err
}

// editSettings reads the settings at path, lets edit change its hooks object, and writes the
// result, with a backup, when it differs from what is there.
func editSettings(path string, now time.Time, edit func(hooks *object) error) (Change, error) {
	real, err := resolve(path)
	if err != nil {
		return Change{}, err
	}
	old, mode, exists, err := readIfExists(real)
	if err != nil {
		return Change{}, err
	}
	fail := func(err error) (Change, error) { return Change{}, fmt.Errorf("%s: %w", path, err) }
	settings := object{}
	if exists && len(bytes.TrimSpace(old)) > 0 {
		if settings, err = parseObject(old); err != nil {
			return fail(err)
		}
	}
	hooks := object{}
	if raw, ok := settings.get("hooks"); ok {
		if hooks, err = parseObject(raw); err != nil {
			return fail(fmt.Errorf("hooks: %w", err))
		}
	}
	before := len(hooks)
	if err := edit(&hooks); err != nil {
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
		return Change{Outcome: Unchanged}, nil
	}
	ch := Change{Outcome: Created}
	if exists {
		ch.Outcome = Updated
		if ch.Backup, err = backup(real, old, mode, now); err != nil {
			return Change{}, err
		}
	} else {
		mode = 0o600
	}
	if err := writeAtomic(real, out, mode); err != nil {
		return Change{}, err
	}
	return ch, nil
}

// removeAgoraHooks takes Agora's hook entries out of every event, dropping groups and events
// that are left empty. Values it does not understand are kept as they are.
func removeAgoraHooks(hooks *object, bin string) {
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

// --- JSON objects that keep their key order ---------------------------------------------

type member struct {
	key   string
	value json.RawMessage
}

// object is a JSON object whose members keep their order and their exact values.
type object []member

var errNotObject = errors.New("not a JSON object")

func parseObject(raw []byte) (object, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	t, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := t.(json.Delim); !ok || d != '{' {
		return nil, errNotObject
	}
	obj := object{}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return nil, err
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		obj = append(obj, member{key: t.(string), value: v})
	}
	if _, err := dec.Token(); err != nil { // the closing brace
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("unexpected data after the JSON object")
	}
	return obj, nil
}

func (o object) get(key string) (json.RawMessage, bool) {
	for _, m := range o {
		if m.key == key {
			return m.value, true
		}
	}
	return nil, false
}

func (o *object) set(key string, value json.RawMessage) {
	for i, m := range *o {
		if m.key == key {
			(*o)[i].value = value
			return
		}
	}
	*o = append(*o, member{key, value})
}

func (o *object) del(key string) {
	out := (*o)[:0]
	for _, m := range *o {
		if m.key != key {
			out = append(out, m)
		}
	}
	*o = out
}

func (o object) marshal() json.RawMessage {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, m := range o {
		if i > 0 {
			b.WriteByte(',')
		}
		k, _ := json.Marshal(m.key)
		b.Write(k)
		b.WriteByte(':')
		b.Write(m.value)
	}
	b.WriteByte('}')
	return b.Bytes()
}

func marshalArray(items []json.RawMessage) json.RawMessage {
	var b bytes.Buffer
	b.WriteByte('[')
	for i, it := range items {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(it)
	}
	b.WriteByte(']')
	return b.Bytes()
}

// indent formats JSON with two-space indentation, as Claude Code writes its settings.
func indent(raw []byte) ([]byte, error) {
	var compact, out bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return nil, err
	}
	if err := json.Indent(&out, compact.Bytes(), "", "  "); err != nil {
		return nil, err
	}
	out.WriteByte('\n')
	return out.Bytes(), nil
}

// sameJSON reports whether a and b hold the same JSON value, ignoring formatting.
func sameJSON(a, b []byte) bool {
	var x, y any
	da, db := json.NewDecoder(bytes.NewReader(a)), json.NewDecoder(bytes.NewReader(b))
	da.UseNumber()
	db.UseNumber()
	if da.Decode(&x) != nil || db.Decode(&y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}
