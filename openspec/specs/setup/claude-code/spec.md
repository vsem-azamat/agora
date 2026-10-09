# Setup: Claude Code

[Specs](../../README.md) / [Setup](../README.md) / **Claude Code**

## Purpose

`agora install claude-code` wires the [Claude Code connector](../../connectors/claude-code/spec.md) and its [wake hook](../../delivery/wakeups/spec.md) into Claude Code's user settings, and `agora uninstall claude-code` takes them out again, without touching anything else in the file.

## Requirements

### Requirement: Installing The Claude Code Hooks

`agora install claude-code` SHALL add to Claude Code's user settings file (`--settings <path>`, else `$CLAUDE_CONFIG_DIR/settings.json` when `CLAUDE_CONFIG_DIR` is set, else `~/.claude/settings.json`) one command hook running `<agora> hook claude-code` on each of `SessionStart`, `UserPromptSubmit`, `PostToolUse` (with matcher `*`), `Stop` and `SessionEnd`, and one command hook running `<agora> hook claude-code-wait` on `Stop` that is asynchronous, wakes the agent (`asyncRewake`) and has a timeout of 86400 seconds. A missing file and its directory SHALL be created.

#### Scenario: Fresh settings
- **WHEN** `agora install claude-code` runs and the settings file does not exist
- **THEN** the file is created with exactly those hooks

#### Scenario: Already installed
- **WHEN** it runs again
- **THEN** it reports that the hooks are already installed and the file is unchanged

### Requirement: Other Settings Are Preserved

Installing and uninstalling SHALL keep every other setting, hook and value of the file, including fields Agora does not know, in their order; only Agora's own hook entries change, and the file's formatting is normalised to two-space indentation. A file that is not a JSON object, whose `hooks` is not an object, that has more than one `hooks` key or more than one list for an event, or that is read-only SHALL be refused and left untouched.

#### Scenario: Foreign hooks and unknown fields
- **WHEN** the settings hold other keys, another tool's `Stop` hook and fields Agora does not know, and Agora's hooks are installed
- **THEN** all of them are still there with the same values, the other tool's `Stop` hook still comes first, and Agora's hooks are added

#### Scenario: Invalid file
- **WHEN** the settings file is not valid JSON
- **THEN** the install fails with an error naming the file and the file is unchanged

#### Scenario: Duplicate hooks key
- **WHEN** the settings file has two `hooks` keys
- **THEN** the install fails and the file is unchanged

#### Scenario: Read-only file
- **WHEN** the settings file is read-only
- **THEN** the install fails saying so and the file is unchanged

### Requirement: Changes Made Meanwhile Are Kept

The system SHALL read the settings file again just before writing it and, when it changed since it was read, SHALL start over once from the new content; when it changed again, it SHALL fail with `settings changed while editing; run again` and write nothing.

#### Scenario: Written meanwhile
- **WHEN** another program changes the settings file while the install edits it
- **THEN** the result holds that change and Agora's hooks

#### Scenario: Keeps changing
- **WHEN** the settings file changes every time it is read
- **THEN** the install fails asking to run again

### Requirement: Outdated Entries Are Replaced

The system SHALL recognise Agora's hook entries, under any event, by a command that is exactly: any `NAME=value` environment assignments, then one command word that is a binary named `agora` (by any path, quoted or not, such as `$HOME/.local/bin/agora`) or the binary given to the command, then `hook claude-code` or `hook claude-code-wait`. It SHALL remove them and add the current entries once; hook entries that share a group with Agora's stay in that group. A command that mentions an Agora hook but has anything else (`;`, `&&`, `|`, redirections, substitutions or extra arguments) is not Agora's: it SHALL be left as it is, and the command SHALL warn that it found an Agora-like hook it did not write.

#### Scenario: Entry from a manual setup
- **WHEN** the settings hold `agora hook claude-code` under `Stop` from an earlier manual setup
- **THEN** after the install `Stop` holds Agora's current two hooks with the absolute binary path and no other Agora entry

#### Scenario: Path through a variable
- **WHEN** the settings hold `$HOME/.local/bin/agora hook claude-code`
- **THEN** it is recognised as Agora's and replaced

#### Scenario: Someone else's command
- **WHEN** the settings hold `notify-send done; agora hook claude-code`, `echo agora hook claude-code`, `agora hook claude-code 2>/dev/null` or `agora hook claude-code --debug`
- **THEN** those entries stay as they are and the output warns about each of them

### Requirement: Mapping A Terminal Handle

With `--terminal-env <VAR>`, the connector hook command SHALL be `AGORA_TERMINAL="${VAR:-$AGORA_TERMINAL}" <agora> hook claude-code`, so the session's terminal reported to the hub (and given to the hub's wake command) comes from whatever variable the user's terminal system sets, and falls back to `AGORA_TERMINAL` when that variable is empty. `VAR` SHALL be an environment variable name (letters, digits and `_`, not starting with a digit); any other value is refused before anything is written.

#### Scenario: Terminal variable
- **WHEN** `agora install claude-code --terminal-env TERM_HANDLE` runs
- **THEN** every `hook claude-code` command is `AGORA_TERMINAL="${TERM_HANDLE:-$AGORA_TERMINAL}" <agora> hook claude-code`, and installing again without `--terminal-env` replaces them with the plain command

#### Scenario: Invalid name
- **WHEN** `--terminal-env` is given `1BAD;rm`
- **THEN** the command fails and the settings file is unchanged

### Requirement: Settings Are Backed Up

Before changing an existing settings file, the system SHALL copy it next to itself as `<file>.agora-backup-<YYYYMMDD-HHMMSS>` and name the copy in its output; a run that changes nothing makes no copy.

#### Scenario: Backup
- **WHEN** `agora install claude-code` changes an existing settings file
- **THEN** a backup with the previous content is next to it and the output names it

### Requirement: Uninstalling The Claude Code Hooks

`agora uninstall claude-code [--settings <path>]` SHALL remove every Agora hook entry, SHALL remove hook groups, event lists and a `hooks` object that this leaves empty, SHALL keep everything else, and SHALL back up the file first.

#### Scenario: Uninstall restores
- **WHEN** Agora's hooks are installed into settings with other keys and hooks and then uninstalled
- **THEN** the settings hold the same keys, hooks and values as before the install
