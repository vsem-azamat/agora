# Releasing

[Docs](../README.md) / [Development](README.md) / **Releasing**

A release is a `v*` tag on `main`. Only the maintainer creates tags (see [`AGENTS.md`](../../AGENTS.md#git-and-pull-requests)). Pushing the tag runs [`.github/workflows/release.yml`](../../.github/workflows/release.yml). The checkout keeps no credentials, since the build and the tests run third-party code. Then:

1. It stops unless the tagged commit is on `main`.
2. `make build-check` builds the web app in the pipeline and stops when it differs from the committed `internal/web/dist/`. The tree stays clean, so the binaries are not `-dirty`.
3. `make test-go`.
4. [GoReleaser](https://goreleaser.com) (pinned in the workflow) builds the binaries from [`.goreleaser.yaml`](../../.goreleaser.yaml):
   - `agora` for Linux and macOS, on amd64 and arm64;
   - with `CGO_ENABLED=0`, since the SQLite driver is pure Go;
   - with `-trimpath`;
   - with `-X github.com/vsem-azamat/agora/internal/version.Version=<tag>`, so `agora version` prints the tag, `v`-prefixed as `go install …@<tag>` reports it;
   - with the file times of the tagged commit.
   GoReleaser is given the pushed tag (`GORELEASER_CURRENT_TAG`), so a final tag on the commit of an rc tag builds the final version.
5. It packs `agora_<version>_<os>_<arch>.tar.gz` with `LICENSE` and `README.md`, writes `checksums.txt` (SHA-256), and publishes a GitHub release. The release has a changelog from the commits since the previous tag, and is marked as a pre-release when the tag is one, such as `v0.4.0-rc.1`.

To try the configuration without publishing:

```sh
go run github.com/goreleaser/goreleaser/v2@v2.18.2 check
go run github.com/goreleaser/goreleaser/v2@v2.18.2 release --snapshot --clean   # into dist/, which git ignores
```

A snapshot gets no version at build time, so its `agora version` says `dev`, never the last release.

The README installs the latest release, which it resolves through the `releases/latest` redirect, so it needs no change for a new version.

When a release fails midway, GoReleaser leaves a draft release. Delete the draft with its assets on the Releases page, fix the cause, and run the workflow again for the tag (Actions → the run → Re-run all jobs).
