# Sessions And Connectors

[Docs](../README.md) / [Architecture](README.md) / **Sessions and connectors**

How `internal/sessions` and the Claude Code connector implement [`openspec/specs/agents/`](../../openspec/specs/agents/README.md) and [`openspec/specs/connectors/`](../../openspec/specs/connectors/README.md).

## Tables

| Table | Holds |
| --- | --- |
| `agents` | Every name that has joined, with the join time |
| `sessions` | One row per session: kind, bound `agent`, `pid`, `cwd`, `terminal`, `state` (`busy`, `idle`, `ended`), `state_at`, `started_at`, the last queue note given to the agent (`noted`) and the last tool-use check (`checked_at`) |

A partial unique index on `sessions (agent) WHERE state != 'ended'` guarantees that a name is bound to at most one live session. A forced claim unbinds the name from the other session first; a resumed session whose name was taken meanwhile comes back unbound.

## Resolving the agent

The CLI takes `--as`, else `$AGORA_NAME`, else asks the hub which name is bound to the session in `$AGORA_SESSION` or `$CLAUDE_CODE_SESSION_ID`. `agora join <name>` binds the name to that session, or only registers the name when the command runs outside a session.

## Liveness

The connector reports the process of the agent tool: it walks up from the hook's parent process to the first `claude` process in `/proc`, and reports `0` when it finds none, so the short-lived hook shell is never taken for the session. Each sweep ends sessions whose process no longer exists (`kill(pid, 0)`); sessions with `pid = 0` are only ended by their connector. When a session ends, its agent leaves every resource queue unless the name is already bound to another live session.

## Claude Code connector

`agora hook claude-code` reads the hook input from standard input, sends one `Report` to the hub with a 2-second timeout, and prints hook output:

| Hook event | Report | Output |
| --- | --- | --- |
| `SessionStart` | `start`: session busy | the queue note as `additionalContext`, always when the agent has places |
| `UserPromptSubmit` | `prompt`: session busy | the queue note when it changed or a slot is offered |
| `PostToolUse` | `tool` | the same, checked at most every 15 seconds |
| `Stop` | `stop`: session idle, unless a slot is offered and the stop is not already a continuation | `{"decision": "block", "reason": …}` when blocked |
| `SessionEnd` | `end`: session ended, places given back | nothing |

On any error the hook prints nothing and exits 0; `AGORA_DEBUG=1` makes it report the error. `AGORA_TERMINAL`, when set, is recorded as the session's terminal.

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
