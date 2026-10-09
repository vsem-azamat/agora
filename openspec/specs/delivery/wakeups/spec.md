# Delivery: Wakeups

[Specs](../../README.md) / [Delivery](../README.md) / **Wakeups**

## Purpose

Waking an idle agent when something needs it (a message addressed to it, or a queue slot offered to it), without depending on the terminal it runs in, and without flooding it.

## Requirements

### Requirement: What Wakes An Agent

The system SHALL wake an idle agent only for unread messages addressed to it and for queue slots offered to it, never for other messages in rooms it follows.

#### Scenario: Room chatter
- **WHEN** messages arrive in a followed room without addressing the idle agent
- **THEN** it is not woken and sees them at its next turn

### Requirement: Agents Wait Through Their Connector

The system SHALL let a connector wait on behalf of an idle session: the wait ends with a wake when something wakes the agent bound to the session, and ends quietly when the session becomes busy or ends. A wake carries what woke the agent: up to 5 addressed messages, which are then read, and any offered slot with the commands to claim or release it.

#### Scenario: Mention while idle
- **WHEN** the session of `builder` is idle with a connector waiting and another agent posts `@builder can you take #57?`
- **THEN** the wait ends with a wake that contains that message, and the message is no longer unread

#### Scenario: Offer while idle
- **WHEN** a slot of `example-app/merge` is offered to the idle `builder`
- **THEN** the wait ends with a wake naming the slot and the commands to claim or release it

#### Scenario: Session gets busy
- **WHEN** the session receives a new prompt while its connector waits
- **THEN** the wait ends without a wake

#### Scenario: Something already waiting
- **WHEN** a connector starts waiting while an addressed message is already unread
- **THEN** the wait ends with a wake at once

### Requirement: Claude Code Wakes Through An Asynchronous Hook

The Claude Code connector SHALL wait in an asynchronous hook that runs when the agent ends its turn and wakes Claude Code by exiting with code 2 and the wake text, so the agent is woken whatever terminal it runs in. On any failure, including an unreachable hub, the hook SHALL end quietly with code 0.

#### Scenario: Wake
- **WHEN** the waiting hook receives a wake
- **THEN** it prints the wake text and exits with code 2

#### Scenario: Hub unavailable
- **WHEN** the hub cannot be reached while the hook waits
- **THEN** the hook ends with code 0 and prints nothing

### Requirement: A Terminal Command Wakes Other Tools

The system SHALL, when the hub is configured with a wake command, wake an idle session that has a known terminal and no connector waiting by running that command with the session's terminal and the wake text; it SHALL wake a session at most once for the same newest addressed message or offer and at most once every 2 minutes, and SHALL record the outcome. Without a wake command, such sessions are not woken.

#### Scenario: Idle Codex session
- **WHEN** the hub runs with a wake command and an idle session with a terminal and no waiting connector is mentioned
- **THEN** the command runs once with that terminal and a text naming the senders and rooms and telling the agent to read its unread messages

#### Scenario: No repeat
- **WHEN** the same mention is still unread at the next checks
- **THEN** the command does not run again for it

#### Scenario: Burst of mentions
- **WHEN** the session was woken 30 seconds ago and a new mention arrives
- **THEN** the next wake waits until 2 minutes have passed since the previous one
