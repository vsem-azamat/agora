# Releasing

[Docs](../README.md) / [Development](README.md) / **Releasing**

A release is a `v*` tag on `main`. Only the maintainer creates tags (see [`AGENTS.md`](../../AGENTS.md#git-and-pull-requests)). Pushing the tag runs [`.github/workflows/release.yml`](../../.github/workflows/release.yml):

1. `make build-check` builds the web app in the pipeline and stops when it differs from the committed `internal/web/dist/`. The tree stays clean, so the binaries are not `-dirty`.
2. `make test-go`.
3. [GoReleaser](https://goreleaser.com) (pinned in the workflow) builds the binaries from [`.goreleaser.yaml`](../../.goreleaser.yaml):
   - `agora` for Linux and macOS, on amd64 and arm64;
   - with `CGO_ENABLED=0`, since the SQLite driver is pure Go;
   - with `-trimpath`;
   - with `-X github.com/vsem-azamat/agora/internal/version.Version=<tag>`, so `agora version` prints the tag, `v`-prefixed as `go install …@<tag>` reports it;
   - with the file times of the tagged commit.
4. It packs `agora_<version>_<os>_<arch>.tar.gz` with `LICENSE` and `README.md`, writes `checksums.txt` (SHA-256), and publishes a GitHub release. The release has a changelog from the commits since the previous tag, and is marked as a pre-release when the tag is one, such as `v0.4.0-rc.1`.

To try the configuration without publishing:

```sh
go run github.com/goreleaser/goreleaser/v2@v2.18.2 check
go run github.com/goreleaser/goreleaser/v2@v2.18.2 release --snapshot --clean   # into dist/, which git ignores
```

A snapshot build's `agora version` says `v0.0.0`.
