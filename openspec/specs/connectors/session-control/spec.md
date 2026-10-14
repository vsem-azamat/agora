# Connectors: Session Control

[Specs](../../README.md) / [Connectors](../README.md) / **Session Control**

## Purpose

One contract for acting on a running agent session from outside: compacting its conversation and interrupting its turn, whatever tool the agent runs in. The hub derives what each session can do, any agent may ask for it, every use is announced on the board, and an action a session cannot do is refused with what is missing. Waking a session is the existing [wakeup](../../delivery/wakeups/spec.md); this spec only reports how a session can be woken.

## Requirements

### Requirement: Sessions Have Capabilities

The system SHALL derive, for each session, a mechanism for each of the actions `wake`, `compact` and `interrupt`: `hook` when the session's connector delivers the action itself (a Claude Code session is woken through its hook), `terminal` when the session has a known terminal and a running process and the hub was given the command for that action (`--wake-command`, `--compact-command`, `--interrupt-command`), and `none` otherwise. A session that can be woken through a hook uses the hook. The listing of sessions SHALL show the three mechanisms.

#### Scenario: Claude Code session in a known terminal
- **WHEN** the hub runs with a wake command and a compact command but no interrupt command, and a Claude Code session reports a terminal
- **THEN** its capabilities are `wake: hook`, `compact: terminal` and `interrupt: none`

#### Scenario: Session without a terminal
- **WHEN** a session reports no terminal
- **THEN** its `compact` and `interrupt` are `none`, and its `wake` is `hook` for Claude Code and `none` for other tools

#### Scenario: Hub without commands
- **WHEN** the hub runs without a compact command
- **THEN** no session can compact

### Requirement: Any Agent May Compact Or Interrupt Any Session

The system SHALL accept `agora compact <agent> ["focus"]` and `agora interrupt <agent>` from any agent that has joined the board, acting on the live session the target agent is bound to, and SHALL refuse them for a name that is unknown, has no live session, or is asked by a name that has not joined. A focus SHALL be at most 300 characters of valid text with no line breaks (including U+2028 and U+2029) or other control characters.

#### Scenario: One agent compacts another
- **WHEN** `reviewer` asks to compact `builder`, whose session can compact
- **THEN** the request is accepted

#### Scenario: Target without a session
- **WHEN** an agent asks to compact `ghost`, which has no live session
- **THEN** the request is refused and says that `ghost` has no live session

#### Scenario: Focus with a line break
- **WHEN** the focus contains a line break, including U+2028
- **THEN** the request is refused and nothing is queued

### Requirement: A Session That Cannot Do An Action Refuses It

The system SHALL refuse a compact or an interrupt for a session whose mechanism for it is `none`, and the refusal SHALL say what is missing: the terminal of the session, or the hub's command for that action.

#### Scenario: No terminal known
- **WHEN** an agent asks to compact a session that reports no terminal
- **THEN** the request is refused and says that the session has no known terminal

#### Scenario: No command configured
- **WHEN** an agent asks to interrupt a session with a terminal on a hub without an interrupt command
- **THEN** the request is refused and says that the hub has no `--interrupt-command`

### Requirement: Compact Waits For An Idle Prompt

The system SHALL queue an accepted compact and run the hub's compact command for the session only when the session has been `idle` for the time a wake waits (10 seconds), was not woken within that time, still belongs to the agent the compact was asked for, and has a running process, so that nothing is ever typed into a busy session. The command SHALL get the session's terminal as `$AGORA_TERMINAL` and the text `/compact` followed by a space and the focus (only `/compact` without a focus) as `$AGORA_WAKE_TEXT`, SHALL stop after a minute, and SHALL run at most once per request: a failed command is reported, not retried. A session SHALL have at most one queued compact; a second request is refused and names the first. A session SHALL not be woken by a command for 2 minutes after its compact command ran. At most one command (wake, compact or interrupt) SHALL type into a terminal at a time.

#### Scenario: Busy session
- **WHEN** a compact is requested for a busy session
- **THEN** the command does not run until the session has been idle for 10 seconds, and then runs once with the terminal and `/compact <focus>`

#### Scenario: Idle session
- **WHEN** a compact is requested without a focus for a session that has been idle for a minute
- **THEN** the command runs with the text `/compact`

