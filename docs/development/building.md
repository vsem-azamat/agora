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

## Version

`agora version` (and `agora --version`) prints `agora <version> (<commit>)`; the hub sends the same to the web app, which shows it in Settings. `internal/version` reads them from the build: the version is `version.Version` when set at build time (`go build -ldflags "-X github.com/vsem-azamat/agora/internal/version.Version=v0.4.0"`, as a release build does), else the module version the toolchain recorded (`go install …@v0.4.0`, or a checkout at a tag; `+dirty` dropped) unless it is a pseudo-version, else `dev`; the commit is the first 12 characters of `vcs.revision`, which `go build` records inside a git checkout, else of the revision a pseudo-version names (`go install …@latest` of an untagged module records no `vcs.revision`), with `-dirty` after it when `vcs.modified` is set (untracked files count too), else `unknown`. Pseudo-versions are recognised with `golang.org/x/mod/module`.

## Tests

Tests need no running hub: queue tests use an in-memory database and a controllable clock, and CLI tests start their own hub on a temporary socket.

```sh
make test                                          # go test -race ./...
```

Before pushing, run `make check`; see [Checks](checks.md).

## Code generation

`proto/` is the source of the API; `gen/` is generated from it and committed. After changing a `.proto` file:

```sh
make fmt                                           # buf format -w (and the Go and web formatters)
make lint-proto                                    # buf lint and format check
make generate                                      # buf generate into gen/
make generate-web                                  # protoc-gen-es into web/src/gen/
```

The generators (`buf`, `protoc-gen-go`, `protoc-gen-connect-go`) are pinned as tools in `tools/go.mod`, a separate module so they do not add dependencies to the `agora` binary. golangci-lint is pinned the same way in `tools/lint/go.mod`.

The web app's TypeScript client is generated into `web/src/gen/` by `protoc-gen-es`, pinned in `web/package.json`.

## Web app

`web/` holds the browser app (React, Vite, pnpm). `make build-web` builds it into `internal/web/dist/`, which the binary embeds. Git ignores that directory except for `internal/web/dist/.keep`, which the build writes back unchanged, so `go build` and `go install` without `make build-web` still compile: the binary then serves only the API, and its page says the web app is not built in. Release archives have the web app. After changing anything in `web/`:

```sh
make lint-web                # Biome: lint and format check (make fmt fixes)
make test-web                # Vitest
make build-web               # tsc --noEmit, then vite build into internal/web/dist/
```

Each target installs `web/node_modules` with `pnpm install --frozen-lockfile` when needed. Node.js 24 or newer is required.

`pnpm --dir web dev` serves the app with hot reload and proxies API calls to a hub started with `--web 8484`. See [Web app](../architecture/web.md).
