#!/usr/bin/env bash
# Runs the working tree's scripts/deploy.sh on a clone of HEAD in a temporary directory, without a
# service: the binary is installed and names the clone's HEAD, a second run keeps backups of the
# binary and the database, a failed check after the install says how to go back, and a tree with
# changes, a symbolic link as the binary and a missing service are refused, and a restart that
# leaves the old hub running is caught by the version the hub reports.
set -euo pipefail
cd "$(dirname "$0")/.."
tmp=$(mktemp -d)
cleanup() {
  [ ! -f "$tmp/hub.pid" ] || kill "$(cat "$tmp/hub.pid")" 2>/dev/null || true
  rm -rf "$tmp"
}
trap cleanup EXIT
fail() { echo "deploy-test: $*" >&2; [ ! -f "$tmp/hub.log" ] || { echo "the hub started by the test said:" >&2; cat "$tmp/hub.log" >&2; }; exit 1; }

git clone -q . "$tmp/repo"
# the script under test is the working tree's, committed in the clone so its tree is clean
cp scripts/deploy.sh "$tmp/repo/scripts/deploy.sh"
git -C "$tmp/repo" -c user.name=test -c user.email=test@example.invalid -c commit.gpgsign=false -c core.hooksPath=/dev/null \
  commit -qam test --allow-empty --no-verify
head=$(git -C "$tmp/repo" rev-parse HEAD | cut -c1-12)
state="$tmp/it's state" # a quote and a space in the database's path
# WEB=0: the clone has no web dependencies installed; building the app is make build-web's own job
export BIN="$tmp/bin/agora" DB="$state/agora.db" SERVICE= CHECK= WEB=0 PULL=0 WEB_URL= SKILL_DIRS= BACKUP=1 RESTART=
unset AGORA_SOCKET AGORA_DB # the environment of a make deploy must not reach the hub the test starts
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

# a service whose restart starts the hub from the installed binary, as systemd would; a stub
# systemctl, since the test has no systemd. FAKE_RESTART=0: the restart leaves the old hub running.
mkdir -p "$tmp/fake" "$state"
cat >"$tmp/fake/systemctl" <<'STUB'
#!/usr/bin/env bash
shift # --user
case "$1" in
  cat | is-active) exit 0 ;;
  show)
    case "$3" in
      ExecStart) echo "{ path=$BIN ; argv[]=$BIN hub ; ignore_errors=no }" ;;
      MainPID) echo 0 ;;
    esac ;;
  restart)
    echo "systemctl restart" >>"$SYSTEMCTL_LOG"
    [ "$FAKE_RESTART" = 1 ] || exit 0
    if [ -f "$FAKE_PID" ]; then
      kill "$(cat "$FAKE_PID")" 2>/dev/null || true
      while kill -0 "$(cat "$FAKE_PID")" 2>/dev/null; do sleep 0.1; done
    fi
    rm -f "$AGORA_SOCKET"
    env -u AGORA_WEB -u AGORA_WEB_AS -u AGORA_JOIN -u AGORA_WAKE_COMMAND -u AGORA_WAKE_TEXT -u AGORA_TERMINAL \
      -u AGORA_BRIDGE_POLICY -u AGORA_BRIDGE_DELAY -u AGORA_DB "$BIN" hub --db "$DB" --watch-prs=false >>"$HUB_LOG" 2>&1 &
    echo $! >"$FAKE_PID" ;;
esac
STUB
chmod +x "$tmp/fake/systemctl"
export SYSTEMCTL_LOG="$tmp/systemctl.log" HUB_LOG="$tmp/hub.log" AGORA_SOCKET="$tmp/hub.sock" FAKE_PID="$tmp/hub.pid"
svc() { PATH="$tmp/fake:$PATH" SERVICE=agora-fake BACKUP=0 FAKE_RESTART="$1" run; }

svc 1 || { cat "$tmp/out"; fail "a deploy that restarts the hub failed"; }
grep -q "the running hub is agora .*($head)" "$tmp/out" || { cat "$tmp/out"; fail "the running hub's version is not reported"; }

git -C "$tmp/repo" -c user.name=test -c user.email=test@example.invalid -c commit.gpgsign=false -c core.hooksPath=/dev/null \
  commit -q --allow-empty -m next --no-verify
new=$(git -C "$tmp/repo" rev-parse HEAD | cut -c1-12)
# agents whose task has a line that looks like the hub's own must not decide the check
AGORA_SESSION=spoof-a CLAUDE_CODE_SESSION_ID= AGORA_NAME= "$BIN" join spoof-a --project p --task "$(printf 'x\nHUB agora dev (%s)' "$new")" >/dev/null \
  || fail "cannot join the stale hub"
