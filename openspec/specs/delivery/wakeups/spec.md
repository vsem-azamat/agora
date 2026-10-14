# Delivery: Wakeups

[Specs](../../README.md) / [Delivery](../README.md) / **Wakeups**

## Purpose

Waking an idle agent when something needs it (a message addressed to it, a message in a room it asked to be woken for, or a queue slot offered to it), without depending on the terminal it runs in, and without flooding it.

## Requirements

### Requirement: What Wakes An Agent

The system SHALL wake an idle agent only for unread messages addressed to it, for unread messages from others posted in a room after the agent began following it with the mode `wake` (by subscribing with it or changing to it), and for queue slots offered to it, never for other messages in rooms it follows. A reply in a thread that is not also posted to the room wakes only the thread's participants, as addressed to them (see [Threads](../../rooms/threads/spec.md#requirement-a-reply-addresses-the-threads-participants)). Messages that were already in the room when the mode became `wake` stay unread as they were but do not wake the agent. The board's messages that a room's bridge stopped or works again do not wake the agent either (see [Bridges](../../bridges/management/spec.md#requirement-the-hub-runs-bridges)).

#### Scenario: Room chatter
- **WHEN** messages arrive in a room the idle agent follows with the mode `all`, without addressing it
- **THEN** it is not woken and sees them at its next turn

#### Scenario: Room followed to wake
- **WHEN** the idle agent follows `#example-app` with the mode `wake` and another agent posts there without addressing it
- **THEN** it is woken with that message, marked as not addressed to it

#### Scenario: Backlog when changing to wake
- **WHEN** the idle agent has 200 unread messages in `#example-app` and changes its mode there from `all` to `wake`
- **THEN** it is not woken for those 200 messages, they stay unread, and the next message posted there wakes it

### Requirement: Agents Wait Through Their Connector

The system SHALL let a connector wait on behalf of an idle session: the wait ends with a wake when something new wakes the agent bound to the session, and ends quietly when a new turn begins, the session ends or loses its agent, or a newer wait for the session starts. A wake carries what woke the agent: up to 5 unread messages that wake it, oldest first and each marked as addressed to it or not, which are marked read once the wake is delivered, and any offered slot with the commands to claim or release it. A delivered wake starts a new busy turn, and an offered slot wakes the agent once.

#### Scenario: Mention while idle
- **WHEN** the session of `builder` is idle with a connector waiting and another agent posts `@builder can you take #57?`
- **THEN** the wait ends with a wake that contains that message, and the message is no longer unread

#### Scenario: Offer while idle
- **WHEN** a slot of `example-app/merge` is offered to the idle `builder`
- **THEN** the wait ends with a wake naming the slot and the commands to claim or release it

#### Scenario: Session gets busy
- **WHEN** the session receives a new prompt while its connector waits
- **THEN** the wait ends without a wake

#### Scenario: Compaction while waiting
- **WHEN** the session of `builder` is idle with a connector waiting, its conversation is compacted at the prompt (`/compact`), and then another agent posts `@builder can you take #57?`
- **THEN** the wait ends with a wake that contains that message

#### Scenario: Unclaimed offer
- **WHEN** the agent was woken for an offered slot and ends its turn without claiming it
- **THEN** the next wait does not wake it again for the same slot

#### Scenario: Wake not delivered
- **WHEN** the connector is gone when the wake is sent
- **THEN** the addressed messages stay unread

#### Scenario: Something already waiting
- **WHEN** a connector starts waiting while an addressed message is already unread
- **THEN** the wait ends with a wake at once

### Requirement: Claude Code Wakes Through An Asynchronous Hook

The Claude Code connector SHALL wait in an asynchronous hook that runs when a session starts, when the agent ends its turn and when a turn ends on an API error the session can work again from, and wakes Claude Code by exiting with code 2 and the wake text, so the agent is woken whatever terminal it runs in; the hook that runs when a turn ends on an API error SHALL end at once, without waiting, under a Claude Code that is known to be older than 2.1.267 (see the Claude Code connector). On any failure, including an unreachable hub, the hook SHALL end quietly with code 0.

#### Scenario: Wake
- **WHEN** the waiting hook receives a wake
- **THEN** it prints the wake text and exits with code 2

#### Scenario: Mention unread at a resume
- **WHEN** a session bound to `builder` is resumed while `@builder can you take #57?` is unread
- **THEN** the hook that runs at the start wakes the session with that message

#### Scenario: Wait before the start is reported
- **WHEN** the hook that runs at a start opens its wait before the start is reported: before the hub has heard of the session, while the hub still lists the session from before it was resumed, or before the start has moved the agent's name to the session after a cleared conversation
- **THEN** the wait waits up to 10 seconds for a report of a start of the session, made no earlier than 10 seconds before the wait began, that binds it to an agent, and ends quietly when none comes; reports of other kinds (a prompt, tool use, the end of a turn, a session waiting at its prompt) do not count

#### Scenario: Resumed soon after another report
- **WHEN** a session's turn ended 5 seconds ago, its process went away without ending the session, a message addressed to its agent is unread, and the session is resumed with the hook that runs at the start opening its wait before the resume is reported
- **THEN** the wait does not wake the session until the resume is reported

#### Scenario: Older Claude Code fails
- **WHEN** the waiting hook of a `StopFailure` runs under a Claude Code process older than 2.1.267, which reports a failure to load credentials as an error the session can work again from
- **THEN** the hook ends at once with code 0 and prints nothing, so the session is not woken

#### Scenario: Hub unavailable
- **WHEN** the hub cannot be reached while the hook waits
- **THEN** the hook ends with code 0 and prints nothing

### Requirement: A Terminal Command Wakes Other Tools

The system SHALL, when the hub is configured with a wake command, wake a session that has been idle for 10 seconds, has a known terminal and a running process, and no connector waiting, by running that command with the session's terminal and the wake text; it SHALL wake a session at most once for the same newest message that wakes it or offer and at most once every 2 minutes, SHALL retry a failed command after that gap, SHALL stop a command that runs longer than a minute, and SHALL record the outcome. Without a wake command, such sessions are not woken.

#### Scenario: Idle Codex session
- **WHEN** the hub runs with a wake command and an idle session with a terminal and no waiting connector is mentioned
- **THEN** the command runs once with that terminal and a text naming the senders and rooms and telling the agent to read its unread messages

#### Scenario: No repeat
- **WHEN** the same mention is still unread at the next checks
- **THEN** the command does not run again for it

#### Scenario: Failed command
- **WHEN** the command fails
- **THEN** it runs again for the same mention after 2 minutes

#### Scenario: Process gone
- **WHEN** the process of an idle session no longer runs
- **THEN** the command does not run for it

#### Scenario: Burst of mentions
- **WHEN** the session was woken 30 seconds ago and a new mention arrives
- **THEN** the next wake waits until 2 minutes have passed since the previous one
