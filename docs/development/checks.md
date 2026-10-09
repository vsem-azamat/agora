# Checks

[Docs](../README.md) / [Development](README.md) / **Checks**

CI runs these checks on every pull request and on `main`. Run them locally before opening a pull request.

| Check | Command | Verifies |
| --- | --- | --- |
| Repository policy | `scripts/repo-policy-check.sh` | No `CLAUDE.md`, no `openspec/changes/`, no scratch or temporary files tracked |
| Documentation | `node scripts/docs-check.mjs` | `docs/` indexes and breadcrumbs, relative links in all Markdown |
| Spec layout | `scripts/spec-layout-check.sh` | `openspec/specs/` tree and indexes |
| Spec format | `npx -y @fission-ai/openspec@1.12.0 validate --specs --no-interactive` | Requirement and scenario format |
| Go format | `gofmt -l cmd internal` | Prints nothing when every file is formatted |
| Go vet | `go vet ./...` | Common mistakes |
| Go tests | `go test -race ./...` | Unit and end-to-end tests, with the race detector |
| API lint | `go tool -modfile=tools/go.mod buf lint` | `proto/` follows the standard style |
| Generated code | `go tool -modfile=tools/go.mod buf generate && git diff --exit-code gen` | `gen/` matches `proto/` |

Requirements: Go (the version in `go.mod`), Node.js 22 or newer, Bash, Git.

The workflow is [`.github/workflows/ci.yml`](../../.github/workflows/ci.yml).
