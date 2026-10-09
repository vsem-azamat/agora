# Connectors: Claude Code

[Specs](../../README.md) / [Connectors](../README.md) / **Claude Code**

## Purpose

The Claude Code connector runs as Claude Code hooks (`agora hook claude-code`). It reports the session to the hub, reminds the agent of its places in resource queues, and keeps it from ending a turn while a slot waits for it.

## Requirements

### Requirement: The Connector Never Disturbs A Session

The connector SHALL exit successfully and print nothing whenever it fails, including when the hub cannot be reached, and SHALL give up on the hub after 2 seconds, so a problem in Agora never interrupts or delays the agent's session. With the `AGORA_DEBUG` environment variable set, it SHALL report its errors instead.

#### Scenario: Hub not running
- **WHEN** a hook runs while no hub is running
- **THEN** the hook succeeds with no output

#### Scenario: Malformed input
- **WHEN** the hook receives input it cannot parse
- **THEN** the hook succeeds with no output

### Requirement: Hook Events Report The Session

The connector SHALL report session start, new prompts, tool use, the end of a turn and session end to the hub, with the session's identifier, the Claude Code process and its start time, the working directory, the terminal named by `AGORA_TERMINAL` when it is set, and the reason Claude Code gives for a session end.

#### Scenario: Start and end
- **WHEN** a Claude Code session starts and later exits
- **THEN** the hub records the session as started and then as `ended`

### Requirement: The Agent Is Reminded Of Its Queues

On session start, on a new prompt and after tool use (at most once every 15 seconds for tool use), the connector SHALL add to the agent's context a short note of every resource the agent bound to the session holds, waits for or is offered, and of every resource it lost since the last note, when that changed since the last note or when the agent was offered a slot; the note says how to claim an offered slot. Concurrent hooks of one session add a note once.

#### Scenario: Offered slot
- **WHEN** a slot of `example-app/merge` is offered to `builder` while it runs tools
- **THEN** after a later tool call the context says it is `builder`'s turn on `example-app/merge`, until when, and that `agora queue renew example-app/merge` claims it

#### Scenario: Lost lock
- **WHEN** the lease of `db/shared` held by `builder` ended since the last note
- **THEN** the next note says that `builder` no longer holds `db/shared`

#### Scenario: Nothing changed
- **WHEN** the agent's places in queues are the same as at the last note and nothing is offered
- **THEN** no note is added

#### Scenario: Session start
- **WHEN** a session bound to `builder` starts or resumes while `builder` holds `db/shared`
- **THEN** the context says that `builder` holds `db/shared` and until when

### Requirement: An Offered Slot Keeps The Turn Going

When the agent tries to end its turn while a slot is offered to it, the connector SHALL block the end once and tell it which slot is waiting and how to claim or release it; otherwise the session becomes `idle`.

#### Scenario: Ending the turn with an offer
- **WHEN** `builder` ends its turn while it is offered `example-app/merge`
- **THEN** the turn continues with a note naming the slot and the commands to claim or release it

#### Scenario: Second attempt
- **WHEN** the agent ends its turn again in the continuation caused by the connector
- **THEN** the turn ends and the session becomes `idle`