AGORA_SESSION=spoof-b CLAUDE_CODE_SESSION_ID= AGORA_NAME= "$BIN" join spoof-b --project p --task "$(printf 'x\nHUB agora v9 (deadbeef)')" >/dev/null \
  || fail "cannot join the stale hub"
svc 0 && fail "a deploy whose hub still runs the old build succeeded"
grep -q "the hub that answers is 'agora .*($head)', not 'agora .*($new)'" "$tmp/out" || { cat "$tmp/out"; fail "the stale hub is not named"; }
svc 1 || { cat "$tmp/out"; fail "the deploy that restarts the hub failed on the new commit"; }
grep -q "the running hub is agora .*($new)" "$tmp/out" || { cat "$tmp/out"; fail "the new hub's version is not reported"; }
# RESTART replaces systemctl: the command runs after the install and sees BIN and DB, the hub it
# starts is the one checked, a command that leaves the old hub serving fails the deploy, and the
# way back says to run it again. Without a service.
mkdir -p "$tmp/hook" "$tmp/xdg/agora"
cat >"$tmp/hook/restart.sh" <<'HOOK'
#!/usr/bin/env bash
echo "restarted with $BIN $DB" >>"$HOOK_LOG"
[ "$FAKE_RESTART" = 1 ] || exit 0
if [ -f "$FAKE_PID" ]; then
  kill "$(cat "$FAKE_PID")" 2>/dev/null || true
  while kill -0 "$(cat "$FAKE_PID")" 2>/dev/null; do sleep 0.1; done
fi
rm -f "$AGORA_SOCKET"
env -u AGORA_WEB -u AGORA_WEB_AS -u AGORA_JOIN -u AGORA_WAKE_COMMAND -u AGORA_WAKE_TEXT -u AGORA_TERMINAL \
  -u AGORA_BRIDGE_POLICY -u AGORA_BRIDGE_DELAY -u AGORA_DB "$BIN" hub --db "$DB" --watch-prs=false >>"$HUB_LOG" 2>&1 &
echo $! >"$FAKE_PID"
HOOK
chmod +x "$tmp/hook/restart.sh"
export HOOK_LOG="$tmp/hook.log"
# BIN and DB are not given: the script finds them, as it does for a user, and passes them on
hook() ( unset BIN DB; export PATH="$tmp/bin:$PATH" XDG_STATE_HOME="$tmp/xdg"; RESTART="$tmp/hook/restart.sh" BACKUP=0 FAKE_RESTART="$1" run )
# the hub that serves before the command runs is another build, so only the command can change it
git -C "$tmp/repo" -c user.name=test -c user.email=test@example.invalid -c commit.gpgsign=false -c core.hooksPath=/dev/null \
  commit -q --allow-empty -m later --no-verify
later=$(git -C "$tmp/repo" rev-parse HEAD | cut -c1-12)
hook 1 || { cat "$tmp/out"; fail "a deploy with a restart command failed"; }
grep -q "restarted with $BIN $tmp/xdg/agora/agora.db" "$HOOK_LOG" || fail "the restart command did not run with BIN and DB"
grep -q "the running hub is agora .*($later)" "$tmp/out" || { cat "$tmp/out"; fail "the hub started by the command was not checked"; }
git -C "$tmp/repo" -c user.name=test -c user.email=test@example.invalid -c commit.gpgsign=false -c core.hooksPath=/dev/null \
  commit -q --allow-empty -m last --no-verify
last=$(git -C "$tmp/repo" rev-parse HEAD | cut -c1-12)
hook 0 && fail "a restart command that leaves the old hub serving succeeded"
grep -q "the hub that answers is 'agora .*($later)', not 'agora .*($last)'" "$tmp/out" || { cat "$tmp/out"; fail "the old hub is not named"; }
# with a service as well, the command restarts the hub, not systemctl
: >"$SYSTEMCTL_LOG"
runs=$(wc -l <"$HOOK_LOG")
PATH="$tmp/fake:$PATH" SERVICE=agora-fake RESTART="$tmp/hook/restart.sh" BACKUP=0 FAKE_RESTART=1 run || { cat "$tmp/out"; fail "a deploy with a service and a restart command failed"; }
[ ! -s "$SYSTEMCTL_LOG" ] || fail "systemctl restarted the service although RESTART is set"
[ "$(wc -l <"$HOOK_LOG")" -eq $((runs + 1)) ] || fail "the restart command did not run next to a service"
sleep 1 # a new backup name
RESTART=false BACKUP=1 run && fail "a deploy whose restart command fails succeeded"
grep -q "the restart command failed" "$tmp/out" && grep -q "^  BIN=.* DB=.* bash -c false\$" "$tmp/out" || { cat "$tmp/out"; fail "a failed restart command does not say how to go back"; }
echo "deploy-test: ok"
