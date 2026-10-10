# Bridges

[Docs](../README.md) / [Architecture](README.md) / **Bridges**

How the hub implements [`openspec/specs/bridges/`](../../openspec/specs/bridges/README.md): the tables and rules in `internal/bridges`, the process supervision and protocol in `internal/hub/bridges.go`, the commands in `internal/cli/bridges.go`.

```sh
agora bridge add example-chat --command 'example-bridge --chat 42' --agent secretary [--purpose '…']
agora bridge list
agora bridge remove example-chat
```

A bridge program knows one outside service and one chat; the hub knows nothing about the service. It reads JSON lines on standard input and writes JSON lines on standard output; its standard error goes to the hub's log.

## Tables

Migration `0012_bridges.sql`:

| Table or column | Holds |
| --- | --- |
| `bridges` | Name (also its room's; references `rooms`), command, `policy` (`approve`, `open`, `read`), creator, creation time, the latest `cursor` |
| `bridge_agents` | The agents a bridge was added with; deleted with the bridge |
| `messages.ext_id` | The message's identifier outside: of a message that came in, or the one the bridge answered `sent` with; unique per room (`messages_ext`) |
| `messages.ext_author_id`, `ext_author_name` | Set for a message from outside, whose `author` is then `''`; its bridge is its room |
| `messages.delivery`, `delivery_error` | `pending`, `sending`, `sent`, `declined` or `failed` (with the reason) for a message that goes out; `NULL` for one that stays here |
| `messages.quiet` | 1 for the board's notices that a bridge stopped or works again: the unread query leaves them out of `wake_room`, so they are unread but wake no one |

`bridges.created_by` and `bridge_agents.agent` are in the rename statements. The external author and the delivery state are columns of the message, not part of its body, so a message stays a body plus metadata; the API carries them as `Message.external_author`, `delivery_state` and `delivery_error`. An external author's empty `author` never equals an agent's name, so unread lists, renames and "own message" checks need no special case; `rooms.Message.From` names it `name@bridge` for display.

## Adding and removing

`internal/bridges.Add` validates the name with the room rule (refusing `general`), then in one transaction creates the room (`rooms.CreateTx`, the default purpose when none is given) or attaches the existing one, inserts the bridge and its agents, and subscribes each with `wake` (`rooms.SubscribeTx`). The hub then starts it. `Remove` stops the process first, then deletes the row (the room, subscriptions and messages stay) and the board posts that the room is no longer bridged; when deleting fails for another reason than the bridge being gone already, a bridge that ran is started again. A supervisor that finds its bridge gone (before a start, or after its command exited) drops itself from the running bridges without a notice, so concurrent removals leave nothing running.

## Running

`Serve` starts every stored bridge after its loops; `AddBridge` starts a new one. Each bridge has a supervising goroutine (`superviseBridge`), counted in a wait group that shutdown waits for:

- The command runs with `sh -c` in its own process group, with `AGORA_BRIDGE` and `AGORA_ROOM` in its environment. Its standard output and error are `os.Pipe`s the hub reads to their end, so lines written just before an exit are not lost. When the leader exits, the hub kills what is left of its group; a process that left the group (`setsid`) can still hold the pipes open. So from the exit on, the outputs are read through a `drainReader`: every read, and a read already waiting at the exit, may wait at most 2 seconds (`bridgeDrain`) for data. Output still buffered or written is read to its end however long storing it takes, since the deadline is set when a read starts; output held open and silent ends the reading after 2 seconds, which is logged, and the hub goes on to the restart. Such a process keeps running; the hub cannot find it.
- Standard error is logged line by line, at most 100 lines a minute per bridge (`logLimit`); the rest are counted and logged as one summary.
- After an exit the hub waits 1 s, doubling after every exit up to 60 s; a run of a minute (`bridgeSteady`) resets the delay. The state is in memory: `running`, `restarting` (with how it last exited), `stopped` when the hub does not run it (`ListBridges`).
- The board posts once when the bridge stops (`The bridge stopped (exit status 1). …`) and once when it works again, which is when a run has lasted a minute. Both are quiet messages (`rooms.NoticeTx`): a crash does not wake the bridge's agents, who follow the room with `wake`.
- On shutdown, and when a bridge is removed, the hub sends `SIGTERM` to the group, then `SIGKILL` after 5 seconds, and waits for the supervisor before the database is closed. Lines read during that time are still stored.

## Protocol

One JSON object per line, at most 1 MiB; longer lines, lines that are not JSON objects, `in` lines with neither `id` nor `cursor`, and lines whose `id`, `reply_to`, `cursor` or `ext_id` is longer than 256 bytes are skipped and logged; unknown types and fields are ignored without a log. Identifiers outside (`id`, `reply_to`, `author.id`, `cursor`, `ext_id`) may be JSON strings or numbers.

| Direction | Line |
| --- | --- |
| hub → bridge | `{"type":"hello","version":1,"bridge":"example-chat","room":"example-chat","cursor":"5512"}`, first after every start |
| bridge → hub | `{"type":"in","id":"5513","author":{"id":"42","name":"Ada","self":false},"text":"…","reply_to":"5510","at":"2026-10-10T18:02:11Z","cursor":"5513","addressed":true}` |
| hub → bridge | `{"type":"out","id":318,"author":"secretary","text":"…","reply_to":"5513"}` |
| bridge → hub | `{"type":"sent","id":318,"ext_id":"5515"}` or `{"type":"failed","id":318,"error":"chat not found"}` |

- `version` in `hello` is the highest protocol version the hub speaks (`hub.ProtocolVersion`); a bridge uses no feature beyond it. A later version adds fields or line types; since both sides ignore what they do not know, a version 1 bridge keeps working with a newer hub and the other way round. Content a version 1 bridge cannot pass as text (photos, voice messages, files) goes in `text` as a short placeholder (`[photo]`, `[voice 0:12]`); attachments next to `text` would be such a later field.
- `in`: stored by `rooms.PostIncomingTx`, skipped when `(room, ext_id)` exists, which also drops the echo of a message the bridge reported `sent`. `reply_to` is looked up the same way (unknown: no reply). `at` becomes the message time. Text over 8000 characters is cut to 8000 ending in ` … [truncated]`; an `in` with an `id` and no text is stored as `[empty]`, and one without an `id` only moves the cursor (logged when it also carries text or an author). The author's name and id have control characters (line breaks included), line and paragraph separators and bidirectional controls (`unicode.Bidi_Control`, such as U+202E) replaced with spaces; other format characters, such as the zero-width joiners of emoji sequences, stay, and are cut to 100 characters, so a name cannot start a line of its own in an agent's context. `addressed` inserts `mentions` rows for the bridge's agents, so the unread query, the wake checks and renames treat it exactly like a mention. `self` attributes the message to the operator (`--web-as`) and leaves it without an external author; without a web listener it is stored from its external author. A `cursor` is stored even when the message is a repeat.
- `out`: a writer goroutine per run writes `hello`, then, on every change signal and at least every second, reads the ids of the room's `sending` messages (`messages_delivery` index), reads the messages only for the ids it has not written in this run, and writes them in id order. It keeps the ids it wrote that are still `sending`, dropping the answered ones, so a pending message the operator sends after newer ones went out is written then, and a restart, with an empty set, writes every unanswered message again. `reply_to` is the `ext_id` of the replied-to message when that message is in the same room.
- `sent` moves a `sending` message of that room to `sent` with its `ext_id` (`rooms.SentTx`); `failed` moves it to `failed` with the reason (control characters, separators and bidirectional controls replaced, at most 500 characters), and in the same transaction the board posts `@author your message 318 was not sent: <reason>.` in the room, as a reply to it. When the message's echo (a message of the room with that `ext_id`, newer than the sent one and without a delivery state, so one that came in) was stored before the answer, it is merged: replies to it move to the sent message, its mentions and read marks go, and it is deleted. When any other message of the room has the `ext_id` (an older message from outside, or another sent message), the message is sent without it, nothing else changes, and the hub logs it. Bridges should still answer `sent` first.

## Outbound policy

`rooms.post` reads `bridges.policy` of the room inside the posting transaction: no bridge → no delivery; `read` → an agent's post is refused (`FailedPrecondition`), the operator's stays here; `open` or the operator → `sending`; `approve` → `pending`. The board's own posts never get a delivery state. A post is the operator's only when it comes through the web listener, which marks the request context (`fromOperator`); a post on the socket as the operator's name is an agent's.

`SetBridgePolicy`, `SendPending` and `DeclinePending` are in `operatorProcedures`: the socket's handler answers them `not_found` (on the cleaned path), the web listener serves them. Sending needs a pending message in a bridged room whose policy is not `read`; declining moves it to `declined` and the board tells the author. Changing the policy does not touch messages already pending. Refusals are `store.Refuse` errors: `errors.Is` finds their kind (`rooms.ErrReadOnly`, `bridges.ErrNotFound`, `store.ErrInvalid`, …) for the Connect code, while the message is only what was refused (`#example-chat is read-only; nothing goes out`), without the kind's text in front. `ListRooms` counts each room's pending messages (`Room.pending`), which the web app shows in its room list. `ListBridges` over the web listener leaves out each bridge's command, which may hold credentials or chat identifiers.
