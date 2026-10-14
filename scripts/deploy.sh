#!/usr/bin/env bash
# Deploys this checkout as the local agora: pulls, checks, builds, keeps a backup of the binary
# and the database, replaces the binary the hub's service runs atomically, restarts the service
# and verifies the result. Everything machine-specific comes from a variable; see
# docs/development/deploying.md.
set -euo pipefail
cd "$(dirname "$0")/.."

SERVICE=${SERVICE-agora-hub} # systemd user unit; empty: the hub is not restarted or checked
DB=${DB:-${AGORA_DB:-${XDG_STATE_HOME:-$HOME/.local/state}/agora/agora.db}}
WEB_URL=${WEB_URL-}       # checked for 200; empty: not checked
CHECK=${CHECK-check}      # make target run first; empty: none
WEB=${WEB-1}              # 1: build the web app into the binary (make build-web); 0: API only
PULL=${PULL-1}            # 1: git pull --ff-only first
BACKUP=${BACKUP-1}        # 1: back up the binary and the database
SKILL_DIRS=${SKILL_DIRS-} # space-separated: agora install skill --dir
COMMIT_LEN=12             # as agora version names a commit
WAIT_TRIES=20             # times WAIT_STEP: how long the service and the hub get to come up
WAIT_STEP=0.5

step() { printf '\n== %s\n' "$*"; }
die() { echo "deploy: $*" >&2; exit 1; }

# the binary the service runs, from its ExecStart: { path=/usr/local/bin/agora ; argv[]=… }
service_binary() {
  systemctl --user show -p ExecStart --value "$SERVICE" | sed -n 's/^{ path=\(.*\) ; argv\[\]=.*/\1/p'
}

step "preflight"
if [ -n "$SERVICE" ]; then
  command -v systemctl >/dev/null || die "systemctl is missing; set SERVICE= when the hub does not run as a systemd user unit"
  systemctl --user cat "$SERVICE" >/dev/null 2>&1 || die "no systemd user unit $SERVICE; set SERVICE= when the hub does not run as one"
fi
if [ -z "${BIN:-}" ]; then
  [ -n "$SERVICE" ] && BIN=$(service_binary)
  [ -n "${BIN:-}" ] || BIN=$(command -v agora || true)
  [ -n "$BIN" ] || die "cannot tell which binary to replace; set BIN"
fi
[ -L "$BIN" ] && die "$BIN is a symbolic link; set BIN to the file it points to"
if [ -n "$SERVICE" ]; then
  runs=$(service_binary)
  [ -n "$runs" ] || die "cannot read the binary from the ExecStart of $SERVICE; set BIN"
  [ "$runs" -ef "$BIN" ] || [ "$runs" = "$BIN" ] || die "$SERVICE runs $runs, not $BIN; set BIN to it"
fi
[ -z "$WEB_URL" ] || command -v curl >/dev/null || die "curl is needed to check WEB_URL"
[ "$BACKUP" != 1 ] || [ ! -f "$DB" ] || command -v sqlite3 >/dev/null || die "sqlite3 is needed to back up $DB; install it, or set BACKUP=0"
echo "binary: $BIN"

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
if [ "$WEB" = 1 ]; then
  make build-web
  [ -z "$(git status --porcelain)" ] || die "make build-web changed the working tree"
fi
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
bin_bak="" db_bak=""
if [ "$BACKUP" = 1 ]; then
  step "backup"
  if [ -f "$BIN" ]; then
    bin_bak="$BIN.bak-$ts"
    cp -p "$BIN" "$bin_bak"
    echo "binary: $bin_bak"
  fi
  if [ -f "$DB" ]; then
    db_bak="$DB.bak-$ts"
    # VACUUM INTO reads a consistent copy while the hub writes; the path is an SQL string
    sq="'" # a quote doubled the way bash 3.2, as on macOS, also does
    (umask 077 && sqlite3 "$DB" "VACUUM INTO '${db_bak//$sq/$sq$sq}'")
    echo "database: $db_bak"
  else
    echo "no database at $DB: not backed up (set DB to the hub's --db)"
  fi
fi

# how to get back to what ran before, from the backups actually taken
rollback() {
  local q
  echo "deploy: to go back to what ran before:" >&2
  [ -z "$SERVICE" ] || echo "  systemctl --user stop $(printf %q "$SERVICE")" >&2
  if [ -n "$db_bak" ]; then
    q=$(printf %q "$DB")
    echo "  cp $(printf %q "$db_bak") $q && rm -f $q-wal $q-shm" >&2
  fi
  if [ -n "$bin_bak" ]; then
    echo "  mv $(printf %q "$bin_bak") $(printf %q "$BIN")" >&2
  elif [ "$BACKUP" = 1 ]; then
    echo "  rm $(printf %q "$BIN")   # there was none before" >&2
  else
    echo "  (BACKUP=0: there are no backups to restore)" >&2
  fi
  [ -z "$SERVICE" ] || echo "  systemctl --user start $(printf %q "$SERVICE")" >&2
}

step "install"
mv -f "$new" "$BIN" # a rename within one directory: the old binary or the new one, never half
trap 'status=$?; [ "$status" = 0 ] || rollback; exit "$status"' EXIT
echo "$BIN"

if [ -n "$SERVICE" ]; then
  step "restart $SERVICE"
  systemctl --user restart "$SERVICE"
  for _ in $(seq "$WAIT_TRIES"); do
    systemctl --user is-active --quiet "$SERVICE" && break
    sleep "$WAIT_STEP"
  done
  systemctl --user is-active --quiet "$SERVICE" || die "$SERVICE is not active"
  pid=$(systemctl --user show -p MainPID --value "$SERVICE")
  if [ -e "/proc/$pid/exe" ]; then
    [ "/proc/$pid/exe" -ef "$BIN" ] || die "$SERVICE runs $(readlink "/proc/$pid/exe"), not $BIN"
  fi
  echo "$SERVICE is active and runs $BIN"
fi

step "verify"
installed=$("$BIN" version)
[ "$installed" = "$built" ] || die "the installed binary says '$installed', not '$built'"
echo "$installed"
if [ -n "$SERVICE" ]; then
  # the hub may take a moment to listen after the unit is active; the CLI finds its socket
  # through AGORA_SOCKET or XDG_RUNTIME_DIR, as for any agora command
  for _ in $(seq "$WAIT_TRIES"); do
    "$BIN" status >/dev/null 2>&1 && break
    sleep "$WAIT_STEP"
  done
  status=$("$BIN" status) || die "agora status fails"
  echo "agora status: ok"
  # what answers on the socket is the build just installed, not an older hub still running
  # only the first line is the hub's own; a later one can be an agent's text
  case "${status%%$'\n'*}" in
    "HUB "*) running=${status%%$'\n'*}; running=${running#HUB } ;;
    *) die "agora status does not show the running hub's version; check that $SERVICE restarted, and that AGORA_SOCKET or XDG_RUNTIME_DIR point at its socket" ;;
  esac
  [ "$running" = "$built" ] || die "the hub that answers is '$running', not '$built'; check that $SERVICE restarted, and that AGORA_SOCKET or XDG_RUNTIME_DIR point at its socket"
  echo "the running hub is $running"
fi
if [ -n "$WEB_URL" ]; then
  code=$(curl -s -o /dev/null -w '%{http_code}' "$WEB_URL" || true)
  [ "$code" = 200 ] || die "$WEB_URL answers ${code:-nothing}, not 200"
  echo "$WEB_URL: 200"
fi

for dir in $SKILL_DIRS; do
  "$BIN" install skill --dir "$dir"
done

step "deployed $head"
