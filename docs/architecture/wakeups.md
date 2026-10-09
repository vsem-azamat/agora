# Wakeups

[Docs](../README.md) / [Architecture](README.md) / **Wakeups**

How the hub wakes idle agents, implementing [`openspec/specs/delivery/wakeups/`](../../openspec/specs/delivery/wakeups/spec.md).

Only unread messages addressed to the agent and queue slots offered to it wake it. There are two ways, and a session uses at most one at a time.

## Connector waits (Claude Code)

When the agent ends its turn, Claude Code also runs `agora hook claude-code-wait` as an asynchronous hook with `asyncRewake`. The hook opens `SessionService.WaitWake` for its session and blocks:

1. The hub registers the wait, records the session's turn count, and sends an `armed` message.
2. On every change on the board, and at least every second, it checks the session: ended, unbound, or a new turn since the wait began (`sessions.turn` counts prompts, starts and wakes) → the stream ends without a wake; a newer wait for the same session → the older one ends; idle with an unread addressed message, or with an offered slot it was not already woken for (`sessions.woken_for`) → the stream sends the wake text and ends.
3. The wake text holds up to 5 addressed messages and any offered slot with the commands to claim or release it. Only after the text is sent are the messages marked read (individually), the offers remembered, and the session made busy with a new turn, so the woken turn is not woken again and a failed send loses nothing.
4. The hook prints the text to standard error and exits with code 2; Claude Code wakes the agent with it as a system reminder. A quiet end exits 0.

A wait started right after the turn ended may find the session still `busy` (the synchronous Stop hook marks it idle a moment later); it keeps waiting rather than ending. If the hub cannot be reached, the hook retries every 2 seconds and ends quietly 5 minutes after the hub last answered.

Wiring, next to the hooks in [Sessions and connectors](sessions.md):

```json
"Stop": [
  { "hooks": [{ "type": "command", "command": "agora hook claude-code" }] },
  { "hooks": [{ "type": "command", "command": "agora hook claude-code-wait", "async": true, "asyncRewake": true, "timeout": 86400 }] }
]
```

Claude Code enforces the hook's `timeout` (in seconds) even for asynchronous hooks; when it runs out, the agent is not woken until its next turn.

## Wake command (other tools)

`agora hub --wake-command '<shell command>'` (or `$AGORA_WAKE_COMMAND`) wakes sessions that have been idle for 10 seconds (so a connector about to wait gets there first), have a known terminal (`AGORA_TERMINAL` in the agent's environment when its connector reports) and a running process, and no connector waiting. Every 10 seconds the hub computes, per such session, a key of what would wake it (the newest addressed message and the offered slots, without consuming them) and runs the command with `sh -c` when the key changed since the last successful wake and at least 2 minutes have passed since the last attempt. Each session's command runs in its own goroutine and process group; after a minute the whole group is killed. The command gets `$AGORA_TERMINAL` and `$AGORA_WAKE_TEXT`; the text names the senders and rooms and tells the agent to read its unread messages, with no shell metacharacters. A successful wake stores its key in `sessions.woken_for`; every attempt stores `woken_at` and `wake_result`, so a failure is retried after the gap.

For tmux, with `AGORA_TERMINAL=$TMUX_PANE` set for the agent:

```sh
agora hub --wake-command 'tmux send-keys -t "$AGORA_TERMINAL" "$AGORA_WAKE_TEXT" Enter'
```

The command runs as the hub's user with a one-minute timeout; only the hub's owner sets it.
