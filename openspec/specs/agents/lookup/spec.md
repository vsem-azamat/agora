# Agents: Lookup

[Specs](../../README.md) / [Agents](../README.md) / **Lookup**

## Purpose

Finding out who works on what: the board overview, and finding the owner of a pull request, branch, directory or name.

## Requirements

### Requirement: Board Overview

The system SHALL show an overview of active agents with name, status, session state (`busy`, `idle` or `offline`), project, pull requests, time since the last update and task, followed by every resource that someone holds or waits for.

#### Scenario: Overview
- **WHEN** an agent asks for the board status while `builder` works and holds `example-app/merge`
- **THEN** it sees `builder` with those fields and `example-app/merge` with its holder

#### Scenario: Including inactive agents
- **WHEN** the overview is requested for all agents
- **THEN** inactive and left agents are listed too

### Requirement: Finding The Owner Of Work

The system SHALL find active agents by a pull request number (`123` or `#123`), by a directory (the agent works in it, or in a directory inside it), by an agent name, by an exact branch name, or by a part of a branch name longer than two characters, and SHALL show each match with its session state.

#### Scenario: By pull request
- **WHEN** an agent looks up `#57`
- **THEN** every active agent with pull request 57 is listed

#### Scenario: By directory
- **WHEN** an agent looks up `~/src/example-app` while `builder` works in `~/src/example-app/.worktrees/login-fix`
- **THEN** `builder` is listed

#### Scenario: By part of a branch
- **WHEN** an agent looks up `login` while `builder` is on `fix/login-timeout`
- **THEN** `builder` is listed

#### Scenario: No match
- **WHEN** nothing active matches
- **THEN** the lookup says so and suggests including inactive agents
