#!/usr/bin/env bash
# Runs the working tree's scripts/deploy.sh on a clone of HEAD in a temporary directory, without a
# service: the binary is installed and names the clone's HEAD, a second run keeps backups of the
# binary and the database, a failed check after the install says how to go back, and a tree with
# changes, a symbolic link as the binary and a missing service are refused.
set -euo pipefail
cd "$(dirname "$0")/.."
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
fail() { echo "deploy-test: $*" >&2; exit 1; }

git clone -q . "$tmp/repo"
# the script under test is the working tree's, committed in the clone so its tree is clean
cp scripts/deploy.sh "$tmp/repo/scripts/deploy.sh"
git -C "$tmp/repo" -c user.name=test -c user.email=test@example.invalid -c commit.gpgsign=false -c core.hooksPath=/dev/null \
  commit -qam test --allow-empty --no-verify
head=$(git -C "$tmp/repo" rev-parse HEAD | cut -c1-12)
state="$tmp/it's state" # a quote and a space in the database's path
# WEB=0: the clone has no web dependencies installed; building the app is make build-web's own job
export BIN="$tmp/bin/agora" DB="$state/agora.db" SERVICE= CHECK= WEB=0 PULL=0 WEB_URL= SKILL_DIRS= BACKUP=1
run() { "$tmp/repo/scripts/deploy.sh" >"$tmp/out" 2>&1; }

run || { cat "$tmp/out"; fail "the first deploy failed"; }
[ "$("$BIN" version)" = "agora dev ($head)" ] || fail "installed: $("$BIN" version), want commit $head"
ls "$tmp/bin" | grep -q '\.new\.' && fail "a temporary binary was left"

if command -v sqlite3 >/dev/null; then
  mkdir -p "$state"
  sqlite3 "$DB" 'CREATE TABLE t (x); INSERT INTO t VALUES (1);'
elif [ -n "${CI-}" ]; then
  fail "sqlite3 is missing, so the database backup is not tested"
fi
run || { cat "$tmp/out"; fail "the second deploy failed"; }
ls "$tmp/bin"/agora.bak-* >/dev/null 2>&1 || fail "no backup of the binary"
if command -v sqlite3 >/dev/null; then
  backup=$(ls "$DB".bak-*)
  [ "$(sqlite3 "$backup" 'SELECT x FROM t')" = 1 ] || fail "the database backup lacks the data"
  [ "$(ls -l "$backup" | cut -c1-10)" = "-rw-------" ] || fail "the database backup is not owner-only"
fi
[ -z "$(git -C "$tmp/repo" status --porcelain)" ] || fail "the deploy left files in the checkout"

# a check after the install fails: the message says how to go back, with the backups taken
sleep 1 # a new backup name
WEB_URL=http://127.0.0.1:1/ run && fail "a deploy whose web app does not answer succeeded"
grep -q "to go back to what ran before" "$tmp/out" && grep -q "mv .*agora.bak-" "$tmp/out" || { cat "$tmp/out"; fail "no way back"; }
if command -v sqlite3 >/dev/null; then
  grep -q "rm -f .*-wal .*-shm" "$tmp/out" || { cat "$tmp/out"; fail "the way back does not restore the database"; }
fi

touch "$tmp/repo/stray"
run && fail "a working tree with an untracked file was deployed"
grep -q "has changes or untracked files" "$tmp/out" || { cat "$tmp/out"; fail "the refusal does not say why"; }
rm "$tmp/repo/stray"

ln -s "$BIN" "$tmp/link"
BIN="$tmp/link" run && fail "a symbolic link was replaced"
grep -q "is a symbolic link" "$tmp/out" || { cat "$tmp/out"; fail "the refusal of a link does not say why"; }

SERVICE=agora-deploy-test-none run && fail "a deploy to a missing service went on"
grep -q "set SERVICE=" "$tmp/out" || { cat "$tmp/out"; fail "the refusal of a missing service does not say why"; }
echo "deploy-test: ok"
