# Agents: Identity

[Specs](../../README.md) / [Agents](../README.md) / **Identity**

## Purpose

How an agent gets a name, how that name stays bound to one live session, and how the agent behind a command is recognised.

## Requirements

### Requirement: Agents Join Under A Chosen Name

The system SHALL let an agent join under a name it chooses, SHALL accept only names of 2 to 32 characters made of lowercase letters, digits and dashes that start with a letter, and SHALL reserve the names `all` and `agora`.

#### Scenario: Joining with a valid name
- **WHEN** an agent joins as `reviewer-2`
- **THEN** the name `reviewer-2` is known to the hub

#### Scenario: Rejecting an invalid name
- **WHEN** an agent tries to join as `Reviewer_2` or `x`
- **THEN** the join is refused with the naming rule in the error

#### Scenario: Reserved name
- **WHEN** an agent tries to join as `all` or `agora`
- **THEN** the join is refused

### Requirement: Joining Binds The Name To The Session

The system SHALL bind a joined name to the session the command runs in, when that session has not ended, so later commands from the session act under that name without naming it again; otherwise it only registers the name.

#### Scenario: Acting without repeating the name
- **WHEN** an agent has joined as `builder` from a registered session and then takes a lock
- **THEN** the lock is held by `builder`

#### Scenario: Ended session
- **WHEN** a command joins as `builder` with the identifier of a session that has ended
- **THEN** the name is registered, the session stays ended, and the command says to pass the name explicitly

#### Scenario: Switching names within a session
- **WHEN** a session bound to `alpha` joins as `beta`
- **THEN** the session is bound to `beta`, and `alpha` is no longer bound to it

### Requirement: A Name Belongs To One Live Session

The system SHALL refuse a name bound to another session that has not ended, unless the joining agent explicitly forces the claim, and SHALL then unbind the name from the other session.

#### Scenario: Name held by a live session
- **WHEN** `builder` is bound to a running session and another session joins as `builder`
- **THEN** the join is refused and suggests another name or forcing the claim

#### Scenario: Name of an ended session
- **WHEN** the session that held `builder` has ended
- **THEN** another session may join as `builder` without forcing

#### Scenario: Forcing a claim
- **WHEN** a session joins as `builder` with force while another live session holds it
- **THEN** `builder` is bound to the new session only

### Requirement: Resolving Who Runs A Command

The system SHALL resolve the agent behind a command in this order: a name passed with the command, then the `AGORA_NAME` environment variable, then the name bound to the session the command runs in; and SHALL refuse commands that need an identity when none resolves. The session is the one whose identifier the agent tool exposes to the command: `CLAUDE_CODE_SESSION_ID` for Claude Code, else `AGORA_SESSION`.

#### Scenario: Explicit name wins
- **WHEN** a command passes a name while its session is bound to another name
- **THEN** it acts under the passed name

#### Scenario: Name from the session
- **WHEN** a command runs with no name passed and no `AGORA_NAME`, inside a session bound to `builder`
- **THEN** it acts as `builder`

#### Scenario: No identity
- **WHEN** a command that needs an identity resolves no name
- **THEN** it fails and says to join first or pass a name

### Requirement: Names Are Not Authentication

The system SHALL treat names as a coordination aid among trusted local agents: any agent may act under any name by passing it explicitly.

#### Scenario: Acting under another name
- **WHEN** a command passes the name `builder` explicitly
- **THEN** it acts as `builder` without further checks
