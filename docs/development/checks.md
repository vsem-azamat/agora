# Checks

[Docs](../README.md) / [Development](README.md) / **Checks**

CI runs these checks on every pull request and on `main`. The [`Makefile`](../../Makefile) is the single entry point: CI calls the same targets, so `make check` locally runs what CI runs. Run it before pushing.

| Target | Runs | Verifies |
| --- | --- | --- |
| `make check` | all of the targets below | Everything CI checks |
| `make repo` | `scripts/repo-policy-check.sh`, `node scripts/docs-check.mjs`, `scripts/spec-layout-check.sh`, `openspec validate --specs` | No `CLAUDE.md`, `openspec/changes/` or scratch files tracked; `docs/` indexes, breadcrumbs and relative links; `openspec/specs/` layout and requirement format |
| `make lint` | `lint-go`, `lint-proto` and `lint-web` | Go, protobuf and web lint and formatting |
| `make lint-go` | `golangci-lint run` | Go linters and formatters configured in [`.golangci.yml`](../../.golangci.yml) |
| `make lint-proto` | `buf lint`, `buf format -d --exit-code` | `proto/` follows the standard style and is formatted |
| `make lint-web` | `pnpm lint` (`biome check`) | Biome's recommended rules and formatting, configured in `web/biome.json` |
| `make breaking` | `buf breaking --against "$BREAKING_AGAINST"` | `proto/` has no breaking change against `main` (CI: the pull request base; pull requests only) |
| `make test` | `test-go` and `test-web` | All tests |
| `make test-go` | `go test -race ./...` | Unit and end-to-end tests, with the race detector |
| `make test-web` | `pnpm test` | Vitest: the web app's helpers, hooks and views |
| `make generate-check` | `buf generate`, then a clean `gen/` in git | `gen/` matches `proto/` |
| `make generate-check-web` | `pnpm generate`, then a clean `web/src/gen/` in git | `web/src/gen/` matches `proto/` |
| `make build-check` | `pnpm build` (`tsc --noEmit`, then `vite build`), then a clean `internal/web/dist/` in git | The web app type-checks (strict, with `noUncheckedIndexedAccess`) and the committed build matches `web/` |

The web targets first run `pnpm install --frozen-lockfile` in `web/` when `web/package.json` or the lockfile is newer than the installed dependencies. The generated-code and build checks fail on any uncommitted change in the directory they check, so commit before running them.

`make fmt` applies the formatters (`golangci-lint fmt`, `buf format -w`, `biome check --write`) to the tree. `make help` lists every target.

Requirements: Go (the version in `go.mod`), GNU Make, Node.js 24 or newer (`engines` in `web/package.json`) with pnpm (the version in `web/package.json`), Bash, Git. Every other tool is pinned and runs through `go tool` or pnpm; the first run downloads and builds it.

## Go lint

[`.golangci.yml`](../../.golangci.yml) enables the standard linters (`errcheck`, `govet`, `ineffassign`, `staticcheck`, `unused`) and a curated set on top: `bodyclose`, `contextcheck`, `copyloopvar`, `errorlint`, `gosec`, `intrange`, `misspell`, `nilerr`, `perfsprint`, `revive` (a limited rule set), `rowserrcheck`, `sqlclosecheck`, `unconvert`, `unparam` and `usestdlibvars`. Formatting is `gofumpt` and `goimports` with `github.com/vsem-azamat/agora` as the local import group. Generated code in `gen/` is excluded.

- Rules turned off for the whole tree carry the reason next to them in the config.
- A `//nolint:<linter>` comment names the linter and gives the reason on the same line.
- golangci-lint is pinned in [`tools/lint/go.mod`](../../tools/lint/go.mod), a module of its own so that its dependencies do not change the versions of the code generators in `tools/go.mod`.

## CI

The workflow is [`.github/workflows/ci.yml`](../../.github/workflows/ci.yml). Its three jobs call the targets above: `Repository checks` runs `repo`; `Go` runs `lint-go`, `test-go`, `lint-proto`, `breaking` (pull requests only) and `generate-check`; `Web app` runs `generate-check-web`, `lint-web`, `test-web` and `build-check`. Every action is pinned to a commit SHA, with its release tag in a comment.
