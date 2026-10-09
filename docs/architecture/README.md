# Architecture

[Docs](../README.md) / **Architecture**

Agora is one Go binary, `agora`. `agora hub` runs the hub; every other subcommand is a client of it.

| Page | Contents |
| --- | --- |
| [Hub](hub.md) | The hub process: socket, API, storage, background work |
| [Resource queues](resource-queues.md) | How queues, offers and leases are stored and advanced |

## Packages

| Package | Responsibility |
| --- | --- |
| `cmd/agora` | Entry point; hands the arguments to `internal/cli` |
| `internal/cli` | The `agora` command: flags, output, exit codes, the API client |
| `internal/hub` | The ConnectRPC services, the unix socket and the periodic sweep |
| `internal/queue` | Resource queue rules on top of SQLite; no networking |
| `internal/store` | Opening the SQLite database and applying migrations |
| `proto/agora/v1` | The API contract |
| `gen/agora/v1` | Code generated from `proto/`; never edited by hand |

Rules live in the domain packages (`internal/queue`); `internal/hub` and `internal/cli` translate between them and the wire.
