# Deploying

[Docs](../README.md) / [Development](README.md) / **Deploying**

`make deploy` installs the checkout it runs in as the machine's `agora` and restarts the hub. It runs `scripts/deploy.sh`, which stops at the first failure:

1. **Checkout.** It refuses a working tree with changes or untracked files, since Go would mark the build `-dirty`, then runs `git pull --ff-only`.
2. **Check.** It runs `make check` and refuses when the check changed the tree.
3. **Build.** It builds into a temporary file next to the installed binary, and refuses unless `agora version` of that build names `HEAD`.
4. **Backup.** It copies the installed binary to `<binary>.bak-<time>`, and backs up the database with `sqlite3 .backup` to `<database>.bak-<time>`, readable by its owner only, so a migration can be undone.
5. **Install.** It renames the new binary over the installed one, within one directory, so the binary is always either the old one or the new one.
6. **Restart.** It runs `systemctl --user restart <service>` and waits up to 10 seconds for the unit to be active.
7. **Verify.** It checks that the installed binary's `agora version` is the build's, that `agora status` reaches the hub, and that the web app answers 200 when `WEB_URL` is given. Then it reinstalls the agent skill into `SKILL_DIRS`.

When a step after the install fails, the message says how to restore the backups.

| Variable | Default | Meaning |
| --- | --- | --- |
| `BIN` | `~/.local/bin/agora` | The installed binary |
| `SERVICE` | `agora-hub` | The systemd user unit of the hub (see [Setup](../architecture/setup.md)); empty: no restart, and no `agora status` check |
| `DB` | `$AGORA_DB`, else `$XDG_STATE_HOME/agora/agora.db`, else `~/.local/state/agora/agora.db` | The hub's database, backed up when it exists |
| `WEB_URL` | empty | The web app's address, checked for 200; empty: not checked |
| `CHECK` | `check` | The make target run before building; empty: none |
| `PULL` | `1` | `0`: deploy the checkout as it is, without pulling |
| `BACKUP` | `1` | `0`: no backups |
| `SKILL_DIRS` | empty | Directories, separated by spaces, to reinstall the agent skill into |

For example:

```sh
make deploy WEB_URL=http://127.0.0.1:8484/ SKILL_DIRS="$HOME/.claude/skills $HOME/.agents/skills"
```

Backups are kept; remove old ones by hand. `make test-deploy`, part of `make test`, runs the script on a clone of `HEAD` into a temporary directory without a service. It checks the installed version, the backups and the refusal of a tree with an untracked file.
