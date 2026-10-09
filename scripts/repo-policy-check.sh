#!/usr/bin/env bash
# Rejects tracked files that the repository rules forbid: tool-specific agent instruction
# files, OpenSpec change folders, and scratch or temporary output.
set -euo pipefail
cd "$(dirname "$0")/.."
fail=0
err() { echo "repo-policy: $*" >&2; fail=1; }

while IFS= read -r path; do
  case "$path" in
    CLAUDE.md | */CLAUDE.md | CLAUDE.local.md | */CLAUDE.local.md)
      err "$path: AGENTS.md is the only agent instruction file" ;;
    openspec/changes/*)
      err "$path: specs are edited in place; openspec/changes/ is not committed" ;;
    tmp/* | */tmp/* | scratch/* | */scratch/* | plans/* | */plans/* | *.log | *.tmp | *.bak | *.orig)
      err "$path: scratch, plan or temporary file" ;;
  esac
done < <(git ls-files)

[ "$fail" = 0 ] && echo "repo-policy: ok"
exit "$fail"
