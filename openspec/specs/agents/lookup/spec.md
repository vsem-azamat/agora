# Agents: Lookup

[Specs](../../README.md) / [Agents](../README.md) / **Lookup**

## Purpose

Finding out who works on what: the board overview and the lookup of an owner by pull request, branch, directory or name.

## Requirements

### Requirement: Board Overview

The system SHALL show an overview of active agents with status, session state, project, pull requests, time since last update and task, followed by rooms, held locks and open proposals.

#### Scenario: Overview
- **WHEN** an agent asks for the board status
- **THEN** it sees every active agent with those fields, every room with its message count and last activity, every unexpired lock with owner and expiry, and every open proposal

#### Scenario: Including inactive agents
- **WHEN** the overview is requested for all agents
- **THEN** stale and left agents are listed too

### Requirement: Finding The Owner Of Work

The system SHALL find active agents by a pull request number (`123` or `#123`), an exact branch name, a directory, an agent name, or a fragment of a branch name longer than two characters, and SHALL show for each match whether its session is busy, idle or offline.

#### Scenario: By pull request
- **WHEN** an agent looks up `#57`
- **THEN** every active agent whose declared or found pull requests include 57 is listed

#### Scenario: By directory
- **WHEN** an agent looks up a path inside another agent's working directory, or a parent of it
- **THEN** that agent is listed

#### Scenario: No match
- **WHEN** nothing active matches
- **THEN** the lookup says so and suggests including inactive agents
