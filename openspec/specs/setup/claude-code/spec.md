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

Installing and uninstalling SHALL keep every other setting, hook and value of the file, including fields Agora does not know, in their order; only Agora's own hook entries change. A file that is not a JSON object, or whose `hooks` is not an object, SHALL be refused and left untouched.

#### Scenario: Foreign hooks and unknown fields
- **WHEN** the settings hold other keys, another tool's `Stop` hook and fields Agora does not know, and Agora's hooks are installed
- **THEN** all of them are still there with the same values, the other tool's `Stop` hook still comes first, and Agora's hooks are added

#### Scenario: Invalid file
- **WHEN** the settings file is not valid JSON
- **THEN** the install fails with an error naming the file and the file is unchanged

### Requirement: Outdated Entries Are Replaced

The system SHALL recognise Agora's hook entries, under any event, by a command that runs `hook claude-code` or `hook claude-code-wait` with a binary named `agora` or with the binary that runs the command (by any path, quoted or not, with or without leading environment assignments), SHALL remove them, and SHALL add the current entries once; hook entries that share a group with Agora's stay in that group.

#### Scenario: Entry from a manual setup
- **WHEN** the settings hold `agora hook claude-code` under `Stop` from an earlier manual setup
- **THEN** after the install `Stop` holds Agora's current two hooks with the absolute binary path and no other Agora entry

### Requirement: Mapping A Terminal Handle

With `--terminal-env <VAR>`, the connector hook command SHALL be `AGORA_TERMINAL="$VAR" <agora> hook claude-code`, so the session's terminal reported to the hub (and given to the hub's wake command) comes from whatever variable the user's terminal system sets. `VAR` SHALL be an environment variable name (letters, digits and `_`, not starting with a digit); any other value is refused before anything is written.

#### Scenario: Terminal variable
- **WHEN** `agora install claude-code --terminal-env TERM_HANDLE` runs
- **THEN** every `hook claude-code` command is `AGORA_TERMINAL="$TERM_HANDLE" <agora> hook claude-code`, and installing again without `--terminal-env` replaces them with the plain command

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
