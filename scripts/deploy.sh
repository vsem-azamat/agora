#!/usr/bin/env bash
# Deploys this checkout as the local agora: pulls, checks, builds, keeps a backup of the binary
# and the database, replaces the installed binary atomically, restarts the hub's service and
# verifies the result. Everything machine-specific comes from a variable; see
# docs/development/deploying.md.
set -euo pipefail
cd "$(dirname "$0")/.."

BIN=${BIN:-$HOME/.local/bin/agora}                                 # the installed binary
SERVICE=${SERVICE-agora-hub}                                        # systemd user unit; empty: no restart
DB=${DB:-${AGORA_DB:-${XDG_STATE_HOME:-$HOME/.local/state}/agora/agora.db}} # backed up when it exists
WEB_URL=${WEB_URL-}                                                 # checked for 200; empty: not checked
CHECK=${CHECK-check}                                                # make target run first; empty: none
PULL=${PULL-1}                                                      # 1: git pull --ff-only first
BACKUP=${BACKUP-1}                                                  # 1: back up the binary and the database
SKILL_DIRS=${SKILL_DIRS-}                                           # space-separated: agora install skill --dir
COMMIT_LEN=12                                                       # as agora version names a commit

step() { printf '\n== %s\n' "$*"; }
die() { echo "deploy: $*" >&2; exit 1; }

step "checkout"
[ -z "$(git status --porcelain)" ] || die "the working tree has changes or untracked files; commit or remove them first"
if [ "$PULL" = 1 ]; then
  git pull --ff-only
fi
head=$(git rev-parse HEAD | cut -c1-"$COMMIT_LEN")
echo "deploying $head"

if [ -n "$CHECK" ]; then
  step "make $CHECK"
  make "$CHECK"
  [ -z "$(git status --porcelain)" ] || die "make $CHECK changed the working tree"
fi

step "build"
mkdir -p "$(dirname "$BIN")"
new="$BIN.new.$$"
trap 'rm -f "$new"' EXIT
go build -o "$new" ./cmd/agora
built=$("$new" version)
case "$built" in
  *"($head)") echo "$built" ;;
  *) die "the build says '$built', not commit $head" ;;
esac

ts=$(date +%Y%m%d-%H%M%S)
if [ "$BACKUP" = 1 ]; then
  step "backup"
  if [ -f "$BIN" ]; then
    cp -p "$BIN" "$BIN.bak-$ts"
    echo "binary: $BIN.bak-$ts"
  fi
  if [ -f "$DB" ]; then
    command -v sqlite3 >/dev/null || die "sqlite3 is needed to back up $DB; install it, or set BACKUP=0"
    (umask 077 && sqlite3 "$DB" ".backup '$DB.bak-$ts'")
    echo "database: $DB.bak-$ts"
  fi
fi

step "install"
mv -f "$new" "$BIN" # a rename within one directory: the old binary or the new one, never half
trap - EXIT
echo "$BIN"
rollback="restore the backups with: mv '$BIN.bak-$ts' '$BIN'"

if [ -n "$SERVICE" ]; then
  step "restart $SERVICE"
  systemctl --user restart "$SERVICE"
  for _ in $(seq 20); do
    systemctl --user is-active --quiet "$SERVICE" && break
    sleep 0.5
  done
  systemctl --user is-active --quiet "$SERVICE" || die "$SERVICE is not active; $rollback, then restart it"
  echo "$SERVICE is active"
fi

step "verify"
installed=$("$BIN" version)
[ "$installed" = "$built" ] || die "the installed binary says '$installed', not '$built'; $rollback"
echo "$installed"
if [ -n "$SERVICE" ]; then
  # the hub may take a moment to listen after the unit is active
  for _ in $(seq 20); do
    "$BIN" status >/dev/null 2>&1 && break
    sleep 0.5
  done
  "$BIN" status >/dev/null || die "agora status fails; $rollback"
  echo "agora status: ok"
fi
if [ -n "$WEB_URL" ]; then
  code=$(curl -s -o /dev/null -w '%{http_code}' "$WEB_URL" || true)
  [ "$code" = 200 ] || die "$WEB_URL answers $code, not 200; $rollback"
  echo "$WEB_URL: 200"
fi

for dir in $SKILL_DIRS; do
  "$BIN" install skill --dir "$dir"
done

step "deployed $head"
