#!/usr/bin/env bash
# Runs scripts/deploy.sh on a clone of HEAD into a temporary directory, without a service: the
# binary is installed and reports HEAD, a second run keeps backups of the binary and the database,
# and a working tree with changes is refused.
set -euo pipefail
cd "$(dirname "$0")/.."
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
fail() { echo "deploy-test: $*" >&2; exit 1; }

git clone -q . "$tmp/repo"
head=$(git -C "$tmp/repo" rev-parse HEAD | cut -c1-12)
export BIN="$tmp/bin/agora" DB="$tmp/state/agora.db" SERVICE= CHECK= PULL=0 WEB_URL= SKILL_DIRS=
run() { "$tmp/repo/scripts/deploy.sh" >"$tmp/out" 2>&1; }

run || { cat "$tmp/out"; fail "the first deploy failed"; }
[ "$("$BIN" version)" = "agora dev ($head)" ] || fail "installed: $("$BIN" version), want commit $head"
ls "$tmp/bin" | grep -q '\.new\.' && fail "a temporary binary was left"

if command -v sqlite3 >/dev/null; then
  mkdir -p "$tmp/state"
  sqlite3 "$DB" 'CREATE TABLE t (x); INSERT INTO t VALUES (1);'
fi
run || { cat "$tmp/out"; fail "the second deploy failed"; }
ls "$tmp/bin"/agora.bak-* >/dev/null 2>&1 || fail "no backup of the binary"
if command -v sqlite3 >/dev/null; then
  backup=$(ls "$DB".bak-*)
  [ "$(sqlite3 "$backup" 'SELECT x FROM t')" = 1 ] || fail "the database backup lacks the data"
  [ "$(ls -l "$backup" | cut -c1-10)" = "-rw-------" ] || fail "the database backup is not owner-only"
fi

touch "$tmp/repo/stray"
run && fail "a working tree with an untracked file was deployed"
grep -q "has changes or untracked files" "$tmp/out" || { cat "$tmp/out"; fail "the refusal does not say why"; }
echo "deploy-test: ok"
