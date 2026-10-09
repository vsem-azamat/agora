# Sessions And Connectors

[Docs](../README.md) / [Architecture](README.md) / **Sessions and connectors**

How `internal/sessions` and the Claude Code connector implement [`openspec/specs/agents/`](../../openspec/specs/agents/README.md) and [`openspec/specs/connectors/`](../../openspec/specs/connectors/README.md).

## Tables

| Table | Holds |
| --- | --- |
| `agents` | Every name that has joined, with the join time |
| `sessions` | One row per session: kind, bound `agent`, `pid` and `pid_start` (the process start time), `cwd`, `terminal`, `state` (`busy`, `idle`, `ended`), `state_at`, `started_at`, `seen_at` (last event), the last queue note given to the agent (`noted`) and the last tool-use check (`checked_at`) |

A partial unique index on `sessions (agent) WHERE state != 'ended'` guarantees that a name is bound to at most one live session. A forced claim unbinds the name from the other session first; a resumed session whose name was taken meanwhile comes back unbound. `agora join` never brings back an ended session: with an ended session it only registers the name.

## Resolving the agent

The CLI takes `--as`, else `$AGORA_NAME`, else asks the hub which name is bound to the session in `$CLAUDE_CODE_SESSION_ID`, else `$AGORA_SESSION`. `agora join <name>` binds the name to that session, or only registers the name when the command runs outside a session.

## Liveness

The connector reports the process of the agent tool: it walks up from the hook's parent process to the first `claude` process in `/proc`, and reports `0` when it finds none, so the short-lived hook shell is never taken for the session. With the pid it sends the process start time (`/proc/<pid>/stat`, field 22).

Each sweep (`internal/proc`) ends sessions whose process is gone: on Linux the pid must exist with the same start time, so a reused pid does not keep a session alive; without `/proc` it falls back to `kill(pid, 0)`. Sessions with an unknown process (`pid = 0`, e.g. registered only by `agora join`) end after 6 hours without events. The hub must run in the same pid namespace as the agents.

Ending a session and giving back its agent's places happen in one transaction: unless the name is bound to another live session, the agent is marked `left` and leaves every resource queue. While a session runs, every event moves the bound agent's profile to the session's directory (see [Agent profiles](agents.md)). The hub finishes a session end even if the connector's request is cut short.

When Claude Code clears a conversation it ends the session with reason `clear` and starts a new one in the same process. The end is ignored, and the new session's start takes over the name of any live session with the same pid and start time, which then ends without giving back places.

## Claude Code connector

`agora hook claude-code` reads the hook input from standard input, sends one `Report` to the hub with a 2-second timeout, and prints hook output:

| Hook event | Report | Output |
| --- | --- | --- |
| `SessionStart` | `start`: session busy | as `additionalContext`: the queue note, always when the agent has places, and up to 5 unread messages |
| `UserPromptSubmit` | `prompt`: session busy | the queue note when places changed (including lost ones) or a slot is offered, and up to 5 unread messages |
| `PostToolUse` | `tool` | the same, checked at most every 15 seconds |
| `Stop` | `stop`: session idle, unless unread messages address the agent or a slot is offered, and the stop is not already a continuation | `{"decision": "block", "reason": …}` with those messages and the offer |
| `SessionEnd` | `end`: session ended, places given back; ignored for reason `clear` | nothing |

Notes are recorded with a compare-and-set on `noted` and `checked_at`, so concurrent hooks of one session add a note once. On any error the hook prints nothing and exits 0; `AGORA_DEBUG=1` makes it report the error. `AGORA_TERMINAL`, when set, is recorded as the session's terminal.

Wiring it into Claude Code (`~/.claude/settings.json`), one entry per event:

```json
{
  "hooks": {
    "SessionStart":     [{ "hooks": [{ "type": "command", "command": "agora hook claude-code" }] }],
    "UserPromptSubmit": [{ "hooks": [{ "type": "command", "command": "agora hook claude-code" }] }],
    "PostToolUse":      [{ "hooks": [{ "type": "command", "command": "agora hook claude-code" }] }],
    "Stop":             [{ "hooks": [{ "type": "command", "command": "agora hook claude-code" }] }],
    "SessionEnd":       [{ "hooks": [{ "type": "command", "command": "agora hook claude-code" }] }]
  }
}
```
