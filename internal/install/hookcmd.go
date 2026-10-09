package install

import (
	"path"
	"strings"
)

// shellWord is one word of a command line, with its quotes removed.
type shellWord struct {
	text   string
	quoted bool // some part of it was quoted
}

// splitCommand splits a simple command line into words, removing quotes. It reports ok false
// for anything but a simple command: unquoted operators, redirections, substitutions, escapes,
// comments, newlines or an unterminated quote.
func splitCommand(s string) (words []shellWord, ok bool) {
	var cur strings.Builder
	started, quoted := false, false
	var quote byte
	flush := func() {
		if started {
			words = append(words, shellWord{cur.String(), quoted})
		}
		cur.Reset()
		started, quoted = false, false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			switch {
			case c == quote:
				quote = 0
			case quote == '"' && (c == '`' || c == '\\' || c == '$' && i+1 < len(s) && s[i+1] == '('):
				return nil, false // substitutions and escapes inside double quotes
			default:
				cur.WriteByte(c)
			}
			continue
		}
		switch c {
		case ' ', '\t':
			flush()
		case '\'', '"':
			quote, started, quoted = c, true, true
		case ';', '&', '|', '<', '>', '(', ')', '`', '\\', '\n', '\r':
			return nil, false
		case '#':
			if !started {
				return nil, false // a comment
			}
			cur.WriteByte(c)
		default:
			if c == '$' && i+1 < len(s) && s[i+1] == '(' {
				return nil, false
			}
			cur.WriteByte(c)
			started = true
		}
	}
	if quote != 0 {
		return nil, false
	}
	flush()
	return words, true
}

// isAssignment reports whether a word is a NAME=value environment assignment.
func isAssignment(w shellWord) bool {
	name, _, found := strings.Cut(w.text, "=")
	return found && ValidEnvName(name)
}

// isAgoraHook reports whether a hook command is one of Agora's Claude Code hooks: any NAME=value
// assignments, then one command word that is a binary named agora (by any path, quoted or not,
// such as $HOME/.local/bin/agora) or bin, then `hook claude-code` or `hook claude-code-wait`,
// and nothing else.
func isAgoraHook(command, bin string) bool {
	words, ok := splitCommand(command)
	if !ok {
		return false
	}
	for len(words) > 0 && isAssignment(words[0]) {
		words = words[1:]
	}
	if len(words) != 3 || words[1].quoted || words[2].quoted || words[1].text != "hook" ||
		words[2].text != "claude-code" && words[2].text != "claude-code-wait" {
		return false
	}
	prog := words[0].text
	return path.Base(prog) == "agora" || bin != "" && prog == bin
}

// looksLikeAgoraHook reports whether a command mentions an Agora hook, so one that is not
// recognised as Agora's own can be pointed out.
func looksLikeAgoraHook(command string) bool {
	return strings.Contains(command, "agora") && strings.Contains(command, "hook") && strings.Contains(command, "claude-code")
}
