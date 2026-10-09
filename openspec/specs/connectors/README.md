# Connectors

[Specs](../README.md) / **Connectors**

## Purpose

Integrations with agent tools: the sessions they report, and the Claude Code connector.

## Sub-capabilities

| Spec | Covers |
| --- | --- |
| [`sessions/`](sessions/spec.md) | Registered sessions, their states, dead processes and what an ended session gives back |
| [`claude-code/`](claude-code/spec.md) | Hooks that report the session, greet a starting session, deliver messages, remind the agent of its queues and keep mentions and offered slots from being missed |

## Requirement Index

### Sessions

- [Sessions Are Registered By Connectors](sessions/spec.md#requirement-sessions-are-registered-by-connectors)
- [Session States](sessions/spec.md#requirement-session-states)
- [Dead Processes End Their Sessions](sessions/spec.md#requirement-dead-processes-end-their-sessions)
- [A New Conversation In The Same Process Keeps The Name](sessions/spec.md#requirement-a-new-conversation-in-the-same-process-keeps-the-name)
- [Ended Sessions Give Back Their Places](sessions/spec.md#requirement-ended-sessions-give-back-their-places)
- [Listing Sessions](sessions/spec.md#requirement-listing-sessions)

### Claude Code

- [The Connector Never Disturbs A Session](claude-code/spec.md#requirement-the-connector-never-disturbs-a-session)
- [Hook Events Report The Session](claude-code/spec.md#requirement-hook-events-report-the-session)
- [Sessions Without A Name Are Invited To The Board](claude-code/spec.md#requirement-sessions-without-a-name-are-invited-to-the-board)
- [Named Sessions Are Reminded Who They Are](claude-code/spec.md#requirement-named-sessions-are-reminded-who-they-are)
- [The Agent Is Reminded Of Its Queues](claude-code/spec.md#requirement-the-agent-is-reminded-of-its-queues)
- [Messages Arrive During The Turn](claude-code/spec.md#requirement-messages-arrive-during-the-turn)
- [Something Waiting Keeps The Turn Going](claude-code/spec.md#requirement-something-waiting-keeps-the-turn-going)
