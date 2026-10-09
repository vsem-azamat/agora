# Checks

[Docs](../README.md) / [Development](README.md) / **Checks**

CI runs these checks on every pull request and on `main`. The [`Makefile`](../../Makefile) is the single entry point: CI calls the same targets, so `make check` locally runs what CI runs. Run it before pushing.

| Target | Runs | Verifies |
| --- | --- | --- |
| `make check` | all of the targets below | Everything CI checks |
| `make repo` | `scripts/repo-policy-check.sh`, `node scripts/docs-check.mjs`, `scripts/spec-layout-check.sh`, `openspec validate --specs` | No `CLAUDE.md`, `openspec/changes/` or scratch files tracked; `docs/` indexes, breadcrumbs and relative links; `openspec/specs/` layout and requirement format |
| `make lint` | `lint-go` and `lint-proto` | Go and protobuf lint and formatting |
| `make lint-go` | `golangci-lint run` | Go linters and formatters configured in [`.golangci.yml`](../../.golangci.yml) |
| `make lint-proto` | `buf lint`, `buf format -d --exit-code` | `proto/` follows the standard style and is formatted |
| `make breaking` | `buf breaking --against "$BREAKING_AGAINST"` | `proto/` has no breaking change against `main` (CI: the pull request base; pull requests only) |
| `make test` | `go test -race ./...` | Unit and end-to-end tests, with the race detector |
| `make generate-check` | `buf generate`, then a clean `gen/` in git | `gen/` matches `proto/` |

`make fmt` applies the formatters (`golangci-lint fmt`, `buf format -w`) to the tree.

Requirements: Go (the version in `go.mod`), GNU Make, Node.js 22 or newer with pnpm (the version in `web/package.json`), Bash, Git. Every other tool is pinned and runs through `go tool`; the first run downloads and builds it.

## Go lint

[`.golangci.yml`](../../.golangci.yml) enables the standard linters (`errcheck`, `govet`, `ineffassign`, `staticcheck`, `unused`) and a curated set on top: `bodyclose`, `contextcheck`, `copyloopvar`, `errorlint`, `gosec`, `intrange`, `misspell`, `nilerr`, `perfsprint`, `revive` (a limited rule set), `rowserrcheck`, `sqlclosecheck`, `unconvert`, `unparam` and `usestdlibvars`. Formatting is `gofumpt` and `goimports` with `github.com/vsem-azamat/agora` as the local import group. Generated code in `gen/` is excluded.

- Rules turned off for the whole tree carry the reason next to them in the config.
- A `//nolint:<linter>` comment names the linter and gives the reason on the same line.
- golangci-lint is pinned in [`tools/lint/go.mod`](../../tools/lint/go.mod), a module of its own so that its dependencies do not change the versions of the code generators in `tools/go.mod`.

## Web app

The `Web app` CI job runs these in `web/`:

| Check | Command | Verifies |
| --- | --- | --- |
| Generated client | `pnpm generate`, then a clean `src/gen/` in git | `web/src/gen/` matches `proto/` |
| Lint and format | `pnpm lint` (`pnpm format` fixes) | Biome's recommended rules and formatting, configured in `web/biome.json` |
| Types | `pnpm typecheck` | `tsc --noEmit`, strict, with `noUncheckedIndexedAccess` |
| Tests | `pnpm test` | Vitest: helpers, hooks and views |
| Embedded build | `pnpm build`, then a clean `internal/web/dist/` in git | The committed build matches `web/` |

The workflow is [`.github/workflows/ci.yml`](../../.github/workflows/ci.yml).
