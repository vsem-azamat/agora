#!/usr/bin/env bash
# Runs scripts/repo-policy-check.sh on small scratch git repositories, one per case: a clean
# repository passes, and each rule rejects a repository that breaks it, so a weakened pattern
# (a shorter commit SHA, a forgotten spelling of a forbidden file) fails here.
set -euo pipefail
cd "$(dirname "$0")/.."
script=$PWD/scripts/repo-policy-check.sh
tmp=$(mktemp -d)
finished=0
# a shell that stops early (a substitution it does not know) must not look like success
trap 'rm -rf "$tmp"; [ "$finished" = 1 ] || { echo "repo-policy-test: stopped before the last case" >&2; exit 1; }' EXIT
fail() { echo "repo-policy-test: $*" >&2; exit 1; }

sha=0123456789abcdef0123456789abcdef01234567 # 40 hex characters
# fixture: a git repository with a workflow that follows the rules; prints its directory
fixture() {
  local dir
  dir=$(mktemp -d "$tmp/case.XXXXXX") # not a counter: this runs in a subshell
  mkdir -p "$dir/scripts" "$dir/.github/workflows"
  cp "$script" "$dir/scripts/repo-policy-check.sh"
  cat >"$dir/.github/workflows/ci.yml" <<YML
jobs:
  build:
    steps:
      - uses: actions/checkout@$sha # v4.1.0
      - uses: goreleaser/goreleaser-action@$sha # v6
        with:
          version: v2.4.8
YML
  git -C "$dir" init -q
  echo "$dir"
}
commit_and_run() { # <dir>; the exit status and output of the check on what is there
  git -C "$1" add -A -f
  (cd "$1" && scripts/repo-policy-check.sh >"$tmp/out" 2>&1)
}
# ok <name> <setup>: the check passes; bad <name> <setup> <message>: it fails and says why.
# <setup> is shell run in the fixture's directory.
ok() {
  local dir; dir=$(fixture)
  (cd "$dir" && eval "$2")
  commit_and_run "$dir" || { cat "$tmp/out"; fail "$1: a repository that follows the rules was rejected"; }
}
bad() {
  local dir; dir=$(fixture)
  (cd "$dir" && eval "$2")
  commit_and_run "$dir" && fail "$1: a repository that breaks the rules passed"
  grep -q -- "$3" "$tmp/out" || { cat "$tmp/out"; fail "$1: the message lacks '$3'"; }
  return 0
}
workflow() { printf '%s\n' "$1" >.github/workflows/ci.yml; } # replace the workflow

ok "clean" ":"
ok "a local action" "printf '      - uses: ./.github/actions/x\n' >>.github/workflows/ci.yml"
ok "a commented action" "printf '      # uses: actions/checkout@v4\n' >>.github/workflows/ci.yml"
ok "near-miss names and a key that ends in uses" "mkdir -p internal/tmpl internal/mytmp docs/myplans && echo x >internal/tmpl/x.go && echo x >internal/mytmp/x.go && echo x >docs/myplans/a.md && echo x >docs/plans.md && printf 'permissions:\n  statuses: write\n' >>.github/workflows/ci.yml"
ok "a workflow with CRLF line ends" "printf 'jobs:\r\n  b:\r\n    steps:\r\n      - uses: actions/checkout@$sha\r\n      - uses: goreleaser/goreleaser-action@$sha\r\n        with:\r\n          version: v2.4.8\r\n' >.github/workflows/ci.yml && printf '      - uses: goreleaser/goreleaser-action@$sha\n        with:\n          version: v2.4.8\n' >.github/workflows/lf.yml"
ok "a workflow without GoReleaser" "printf 'jobs:\n  b:\n    steps:\n      - uses: actions/checkout@$sha\n' >.github/workflows/ci.yml"
ok "the same GoReleaser version twice" "printf '      - uses: goreleaser/goreleaser-action@$sha\n        with:\n          version: \"v2.4.8\" # pinned\n' >>.github/workflows/ci.yml"