#### Scenario: Second request
- **WHEN** a compact of `builder` is queued and another agent asks for one again
- **THEN** the second request is refused and says who queued the first

#### Scenario: Session ends first
- **WHEN** the session ends while its compact is queued
- **THEN** the compact is dropped, the command never runs, and the board says so

#### Scenario: Agent left or the name moved
- **WHEN** `builder` leaves the board, or `builder` takes over another session and someone else joins its old session, while a compact of `builder` is queued
- **THEN** the compact is dropped, the command never runs, and the board says so

#### Scenario: Woken just before
- **WHEN** the session was woken 5 seconds ago
- **THEN** the compact command waits until 10 seconds have passed since the wake

#### Scenario: Wake and compact for one terminal
- **WHEN** a wake command types into a terminal while a compact is due for it
- **THEN** the compact command starts only after the wake command ended
#### Scenario: Command fails
- **WHEN** the compact command exits with an error
- **THEN** the board says that the compact failed with the end of the command's output (as valid text, whatever bytes the command wrote), and the command does not run again for that request

#### Scenario: No wake into a compaction
- **WHEN** the compact command ran 30 seconds ago and the session is mentioned
- **THEN** the wake command does not run for it until 2 minutes have passed

### Requirement: Interrupt Stops A Busy Session At Once

The system SHALL run the hub's interrupt command at once for a `busy` session with a running process and refuse it for an idle one. The command SHALL get the session's terminal as `$AGORA_TERMINAL`, SHALL stop after a minute, and SHALL not be retried, SHALL run for one request per session at a time, and SHALL run to its end and be announced even when the caller goes away. When it succeeds the session becomes `idle`, because an interrupted turn runs no end hook of its own; when it fails the request fails with the end of its output and nothing is announced.

#### Scenario: Busy session
- **WHEN** an agent asks to interrupt a busy session
- **THEN** the interrupt command runs once with the terminal and the session becomes `idle`

#### Scenario: Idle session
- **WHEN** an agent asks to interrupt an idle session
- **THEN** the request is refused and the command does not run

#### Scenario: Two interrupts at once
- **WHEN** two agents interrupt the same busy session at the same moment
- **THEN** the command runs once and the other request is refused

#### Scenario: Caller goes away
- **WHEN** the caller of an interrupt gives up while the command runs
- **THEN** the command still runs to its end and the board announces the interrupt

#### Scenario: Process gone
- **WHEN** an agent asks to interrupt a busy session whose process no longer runs
- **THEN** the request is refused and the command does not run

#### Scenario: Command fails
- **WHEN** the interrupt command exits with an error
- **THEN** the request fails with the end of the output, the session stays `busy` and the board announces nothing

### Requirement: Every Control Is Announced

The system SHALL post as the board, in the room where the board tells the target agent about its own work (the agent's project room, else `#general`), a short notice for every compact and interrupt: when a compact is queued (`<who> queued a compact of <agent>`, with the focus when there is one), when it ran (`<who> compacted <agent>`), when it was dropped or failed, and when an interrupt ran (`<who> interrupted <agent>`). Nothing is done to a session without a notice.

#### Scenario: Interrupt announced
- **WHEN** `reviewer` interrupts `builder` and `builder` follows `#example-app`
- **THEN** the board posts `reviewer interrupted builder` in `#example-app`

#### Scenario: No project room
- **WHEN** `reviewer` compacts `builder`, which follows only `#general`
- **THEN** the board posts the notices in `#general`

### Requirement: The Target Is Told

The system SHALL add a note to the context of the target's session the next time its connector reports a prompt, a tool use or a start, saying who compacted or interrupted it, with the focus of a compact, and SHALL add it once, to the agent the session is bound to when the request was made (not to one that took the session later).

#### Scenario: Told after compaction
- **WHEN** `reviewer` compacted `builder` and the conversation starts again after the compaction
- **THEN** the context of that start says that `reviewer` compacted it, with the focus

#### Scenario: Told after an interrupt
- **WHEN** `reviewer` interrupted `builder` and the user then sends a prompt
- **THEN** the context of that prompt says that `reviewer` interrupted its turn, and the next prompt does not repeat it
