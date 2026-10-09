# Setup

[Docs](../README.md) / [Architecture](README.md) / **Setup**

How `agora install` and `agora uninstall` (`internal/install`, commands in `internal/cli/install.go`) implement [`openspec/specs/setup/`](../../openspec/specs/setup/README.md).

```sh
agora install                                   # lists the targets
agora install claude-code [--settings <file>] [--terminal-env <VAR>]
agora install service [--db <file>] [--wake-command <cmd>] [--now]
agora install skill [--dir <skills-dir>]...
agora uninstall claude-code|service|skill       # same options; --terminal-env, --db and --wake-command do not apply
```

Every target is idempotent: it computes what it would write, compares it with what is there, and writes only when they differ. Hooks and the unit call the absolute path of the running binary (`os.Executable`), so they keep working whatever `PATH` the agent tool or systemd has; after moving the binary, install again.

## Writing files

- Writes go to a temporary file in the target's directory (`.<name>.tmp-*`), which is synced and renamed over the target.
- A target that is a symbolic link is resolved first, so a settings file kept in a dotfiles repository stays linked.
- An existing file keeps its permissions; a new settings file gets `0600`, a new unit or skill `0644`.
- Before changing an existing settings file, a copy goes next to it as `<file>.agora-backup-<YYYYMMDD-HHMMSS>` (local time; `-2`, `-3`, … when one exists for that second). The unit and the skill are Agora's own files and get no backup.

## Claude Code settings

The file is `--settings`, else `$CLAUDE_CONFIG_DIR/settings.json`, else `~/.claude/settings.json`. It is read as an ordered list of top-level members with their raw values, and `hooks` the same way; only the event lists that hold Agora's entries are re-encoded. Other members keep their order and exact values (number literals, unknown fields), and the result is re-indented with two spaces. A run whose result is the same JSON value as the file writes nothing.

An entry is Agora's when its `command` ends in `hook claude-code` or `hook claude-code-wait` run by a binary named `agora` (any path, quoted or not, after any `VAR=value` assignments) or by the installing binary. Install removes all of them, then appends the current groups; uninstall only removes. Groups and event lists left empty are dropped, and so is `hooks` when the removal empties it.

What install writes, with `<agora>` the binary's path (single-quoted for the shell when it holds other characters than letters, digits and `_/.,:+=@%-`):

| Event | Group |
| --- | --- |
| `SessionStart`, `UserPromptSubmit`, `SessionEnd` | `{"hooks": [{"type": "command", "command": "<agora> hook claude-code"}]}` |
| `PostToolUse` | the same with `"matcher": "*"` |
| `Stop` | the same, then `{"hooks": [{"type": "command", "command": "<agora> hook claude-code-wait", "async": true, "asyncRewake": true, "timeout": 86400}]}` |

`--terminal-env VAR` (validated as `[A-Za-z_][A-Za-z0-9_]*`) prefixes the `hook claude-code` command with `AGORA_TERMINAL="$VAR" `, so the hook reports the value of whatever variable the user's terminal system sets as the session's terminal, which the hub's wake command receives (see [Wakeups](wakeups.md)). The wait hook does not use a terminal.

## Hub service

Linux only (checked with `runtime.GOOS`). The unit is `$XDG_CONFIG_HOME/systemd/user/agora-hub.service`, else `~/.config/systemd/user/agora-hub.service`:

```ini
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

Without `--now`, install prints `systemctl --user daemon-reload && systemctl --user enable --now agora-hub` (and `systemctl --user restart agora-hub` after a changed unit); with `--now` it runs them. Uninstall removes the unit and the `default.target.wants/agora-hub.service` link, then prints `systemctl --user stop agora-hub && systemctl --user daemon-reload`; with `--now` it runs `systemctl --user disable --now agora-hub` first and `daemon-reload` after.

## Agent skill

`internal/install/skill/SKILL.md` is embedded with `go:embed` and written as `<dir>/agora/SKILL.md` for each `--dir`, else into `$CLAUDE_CONFIG_DIR/skills` or `~/.claude/skills`. It follows the [Agent Skills](https://agentskills.io) format: YAML frontmatter with `name` and `description`, then the guide. A test resolves every `agora` command in its code blocks and inline code against the command tree and checks each flag, so the guide cannot name a command or flag the binary lacks. Uninstall removes `SKILL.md` and the `agora` directory when it is then empty.
