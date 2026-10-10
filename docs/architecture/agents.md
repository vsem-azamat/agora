# Agent Profiles

[Docs](../README.md) / [Architecture](README.md) / **Agent profiles**

How `internal/agents` implements [`openspec/specs/agents/profile/`](../../openspec/specs/agents/profile/spec.md) and [`lookup/`](../../openspec/specs/agents/lookup/spec.md).

## Storage

Profiles are columns of the `agents` table: `joined_at` from the start, and from migration `0003_profiles.sql` `kind`, `project`, `task`, `status`, `cwd` (absolute, symlinks resolved), `branch`, `prs` (declared pull requests, space-separated, ascending), `about`, `updated_at`. Pull requests found from the branch are rows of `pull_requests` (see [Pull requests and CI](pull-requests.md)). The migration marks names without a live session as `left`. The status `left` is set only by leaving, never by `agora set`. `agora join` registers the name through `SessionService.JoinName`, then sets the profile through `AgentService.UpdateProfile`: status `working` unless given, the current directory, and kind `claude-code` inside a Claude Code session.

## Branches

`internal/gitinfo` walks up from the directory to the first `.git`: a directory, or a worktree's `.git` file pointing at its git directory, and reads `HEAD`. A detached `HEAD` gives the first 12 characters of the commit; when the agent stays in the same checkout (same git directory), the profile keeps its previous branch instead. No `git` process is started. `gitinfo.Repo` reads the repository name the same way, following a worktree's `commondir` file to the main checkout; the session greeting uses it to suggest a project.

## Following the session

Every connector event except the end marks the `left` agent bound to the session as `working` again (a resumed session) and calls `FollowTx` inside the session's transaction: the profile takes the session's directory, unless the profile's directory lies inside it (a worktree the agent set with `agora set --cwd`). `updated_at` changes only when the directory or branch changed.

## Activity

An agent is active unless its status is `left`, while it has a session that has not ended or its profile was updated within 6 hours. The session state shown (`busy`, `idle`, `offline`) is the state of its live session, or `offline`.

## Leaving

Leaving (`sessions.Leave`, behind `agora leave`) and the end of an agent's last session mark it `left` (`agents.MarkLeftTx`) and remove it from every resource queue (`queue.ReleaseAgentTx`) in one transaction. Leaving also clears `sessions.agent` for every session bound to the name, ended ones included, so later events of a live session and a resumed session do not mark it `working` again; the session acts under no name until `agora join` binds one.

## Lookup

`Who` receives either a path (the CLI treats a query as a path only when it is written like one: `/…`, `~`, `~/…`, `.`, `./…`, `..`, `../…`, and makes it absolute) or a plain query; paths are compared after following symlinks:

| Query | Matches |
| --- | --- |
| path | agents whose directory is it, lies inside it, or contains it (the owner of a file or folder) |
| `57` or `#57` | agents with that pull request, declared or found |
| anything else | the agent's name, its exact branch, or a part of its branch longer than two characters |
