# Releasing

[Docs](../README.md) / [Development](README.md) / **Releasing**

A release is a `v*` tag on `main`. Only the maintainer creates tags (see [`AGENTS.md`](../../AGENTS.md#git-and-pull-requests)). Pushing the tag runs [`.github/workflows/release.yml`](../../.github/workflows/release.yml). The checkout keeps no credentials, since the build and the tests run third-party code. Then:

1. It stops unless the tagged commit is on `main`.
2. `make test-go`, which builds the web app first.
3. [GoReleaser](https://goreleaser.com) (pinned in the workflow) builds the web app (its `before` hook runs `make build-web`) and then the binaries from [`.goreleaser.yaml`](../../.goreleaser.yaml):
   - `agora` for Linux and macOS, on amd64 and arm64;
   - with `CGO_ENABLED=0`, since the SQLite driver is pure Go;
   - with `-trimpath`;
   - with `-X github.com/vsem-azamat/agora/internal/version.Version=<tag>`, so `agora version` prints the tag, `v`-prefixed as `go install …@<tag>` reports it;
   - with the file times of the tagged commit.
   GoReleaser is given the pushed tag (`GORELEASER_CURRENT_TAG`), so a final tag on the commit of an rc tag builds the final version.
4. It packs `agora_<version>_<os>_<arch>.tar.gz` with `LICENSE` and `README.md`, writes `checksums.txt` (SHA-256), and publishes a GitHub release. The release has a changelog from the commits since the previous tag, and is marked as a pre-release when the tag is one, such as `v0.4.0-rc.1`.

CI runs the same GoReleaser (the action's prebuilt binary, which builds with the Go on `PATH`, the version in `go.mod`) with `check` and `release --snapshot --clean` on every pull request: the `Release config` job, see [Checks](checks.md). A snapshot builds, packs and checksums every target; the changelog and the publishing steps run only on a real tag. To try it by hand without publishing:

```sh
go run github.com/goreleaser/goreleaser/v2@v2.18.2 check
go run github.com/goreleaser/goreleaser/v2@v2.18.2 release --snapshot --clean   # into dist/, which git ignores
```

`go run` builds GoReleaser with the newer Go it requires and may switch the toolchain for the builds too, so check `go version` of a binary built this way before relying on it.

A snapshot gets no version at build time, so its `agora version` says `dev`, or the tag when the commit has one; it never claims a release it was not built from.

The README installs the latest release, which it resolves through the `releases/latest` redirect, so it needs no change for a new version.

When a release fails midway, GoReleaser leaves a draft release; delete it on the Releases page. When the cause was outside the repository (an outage, a token), run the workflow again for the tag (Actions → the run → Re-run all jobs). When it is in the tagged commit, a re-run fails the same way: fix it on `main` and push a new version tag.
