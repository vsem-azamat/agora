# Deploying

[Docs](../README.md) / [Development](README.md) / **Deploying**

`make deploy` installs the checkout it runs in as the machine's `agora` and restarts the hub. It runs `scripts/deploy.sh`, which stops at the first failure:

1. **Preflight.**
   - It finds the binary to replace: `BIN`, else the binary the service's `ExecStart` runs, else `agora` on `PATH`.
   - It refuses a symbolic link, since renaming over it would leave its target unchanged.
   - It refuses a service that does not exist or that runs another binary.
   - It refuses when `curl` (for `WEB_URL`) or `sqlite3` (for the database backup) is missing.
2. **Checkout.** It refuses a working tree with changes or untracked files, since Go would mark the build `-dirty`, then runs `git pull --ff-only`. Without an upstream, set `PULL=0`.
3. **Check.** It runs `make check` and refuses when the check changed the tree.
4. **Build.** It runs `make build-web`, so the binary embeds the web app (`internal/web/dist/`, which git ignores), and refuses when that changed the tree. It then builds into a temporary file next to the binary, and refuses unless `agora version` of the build names `HEAD`.
5. **Backup.**
   - It copies the binary to `<binary>.bak-<time>`.
   - It copies the database with `VACUUM INTO` to `<database>.bak-<time>`, readable by its owner only, so a migration can be undone. The copy is consistent while the hub writes.
   - It says so when there is no database at `DB`.
   - After the deploy succeeded, it removes the oldest backups it made, keeping `KEEP_BACKUPS` of the binary and of the database each: this deploy's own and the newest others by name (a name is the local time, so after the clock went back the newest name may not be the newest backup, and this deploy's backup is never removed). A file whose name is not `<file>.bak-<date>-<time>` is never removed.
6. **Install.** It renames the new binary over the old one within one directory, so the binary is always either the old one or the new one.
7. **Restart.** With `RESTART` set it runs that command with `bash -c`, with `BIN` and `DB` in its environment, and stops when it fails. Else it runs `systemctl --user restart <service>`. With a service it then waits up to 10 seconds for the unit to be active, and checks that the running process is the new binary.
8. **Verify.**
   - It checks that the installed binary's `agora version` is the build's.
   - When the hub was restarted (`SERVICE` or `RESTART`), it checks that `agora status` reaches the hub. Like every `agora` command, it finds the socket through `AGORA_SOCKET` or `XDG_RUNTIME_DIR`, so they must match what the service or the command uses.
   - It checks that the version `agora status` shows for the running hub (its `HUB` line, which the hub reports itself) is the build's, so a hub still running an older build fails the deploy even when the process looks right.
   - When `WEB_URL` is given, it checks that the web app answers 200; a binary without the web app answers 503 there.
   - It reinstalls the agent skill into `SKILL_DIRS`.

When anything fails after the install, the script prints the commands that go back to what ran before, from the backups it took:
1. stop the service, or the hub the way you do when `RESTART` is set;
2. restore the database and remove its `-wal` and `-shm` files, since an older binary refuses a database migrated by a newer one;
3. restore the binary, or remove it when there was none before;
4. start the service, or run `RESTART` again, printed with the `BIN` and `DB` it needs.

| Variable | Default | Meaning |
| --- | --- | --- |
| `BIN` | the path in the service's `ExecStart`, else `agora` on `PATH` | The binary to replace |
| `SERVICE` | `agora-hub` | The systemd user unit of the hub (see [Setup](../architecture/setup.md)); empty when the hub does not run as one: then no unit is restarted or checked |
| `RESTART` | empty | A shell command that restarts the hub, in place of `systemctl`; for a hub that runs under launchd, or is started by hand. Set `SERVICE=` with it when there is no systemd unit |
| `DB` | `$AGORA_DB`, else `$XDG_STATE_HOME/agora/agora.db`, else `~/.local/state/agora/agora.db` | The hub's database, backed up when it exists; set it to the service's `--db` when the service has one |
| `WEB_URL` | empty | The web app's address, checked for 200; empty: not checked |
| `CHECK` | `check` | The make target run before building; empty: none |
| `WEB` | `1` | `0`: build without the web app, which serves only the API |
| `PULL` | `1` | `0`: deploy the checkout as it is, without pulling |
| `BACKUP` | `1` | `0`: no backups, and nothing to go back to |
| `KEEP_BACKUPS` | `5` | How many backups of the binary, and of the database, stay after a successful deploy; `0`: all of them. Only with `BACKUP=1` |
| `SKILL_DIRS` | empty | Directories, separated by spaces, to reinstall the agent skill into |

For a hub under launchd on macOS, which the script does not drive itself:

```sh
make deploy SERVICE= RESTART='launchctl kickstart -k "gui/$(id -u)/<label>"'
```

For example:

```sh
make deploy WEB_URL=http://127.0.0.1:8484/ SKILL_DIRS="$HOME/.claude/skills $HOME/.agents/skills"
```

A failed deploy removes nothing, so the backups its way back names stay. `make test-deploy`, part of `make test`, commits the working tree's script into a clone of `HEAD` in a temporary directory and runs it there without a service and with `WEB=0`, since the clone has no web dependencies installed. It checks:
- the installed version;
- the backups and their pruning to `KEEP_BACKUPS`, with a quote and a space in the database's path;
- the way back after a failed check;
- a restart that starts the new build and one that leaves the old hub running, through a stub `systemctl` and through a `RESTART` command, and the way back after a command that fails;
- the refusal of a tree with an untracked file, of a symbolic link and of a missing service.
