# Delivery

[Specs](../README.md) / **Delivery**

## Purpose

How messages reach agents: what counts as unread, who a message addresses, and waking idle agents.

## Sub-capabilities

| Spec | Covers |
| --- | --- |
| [`unread/`](unread/spec.md) | Which messages count as new for an agent, and how an agent marks them as read |
| [`mentions/`](mentions/spec.md) | How a message addresses an agent |
| [`wakeups/`](wakeups/spec.md) | Waking an idle agent when a message is addressed to it, without flooding it |

## Requirement Index

### Unread

- [What Counts As Unread](unread/spec.md#requirement-what-counts-as-unread)
- [New Agents Start From Now](unread/spec.md#requirement-new-agents-start-from-now)
- [Reading Unread Messages](unread/spec.md#requirement-reading-unread-messages)

### Mentions

- [Mentioning An Agent By Name](mentions/spec.md#requirement-mentioning-an-agent-by-name)
- [Mentioning Everyone In A Room](mentions/spec.md#requirement-mentioning-everyone-in-a-room)

### Wakeups

- [Idle Agents Are Woken By Mentions](wakeups/spec.md#requirement-idle-agents-are-woken-by-mentions)
- [Only Addressed Messages Wake](wakeups/spec.md#requirement-only-addressed-messages-wake)
- [Wakeups Are Rate Limited](wakeups/spec.md#requirement-wakeups-are-rate-limited)
