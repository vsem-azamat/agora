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
    [ "$FAKE_RESTART" = 1 ] || exit 0
    if [ -f "$FAKE_PID" ]; then
      kill "$(cat "$FAKE_PID")" 2>/dev/null || true
      while kill -0 "$(cat "$FAKE_PID")" 2>/dev/null; do sleep 0.1; done
    fi
    rm -f "$AGORA_SOCKET"
    "$BIN" hub --db "$DB" >/dev/null 2>&1 &
    echo $! >"$FAKE_PID" ;;
esac
STUB
chmod +x "$tmp/fake/systemctl"
export AGORA_SOCKET="$tmp/hub.sock" FAKE_PID="$tmp/hub.pid"
svc() { PATH="$tmp/fake:$PATH" SERVICE=agora-fake BACKUP=0 FAKE_RESTART="$1" run; }

svc 1 || { cat "$tmp/out"; fail "a deploy that restarts the hub failed"; }
grep -q "the running hub is agora .*($head)" "$tmp/out" || { cat "$tmp/out"; fail "the running hub's version is not reported"; }

git -C "$tmp/repo" -c user.name=test -c user.email=test@example.invalid -c commit.gpgsign=false -c core.hooksPath=/dev/null \
  commit -q --allow-empty -m next --no-verify
new=$(git -C "$tmp/repo" rev-parse HEAD | cut -c1-12)
svc 0 && fail "a deploy whose hub still runs the old build succeeded"
grep -q "the hub that answers is 'agora .*($head)', not 'agora .*($new)'" "$tmp/out" || { cat "$tmp/out"; fail "the stale hub is not named"; }
svc 1 || { cat "$tmp/out"; fail "the deploy that restarts the hub failed on the new commit"; }
grep -q "the running hub is agora .*($new)" "$tmp/out" || { cat "$tmp/out"; fail "the new hub's version is not reported"; }
echo "deploy-test: ok"