bad "CLAUDE.md" "echo x >CLAUDE.md" "AGENTS.md is the only agent instruction file"
bad "a nested CLAUDE.md" "mkdir -p docs && echo x >docs/CLAUDE.md" "docs/CLAUDE.md"
bad "CLAUDE.local.md" "echo x >CLAUDE.local.md" "CLAUDE.local.md"
bad "a nested CLAUDE.local.md" "mkdir -p docs && echo x >docs/CLAUDE.local.md" "docs/CLAUDE.local.md"
bad "an OpenSpec change" "mkdir -p openspec/changes/x && echo x >openspec/changes/x/p.md" "openspec/changes/ is not committed"
for f in tmp/a scratch/a plans/a docs/tmp/a docs/scratch/a docs/plans/a a.log a.tmp a.bak a.orig; do
  bad "scratch file $f" "mkdir -p $(dirname "$f") && echo x >$f" "scratch, plan or temporary file"
done

for ref in v4 main "${sha:0:7}" "${sha:0:39}" "${sha}0" "$(tr a-f A-F <<<"$sha")" "${sha//0/g}"; do
  bad "action pinned to $ref" "printf '      - uses: actions/setup-go@$ref\n' >>.github/workflows/ci.yml" "is not pinned to a commit SHA"
done
bad "an action without a ref" "printf '      - uses: actions/setup-go\n' >>.github/workflows/ci.yml" "is not pinned to a commit SHA"
bad "a Docker action" "printf '      - uses: docker://alpine:3\n' >>.github/workflows/ci.yml" "Docker actions are not used"
bad "a quoted key" "printf '      - \"uses\": actions/setup-go@v4\n' >>.github/workflows/ci.yml" "plain 'uses: <ref>' line"
bad "a flow mapping" "printf '      - { uses: actions/setup-go@v4 }\n' >>.github/workflows/ci.yml" "plain 'uses: <ref>' line"
bad "a .yaml workflow" "printf 'jobs:\n  b:\n    steps:\n      - uses: actions/setup-go@v4\n' >.github/workflows/b.yaml" "is not pinned to a commit SHA"
bad "a .yaml workflow with another GoReleaser version" "printf 'jobs:\n  b:\n    steps:\n      - uses: goreleaser/goreleaser-action@$sha\n        with:\n          version: v9.9.9\n' >.github/workflows/b.yaml" "different GoReleaser versions"
bad "a .yaml action" "mkdir -p .github/actions/x && printf 'runs:\n  steps:\n    - uses: actions/setup-go@v4\n' >.github/actions/x/action.yaml" "is not pinned to a commit SHA"
bad "a single-quoted key" "printf \"      - 'uses': actions/setup-go@v4\\n\" >>.github/workflows/ci.yml" "plain 'uses: <ref>' line"
bad "a space before the colon" "printf '      - uses : actions/setup-go@v4\n' >>.github/workflows/ci.yml" "plain 'uses: <ref>' line"
bad "an action in .github/actions" "mkdir -p .github/actions/x && printf 'runs:\n  steps:\n    - uses: actions/setup-go@v4\n' >.github/actions/x/action.yml" "is not pinned to a commit SHA"
bad "a workflow without actions" "printf 'jobs: {}\n' >.github/workflows/ci.yml" "read nothing"

bad "two GoReleaser versions" "printf '      - uses: goreleaser/goreleaser-action@$sha\n        with:\n          version: v2.5.0\n' >>.github/workflows/ci.yml" "different GoReleaser versions"
bad "GoReleaser without a version" "printf '      - uses: goreleaser/goreleaser-action@$sha\n' >>.github/workflows/ci.yml" "goreleaser-action without a version"
bad "GoReleaser without a version before one with" "printf '      - uses: goreleaser/goreleaser-action@$sha\n      - uses: goreleaser/goreleaser-action@$sha\n        with:\n          version: v2.4.8\n' >>.github/workflows/ci.yml" "goreleaser-action without a version"
bad "GoReleaser without a version before another tool's version" "printf '      - uses: goreleaser/goreleaser-action@$sha\n      - uses: actions/setup-go@$sha\n        with:\n          version: 1.22\n' >>.github/workflows/ci.yml" "goreleaser-action without a version"
bad "two GoReleaser steps without versions in a row" "printf '      - uses: goreleaser/goreleaser-action@$sha\n      - uses: goreleaser/goreleaser-action@$sha\n' >>.github/workflows/ci.yml" "goreleaser-action without a version"
bad "GoReleaser with an empty version" "printf '      - uses: goreleaser/goreleaser-action@$sha\n        with:\n          version:\n' >>.github/workflows/ci.yml" "goreleaser-action without a version"
finished=1
echo "repo-policy-test: ok"
