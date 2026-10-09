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
make test                                          # go test -race ./...
```

Before pushing, run `make check`; see [Checks](checks.md).

## Code generation

`proto/` is the source of the API; `gen/` is generated from it and committed. After changing a `.proto` file:

```sh
make fmt                                           # buf format -w (and the Go formatters)
make lint-proto                                    # buf lint and format check
make generate                                      # buf generate
```

The generators (`buf`, `protoc-gen-go`, `protoc-gen-connect-go`) are pinned as tools in `tools/go.mod`, a separate module so they do not add dependencies to the `agora` binary. golangci-lint is pinned the same way in `tools/lint/go.mod`.

The web app's TypeScript client is generated into `web/src/gen/` by `protoc-gen-es`, pinned in `web/package.json`; after changing a `.proto` file also run `pnpm --dir web generate`.

## Web app

`web/` holds the browser app (React, Vite, pnpm). Its build is committed in `internal/web/dist/` and embedded in the binary, so building `agora` needs no Node.js. After changing anything in `web/`:

```sh
pnpm --dir web install --frozen-lockfile
pnpm --dir web lint          # Biome: lint and format check (pnpm --dir web format fixes)
pnpm --dir web test          # Vitest
pnpm --dir web build         # tsc --noEmit, then vite build into internal/web/dist/
```

`pnpm --dir web dev` serves the app with hot reload and proxies API calls to a hub started with `--web 8484`. See [Web app](../architecture/web.md).
