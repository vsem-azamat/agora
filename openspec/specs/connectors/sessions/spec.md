# Connectors: Sessions

[Specs](../../README.md) / [Connectors](../README.md) / **Sessions**

## Purpose

The running agent sessions that connectors report: their state, and what happens when a session ends or its process disappears.

## Requirements

### Requirement: Sessions Are Registered By Connectors

The system SHALL record for each agent session reported by a connector its identifier, tool kind, process, working directory, terminal (when known) and start time, and SHALL accept only identifiers of 6 to 80 letters, digits, `_` and `-`.

#### Scenario: New session
- **WHEN** a connector reports a session starting
- **THEN** the session is recorded with those details and state `busy`

#### Scenario: Session that started before the connector
- **WHEN** a connector receives its first event from a session it has not seen
- **THEN** the session is registered at that point

### Requirement: Session States

The system SHALL keep each session in one of the states `busy`, `idle` or `ended` as reported by its connector, with the time of the last change.

#### Scenario: Turn ends
- **WHEN** the agent ends its turn with nothing addressed to it
- **THEN** its session becomes `idle`

#### Scenario: New prompt
- **WHEN** the agent receives a new prompt
- **THEN** its session becomes `busy`

### Requirement: Ended Sessions Release Their Agent

The system SHALL mark a session `ended` when its connector reports the end or when its process no longer exists, checking processes every 10 seconds, and SHALL then mark the agent bound to it as `left`, releasing its locks, unless the agent has meanwhile been bound to another session.

#### Scenario: Process gone
- **WHEN** the process of a session bound to `builder` exits without notice
- **THEN** by the next check, at most 10 seconds later, the session is `ended`, `builder` is `left` and its locks are free

#### Scenario: Agent continued elsewhere
- **WHEN** `builder` joined from a new session and the old session then ends
- **THEN** `builder` keeps its status
