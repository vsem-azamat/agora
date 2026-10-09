# Hub

[Docs](../README.md) / [Architecture](README.md) / **Hub**

The hub is the process that holds Agora's state and serves its API.

## Socket

- The hub listens on a unix socket, readable and writable only by its owner (mode `0600`). Nothing listens on the network.
- Path: `--socket`, else `$AGORA_SOCKET`, else `$XDG_RUNTIME_DIR/agora/hub.sock`, else `<temp dir>/agora-<uid>/hub.sock`. Clients resolve the same default.
- On start, a socket left by a hub that is no longer running is replaced; if another hub answers on it, the new hub refuses to start.

## API

- ConnectRPC services defined in `proto/agora/v1/`, served over HTTP/1.1 and unencrypted HTTP/2 on the socket. Unary calls and server streams both work over HTTP/1.1 with the Connect protocol.
- Services: `ResourceService` (see [Resource queues](resource-queues.md)).
- The CLI client dials the socket directly; the URL host (`http://agora`) is a placeholder.

## Storage

- One SQLite database file: `--db`, else `$AGORA_DB`, else `$XDG_STATE_HOME/agora/agora.db`, else `~/.local/state/agora/agora.db`.
- Driver: `modernc.org/sqlite` (pure Go, no cgo). WAL journal, 5-second busy timeout, foreign keys on.
- The process uses a single connection, so every transaction is serialised.
- Migrations are SQL files in `internal/store/migrations/`, applied in name order on open; `PRAGMA user_version` records how many have run. A new migration is a new file; applied files are never edited.

## Background work

- Every second the hub sweeps resources whose lease or claim deadline has passed, applies the result and wakes every streaming waiter so it can report its new position.
- Waiters are woken through a single change signal; each waiter re-reads its own entry, so a spurious wake costs one small query.

## Identity

The agent name comes from the client (`--as` or `$AGORA_NAME`) and is not authenticated; only the socket's file permissions limit who can reach the hub.
