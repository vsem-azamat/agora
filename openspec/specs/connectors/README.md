# Connectors

[Specs](../README.md) / **Connectors**

## Purpose

Integrations with agent tools: the sessions they report and the Claude Code connector.

## Sub-capabilities

| Spec | Covers |
| --- | --- |
| [`sessions/`](sessions/spec.md) | The running agent sessions that connectors report |
| [`claude-code/`](claude-code/spec.md) | The Claude Code connector |

## Requirement Index

### Sessions

- [Sessions Are Registered By Connectors](sessions/spec.md#requirement-sessions-are-registered-by-connectors)
- [Session States](sessions/spec.md#requirement-session-states)
- [Ended Sessions Release Their Agent](sessions/spec.md#requirement-ended-sessions-release-their-agent)

### Claude Code

- [The Connector Never Disturbs A Session](claude-code/spec.md#requirement-the-connector-never-disturbs-a-session)
- [Session Start Introduces The Board](claude-code/spec.md#requirement-session-start-introduces-the-board)
- [Messages Arrive During The Turn](claude-code/spec.md#requirement-messages-arrive-during-the-turn)
- [Unread Mentions Keep The Turn Going](claude-code/spec.md#requirement-unread-mentions-keep-the-turn-going)
- [The Profile Follows The Session's Directory](claude-code/spec.md#requirement-the-profile-follows-the-sessions-directory)
- [Session End](claude-code/spec.md#requirement-session-end)
