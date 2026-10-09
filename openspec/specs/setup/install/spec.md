# Setup: Install

[Specs](../../README.md) / [Setup](../README.md) / **Install**

## Purpose

`agora install <target>` adds one of Agora's integrations to the machine and `agora uninstall <target>` removes it. The targets are `claude-code`, `service` and `skill`. This spec holds the rules every target follows.

## Requirements

### Requirement: Listing What Can Be Installed

`agora install` and `agora uninstall` without a target SHALL list every target with what it installs, and change nothing.

#### Scenario: No target
- **WHEN** `agora install` runs without a target
- **THEN** it lists `claude-code`, `service` and `skill`, each with a short description, and exits successfully

### Requirement: Repeated Runs Change Nothing

Installing a target SHALL compare what it would write with what is already there; when they are the same it SHALL write nothing and say that the target is already installed, otherwise it SHALL write and say what it wrote and where. Uninstalling a target that is not installed SHALL write nothing and say so.

#### Scenario: Install twice
- **WHEN** a target is installed and then installed again with the same options
- **THEN** the second run reports that it is already installed and leaves every file as it was

#### Scenario: Uninstall twice
- **WHEN** a target is uninstalled and then uninstalled again
- **THEN** the second run reports that it is not installed and leaves every file as it was

### Requirement: Files Are Replaced Atomically

The system SHALL write every file it installs or changes through a temporary file in the same directory that is renamed over the target, keeping an existing file's permissions, and SHALL update the file a symbolic link points to rather than replace the link.

#### Scenario: Symlinked settings
- **WHEN** the settings file is a symbolic link to a file elsewhere
- **THEN** the linked file is updated and the link stays a link

### Requirement: Integrations Call The Installing Binary

Hooks and the service SHALL run the absolute path of the `agora` binary that installed them, so they work whatever `PATH` the agent tool or the service manager has.

#### Scenario: Absolute path
- **WHEN** `agora install claude-code` runs
- **THEN** every hook command it writes starts with the absolute path of that binary
