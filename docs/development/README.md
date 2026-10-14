# Development

[Docs](../README.md) / **Development**

How the repository is organized and verified.

| Page | Contents |
| --- | --- |
| [Building](building.md) | Building and running `agora`, generating code from `proto/` |
| [Specs](specs.md) | Layout and format of `openspec/specs/` |
| [Documentation](documentation.md) | Structure and rules of `docs/` |
| [Checks](checks.md) | `make check`, lint configuration, CI |
| [Deploying](deploying.md) | `make deploy`: installing a checkout as the machine's `agora` and restarting the hub |
| [Releasing](releasing.md) | Version tags, the release workflow and GoReleaser |
| [Code review](code-review.md) | What a review checks: contracts, constants, consistency, tests, errors |

## Repository layout

| Path | Contents |
| --- | --- |
| `cmd/`, `internal/` | Go code; see [Architecture](../architecture/README.md) |
| `proto/` | API contract; `gen/` holds the code generated from it |
| `web/` | The web app's sources; its build is committed in `internal/web/dist/` |
| `tools/` | Pinned development tools: code generators in `tools/go.mod`, golangci-lint in `tools/lint/go.mod` |
| `Makefile` | Entry point for formatting, lint, tests and every CI check but the release config |
| `openspec/specs/` | Behavior specification |
| `docs/` | Engineering documentation |
| `scripts/` | Repository checks and `make deploy` |
| `.github/` | CI and release workflows and the pull request template |
