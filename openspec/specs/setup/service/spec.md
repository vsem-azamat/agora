# Setup: Service

[Specs](../../README.md) / [Setup](../README.md) / **Service**

## Purpose

`agora install service` makes the hub a systemd user service on Linux, so it runs whenever the user is logged in and restarts after a crash. Other systems run `agora hub` under their own service manager.

## Requirements

### Requirement: Installing The Hub As A User Service

`agora install service` SHALL write the systemd user unit `agora-hub.service` into `$XDG_CONFIG_HOME/systemd/user`, else `~/.config/systemd/user`. The unit SHALL run `<agora> hub`, adding `--socket` when `--socket` is given or `AGORA_SOCKET` is set, `--db` when `--db` is given or `AGORA_DB` is set, and `--wake-command` when `--wake-command` is given or `AGORA_WAKE_COMMAND` is set, each with the value the install command sees; it SHALL restart the hub when it fails and start it with the user's service manager. The unit's first line SHALL be `# Written by agora install service; agora uninstall service removes it.`; an `agora-hub.service` without that line is not Agora's and SHALL never be overwritten or removed.

#### Scenario: Plain unit
- **WHEN** `agora install service` runs without options and without those variables
- **THEN** the unit runs `<agora> hub` with no flags, with `Restart=on-failure` and `WantedBy=default.target`

#### Scenario: Someone else's unit
- **WHEN** `agora-hub.service` exists without Agora's first line
- **THEN** installing and uninstalling fail, naming the file, and leave it unchanged

#### Scenario: Options
- **WHEN** `agora install service --db /srv/agora/agora.db --wake-command 'notify "$AGORA_TERMINAL"'` runs
- **THEN** the unit runs the hub with that database and that wake command

### Requirement: Arguments Reach The Hub Unchanged

The system SHALL quote every argument of the unit's command so that systemd passes it to the hub exactly as given, including spaces, quotes, backslashes, `$` (which systemd would otherwise expand) and `%` (which systemd would otherwise treat as a specifier).

#### Scenario: Wake command with variables
- **WHEN** the wake command is `notify -t "$AGORA_TERMINAL" 100% a\b`
- **THEN** the unit holds it as one double-quoted argument with `$$`, `%%`, `\"` and `\\` in place of `$`, `%`, `"` and `\`

### Requirement: Starting The Service

Without `--now`, installing SHALL print the commands that load and start the service, also when it is already installed: `systemctl --user daemon-reload && systemctl --user enable --now agora-hub`, followed by `systemctl --user restart agora-hub` when an existing unit changed. With `--now`, it SHALL first check that the user's systemd manager answers (`systemctl --user show-environment`) and fail with a clear message otherwise, then run those commands itself and fail with their output when one fails.

#### Scenario: Printed commands
- **WHEN** `agora install service` writes a new unit
- **THEN** the output holds `systemctl --user daemon-reload && systemctl --user enable --now agora-hub` and nothing is run

#### Scenario: Started now
- **WHEN** `agora install service --now` writes a new unit
- **THEN** `systemctl --user daemon-reload` and `systemctl --user enable --now agora-hub` run in that order

#### Scenario: No user manager
- **WHEN** `agora install service --now` runs where the user's systemd manager cannot be reached
- **THEN** it fails saying the user manager is not reachable, after writing the unit

### Requirement: The Service Needs Linux

On systems other than Linux, `agora install service` and `agora uninstall service` SHALL refuse with a message saying to run `agora hub` under the system's own service manager, and write nothing.

#### Scenario: Other system
- **WHEN** `agora install service` runs on macOS
- **THEN** it fails with that message and writes no file

### Requirement: Uninstalling The Service

`agora uninstall service` SHALL remove the unit and the link that enabling it created in `default.target.wants` (also when that link dangles), and print `systemctl --user stop agora-hub && systemctl --user daemon-reload`; with `--now` it SHALL instead run `systemctl --user disable --now agora-hub`, remove the unit, and run `systemctl --user daemon-reload`.

#### Scenario: Uninstall
- **WHEN** the service is installed and enabled and `agora uninstall service` runs
- **THEN** the unit and its link are gone and the output holds the commands to stop the hub and reload systemd
