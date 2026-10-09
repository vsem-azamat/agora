# Connectors: Sessions

[Specs](../../README.md) / [Connectors](../README.md) / **Sessions**

## Purpose

The running agent sessions that connectors report: their state, how the hub notices that a session's process is gone, and what an ended session gives back.

## Requirements

### Requirement: Sessions Are Registered By Connectors

The system SHALL record each agent session a connector reports with its identifier, tool kind, process, working directory, terminal (when known) and start time, SHALL accept only identifiers of 6 to 80 letters, digits, `_` and `-`, and SHALL register a session on the first event it receives from it.

#### Scenario: New session
- **WHEN** a connector reports a session starting
- **THEN** the session is recorded with those details and state `busy`

#### Scenario: Session that started before the connector
- **WHEN** the first event the hub receives from a session is not its start
- **THEN** the session is registered at that point

### Requirement: Session States

The system SHALL keep each session `busy`, `idle` or `ended` as reported by its connector, with the time of the last change.

#### Scenario: Turn ends
- **WHEN** the agent ends its turn and the connector lets it
- **THEN** its session becomes `idle`

#### Scenario: New prompt
- **WHEN** the agent receives a new prompt
- **THEN** its session becomes `busy`

### Requirement: Dead Processes End Their Sessions

The system SHALL check every second whether the process of each session that has not ended still runs on the hub's machine, telling a reused process number apart by the process start time, and SHALL end the session when it does not. A session whose process is unknown SHALL end after 6 hours without events from it.

#### Scenario: Process gone
- **WHEN** the process of a session exits without its connector reporting the end
- **THEN** by the next check, at most a second later, the session is `ended`

#### Scenario: Process number reused
- **WHEN** the session's process exited and another process now has the same number
- **THEN** the session is `ended`

#### Scenario: Unknown process
- **WHEN** a session whose process is unknown sends no event for 6 hours
- **THEN** it is `ended`, and its name is free

### Requirement: A New Conversation In The Same Process Keeps The Name

The system SHALL move the name of a live session to a session that starts in the same process, and SHALL then end the old session without giving back its places; an end that the agent tool reports as caused by starting a new conversation SHALL not give back places.

#### Scenario: Clearing the conversation
- **WHEN** the agent `builder` holds `db/shared` and its Claude Code conversation is cleared, which ends the session and starts a new one in the same process
- **THEN** the new session is bound to `builder`, and `builder` still holds `db/shared`

### Requirement: Ended Sessions Give Back Their Places

The system SHALL, when a session ends, remove the agent bound to it from every resource queue in the same step, unless that agent is now bound to another session that has not ended.

#### Scenario: Session ends while holding a lock
- **WHEN** the session bound to `builder` ends while `builder` holds `example-app/merge` and waits for `heavy/typecheck`
- **THEN** `example-app/merge` is free and `builder` is no longer queued for `heavy/typecheck`

#### Scenario: Agent continued elsewhere
- **WHEN** `builder` joined from a new session and its old session then ends
- **THEN** `builder` keeps its places in queues

### Requirement: Listing Sessions

The system SHALL list sessions that have not ended with identifier, kind, bound agent, state, time of the last change and working directory.

#### Scenario: Listing
- **WHEN** an agent lists sessions
- **THEN** it sees every session that has not ended
