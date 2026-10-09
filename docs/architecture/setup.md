# Setup

[Docs](../README.md) / [Architecture](README.md) / **Setup**

How `agora install` and `agora uninstall` (`internal/install`, commands in `internal/cli/install.go`) implement [`openspec/specs/setup/`](../../openspec/specs/setup/README.md).

```sh
agora install                                   # lists the targets
agora install claude-code [--settings <file>] [--terminal-env <VAR>] [--bin <agora>]
agora install service [--db <file>] [--wake-command <cmd>] [--now] [--bin <agora>]
agora install skill [--dir <skills-dir>]...
agora uninstall claude-code|service|skill       # same options; --terminal-env, --db and --wake-command do not apply
```

Every target is idempotent: it computes what it would write, compares it with what is there, and writes only when they differ.

Hooks and the unit call an absolute path of the binary, so they keep working whatever `PATH` the agent tool or systemd has: `--bin` when given (an executable file), else `exec.LookPath("agora")` when it is the same file as `os.Executable()` (so a link that a package manager moves to each new version stays in use), else `os.Executable()`. Without `--bin`, a binary under `os.TempDir()` or in a `go-build` directory (`go run`, test binaries) is refused with a hint to pass `--bin`. After moving the binary, install again.

## Writing files

- Writes go to a temporary file in the target's directory (`.<name>.tmp-*`), which is synced and renamed over the target.
- A target that is a symbolic link is resolved first with `Lstat` and `Readlink` (relative targets against the link's directory, up to 40 links, dangling ones included), so a settings file kept in a dotfiles repository stays linked and a link to a file not created yet gets that file.
- An existing file keeps its permissions; a new settings file gets `0600`, a new unit or skill `0644`. Missing directories are created with `0700`.
- Before changing an existing settings file, a copy goes next to it as `<file>.agora-backup-<YYYYMMDD-HHMMSS>` (local time; `-2`, `-3`, … when one exists for that second). The unit and the skill are Agora's own files and get no backup.

## Claude Code settings

The file is `--settings`, else `$CLAUDE_CONFIG_DIR/settings.json`, else `~/.claude/settings.json`. It is read as an ordered list of top-level members with their raw values, and `hooks` the same way; only the event lists that hold Agora's entries are re-encoded. Other members keep their order and exact values (number literals, unknown fields). The whole file's formatting is normalised: it is re-indented with two spaces, so a file formatted otherwise shows whitespace changes against its backup. A run whose result is the same JSON value as the file writes nothing. Refused, untouched: invalid JSON, a non-object, trailing data, duplicate `hooks` keys or duplicate event keys inside `hooks`, and a read-only file.

Just before writing, the file is read again; if its bytes changed since the first read (Claude Code writes this file too), the edit starts over once from the new content, and a second change fails with `settings changed while editing; run again`. The backup is taken after that check.

An entry is Agora's when its `command` splits, like a simple shell command, into `NAME=value` assignments, one command word and exactly `hook claude-code` or `hook claude-code-wait`, where the command word (quotes removed, variables not expanded, so `$HOME/.local/bin/agora` counts) has the base name `agora` or equals the installing binary. Unquoted `;`, `&`, `|`, `<`, `>`, parentheses, backquotes, `$(`, backslashes, comments and extra words make it someone else's command; such a command that still mentions `agora`, `hook` and `claude-code` is left alone and reported as `warning: found an Agora-like hook it did not write`. Install removes Agora's entries, then appends the current groups; uninstall only removes. Groups and event lists left empty are dropped, and so is `hooks` when the removal empties it.

What install writes, with `<agora>` the binary's path (single-quoted for the shell when it holds other characters than letters, digits and `_/.,:+=@%-`):

| Event | Group |
| --- | --- |
| `SessionStart`, `UserPromptSubmit`, `SessionEnd` | `{"hooks": [{"type": "command", "command": "<agora> hook claude-code"}]}` |
| `PostToolUse` | the same with `"matcher": "*"` |
| `Stop` | the same, then `{"hooks": [{"type": "command", "command": "<agora> hook claude-code-wait", "async": true, "asyncRewake": true, "timeout": 86400}]}` |

`--terminal-env VAR` (validated as `[A-Za-z_][A-Za-z0-9_]*`) prefixes the `hook claude-code` command with `AGORA_TERMINAL="${VAR:-$AGORA_TERMINAL}" ` (an empty `VAR` keeps an `AGORA_TERMINAL` set otherwise), so the hook reports the value of whatever variable the user's terminal system sets as the session's terminal, which the hub's wake command receives (see [Wakeups](wakeups.md)). The wait hook does not use a terminal.

## Hub service

Linux only (checked with `runtime.GOOS`). The unit is `$XDG_CONFIG_HOME/systemd/user/agora-hub.service`, else `~/.config/systemd/user/agora-hub.service`:

```ini
# Written by agora install service; agora uninstall service removes it.
[Unit]
Description=Agora hub
Documentation=https://github.com/vsem-azamat/agora

[Service]
ExecStart=<agora> hub [--socket <path>] [--db <path>] [--wake-command <cmd>]
Restart=on-failure
RestartSec=2

[Install]
WantedBy=default.target
```

`--socket` is added when given or `$AGORA_SOCKET` is set, `--db` when given or `$AGORA_DB` is set, `--wake-command` when given or `$AGORA_WAKE_COMMAND` is set, because a systemd user service does not see the shell's environment. Paths are made absolute. Each argument that is not a plain word is double-quoted with `\\`, `\"`, `\n` and `\t` escapes, and `$` and `%` are doubled, so systemd passes it unchanged instead of expanding variables or specifiers.

The first line marks the unit as Agora's: install and uninstall refuse an `agora-hub.service` without it, naming the file.

Without `--now`, install prints `systemctl --user daemon-reload && systemctl --user enable --now agora-hub` (and `systemctl --user restart agora-hub` after a changed unit); with `--now` it first checks that the user manager answers `systemctl --user show-environment` (it does not, for example, over SSH without a user session or in a container without systemd) and then runs them. Uninstall removes the unit and the `default.target.wants/agora-hub.service` link (also a dangling one left after the unit was deleted by hand), then prints `systemctl --user stop agora-hub && systemctl --user daemon-reload`; with `--now` it runs `systemctl --user disable --now agora-hub` first and `daemon-reload` after.

## Agent skill

`internal/install/skill/SKILL.md` is embedded with `go:embed` and written as `<dir>/agora/SKILL.md` for each `--dir`, else into `$CLAUDE_CONFIG_DIR/skills` or `~/.claude/skills`. It follows the [Agent Skills](https://agentskills.io) format: YAML frontmatter with `name` and `description`, then the guide. A test resolves every `agora` command in its code blocks and inline code against the command tree and checks each flag, so the guide cannot name a command or flag the binary lacks. Uninstall removes `SKILL.md` and the `agora` directory when it is then empty.
