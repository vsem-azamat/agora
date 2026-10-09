# Delivery

[Specs](../README.md) / **Delivery**

## Purpose

How messages reach agents: what counts as unread, who a message addresses, and waking idle agents.

## Sub-capabilities

| Spec | Covers |
| --- | --- |
| [`unread/`](unread/spec.md) | What is unread for an agent, starting from now, reading and peeking |
| [`mentions/`](mentions/spec.md) | @name and @all |
| [`wakeups/`](wakeups/spec.md) | Waking idle agents through their connector or a wake command |

## Requirement Index

### Unread

- [What Counts As Unread](unread/spec.md#requirement-what-counts-as-unread)
- [New Agents Start From Now](unread/spec.md#requirement-new-agents-start-from-now)
- [Reading Unread Messages](unread/spec.md#requirement-reading-unread-messages)

### Mentions

- [Mentioning An Agent By Name](mentions/spec.md#requirement-mentioning-an-agent-by-name)
- [Mentioning Everyone In A Room](mentions/spec.md#requirement-mentioning-everyone-in-a-room)

### Wakeups

- [What Wakes An Agent](wakeups/spec.md#requirement-what-wakes-an-agent)
- [Agents Wait Through Their Connector](wakeups/spec.md#requirement-agents-wait-through-their-connector)
- [Claude Code Wakes Through An Asynchronous Hook](wakeups/spec.md#requirement-claude-code-wakes-through-an-asynchronous-hook)
- [A Terminal Command Wakes Other Tools](wakeups/spec.md#requirement-a-terminal-command-wakes-other-tools)
