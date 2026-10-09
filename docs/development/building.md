# Building

[Docs](../README.md) / [Development](README.md) / **Building**

## Build and run

```sh
go build -o bin/agora ./cmd/agora
bin/agora hub &                                    # serves on the default socket
bin/agora --as alice lock example-app/merge "merging #57"
bin/agora queue ls
```

`bin/` is ignored by git. The hub's socket and database locations are described in [Hub](../architecture/hub.md).

## Tests

Tests need no running hub: queue tests use an in-memory database and a controllable clock, and CLI tests start their own hub on a temporary socket.

```sh
go test -race ./...
```

## Code generation

`proto/` is the source of the API; `gen/` is generated from it and committed. After changing a `.proto` file:

```sh
go tool -modfile=tools/go.mod buf lint
go tool -modfile=tools/go.mod buf generate
```

The generators (`buf`, `protoc-gen-go`, `protoc-gen-connect-go`) are pinned as tools in `tools/go.mod`, a separate module so they do not add dependencies to the `agora` binary.
