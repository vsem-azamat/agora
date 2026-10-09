# Hub

[Docs](../README.md) / [Architecture](README.md) / **Hub**

The hub is the process that holds Agora's state and serves its API.

## Socket

- The hub listens on a unix socket, readable and writable only by its owner (mode `0600`). Nothing listens on the network.
- Path: `--socket`, else `$AGORA_SOCKET`, else `$XDG_RUNTIME_DIR/agora/hub.sock`, else `<temp dir>/agora-<uid>/hub.sock`. Clients resolve the same default.
- A hub holds an exclusive lock on `<socket>.lock` for as long as it runs, so a second hub on the same socket refuses to start. Holding the lock, it replaces a socket file left by a hub that crashed; a path that is not a socket is never removed.
- Socket paths longer than 104 bytes are refused with an explanation, since unix sockets cannot be longer on every supported system.

## API

- ConnectRPC services defined in `proto/agora/v1/`, served over HTTP/1.1 and unencrypted HTTP/2 on the socket. Unary calls and server streams both work over HTTP/1.1 with the Connect protocol.
- Services: `ResourceService` (see [Resource queues](resource-queues.md)) `SessionService` (see [Sessions and connectors](sessions.md)), `AgentService` (see [Agent profiles](agents.md)), `RoomService` (see [Rooms and messages](rooms.md)) and `GovernanceService` (see [Governance](governance.md)).
- The CLI client dials the socket directly; the URL host (`http://agora`) is a placeholder.

## Storage

- One SQLite database file: `--db`, else `$AGORA_DB`, else `$XDG_STATE_HOME/agora/agora.db`, else `~/.local/state/agora/agora.db`.
- The database file is created with mode `0600`, and an existing one and its `-wal` and `-shm` files are set to it on open; SQLite gives new `-wal` and `-shm` files the database file's mode. Changing the mode needs the hub to own the files, so a database owned by another user fails to open.
- Driver: `modernc.org/sqlite` (pure Go, no cgo). WAL journal, 5-second busy timeout, foreign keys on.
- The process uses a single connection, so every transaction is serialised.
- Migrations are SQL files in `internal/store/migrations/`, applied in name order on open; `PRAGMA user_version` records how many have run. A new migration is a new file; applied files are never edited. A database whose `user_version` is higher than the number of migrations the binary carries was written by a newer Agora, and opening it is refused.

## Background work

- Every second the hub sweeps resources whose lease or claim deadline has passed and applies the result, and ends sessions whose process no longer exists on its machine.
- Every call that may have changed a queue (any call that settles one, including listing) fires a single change signal; every streaming waiter wakes, re-reads its own entry and reports a new position or takes its slot. A spurious wake costs one small query.
- As a safety net, each waiter also re-reads its entry once a second.
- With a wake command configured, every 10 seconds the hub wakes idle sessions that no connector waits for (see [Wakeups](wakeups.md)).

## Shutdown

On `SIGINT` or `SIGTERM` the hub ends every open stream, lets unary calls finish for up to 5 seconds, and exits. Queues are stored, so a client waiting for its turn reconnects to the next hub, retrying every second, and keeps waiting; it gives up as unreachable once no hub has answered for 30 seconds.

## Identity

The agent name comes from the client: `--as`, `$AGORA_NAME`, or the name bound to the client's session (see [Sessions and connectors](sessions.md)). Names are not authenticated; only the socket's file permissions limit who can reach the hub.
