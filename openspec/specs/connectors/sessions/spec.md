# Connectors: Sessions

[Specs](../../README.md) / [Connectors](../README.md) / **Sessions**

## Purpose

The running agent sessions that connectors report: their state, how the hub notices that a session's process is gone, and what an ended session gives back.

## Requirements

### Requirement: Sessions Are Registered By Connectors

The system SHALL record each agent session a connector reports with its identifier, tool kind, process, working directory, terminal (when known) and start time, SHALL accept only identifiers of 6 to 80 letters, digits, `_` and `-`, and SHALL register a session on the first event it receives from it.

#### Scenario: New session
- **WHEN** a connector reports a session starting
- **THEN** the session is recorded with those details and state `idle`

#### Scenario: Session that started before the connector
- **WHEN** the first event the hub receives from a session is not its start
- **THEN** the session is registered at that point

### Requirement: Session States

The system SHALL keep each session `busy`, `idle` or `ended` as reported by its connector, with the time of the last change. A session that starts new, resumed or after a cleared conversation SHALL be `idle`; a start that the agent tool reports as caused by compaction SHALL keep the state of a session that is live and SHALL make a session the hub did not know or had ended `idle`, and no start SHALL count as a new turn. A session SHALL become `idle` when its connector reports that it waits at its prompt after a turn that ended without the end being reported. When the connector reports that the conversation is about to be compacted, the session SHALL become `idle` when the user asked for it and `busy` when the agent tool does it by itself, whether or not the session was live before; a report of any other kind of compaction SHALL be refused.

#### Scenario: Turn ends
- **WHEN** the agent ends its turn and the connector lets it
- **THEN** its session becomes `idle`

#### Scenario: New prompt
- **WHEN** the agent receives a new prompt
- **THEN** its session becomes `busy`

#### Scenario: Compacted while idle
- **WHEN** the agent's session is idle after its turn and the conversation is compacted at the prompt (`/compact`), which starts the session again with no turn after it
- **THEN** the session stays `idle`, and a connector waiting on its behalf keeps waiting

#### Scenario: Compacted during a turn
- **WHEN** the agent's session is busy and the conversation is compacted automatically, which starts the session again
- **THEN** the session stays `busy` until its turn ends

#### Scenario: Compaction asked for at the prompt
- **WHEN** the agent's session is `busy` because its turn was interrupted without a stop, and the user compacts the conversation at the prompt, which the connector reports before the compaction and its start
- **THEN** the session is `idle`, and stays `idle` after the compaction's start

#### Scenario: Compaction during a turn of an unknown session
- **WHEN** the hub has not heard of the agent's session and the conversation is compacted automatically, which the connector reports before the compaction's start
- **THEN** the session is `busy` after that report and after the start, until its turn ends

#### Scenario: Compaction of another kind
- **WHEN** the connector reports a compaction that is neither asked for nor automatic
- **THEN** the report is refused and the session's state does not change

#### Scenario: Compaction revives an ended session
- **WHEN** the hub has ended the agent's session, or has not heard of it, and the conversation is compacted at the prompt
- **THEN** the session becomes `idle`

#### Scenario: Turn ended without its end reported
- **WHEN** the agent's turn ends on an error, so the connector never reports its end, and the connector reports that the session waits at its prompt
- **THEN** the session becomes `idle`

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

The system SHALL list sessions that have not ended with identifier, kind, bound agent, state, time of the last change, working directory and what each can do (see [Session Control](../session-control/spec.md#requirement-sessions-have-capabilities)).

#### Scenario: Listing
- **WHEN** an agent lists sessions
- **THEN** it sees every session that has not ended
