# Agent Profiles

[Docs](../README.md) / [Architecture](README.md) / **Agent profiles**

How `internal/agents` implements [`openspec/specs/agents/profile/`](../../openspec/specs/agents/profile/spec.md) and [`lookup/`](../../openspec/specs/agents/lookup/spec.md).

## Storage

Profiles are columns of the `agents` table (migration `0003_profiles.sql`): `kind`, `project`, `task`, `status`, `cwd`, `branch`, `prs` (declared pull requests, space-separated, ascending), `about`, `joined_at`, `updated_at`. `agora join` registers the name through `SessionService.JoinName`, then sets the profile through `AgentService.UpdateProfile`: status `working` unless given, the current directory, and kind `claude-code` inside a Claude Code session.

## Branches

`internal/gitinfo` walks up from the directory to the first `.git`: a directory, or a worktree's `.git` file pointing at its git directory, and reads `HEAD`. A detached `HEAD` gives the first 12 characters of the commit; the profile then keeps its previous branch. No `git` process is started.

## Following the session

Every connector event except the end calls `FollowTx` inside the session's transaction: the profile takes the session's directory, unless the profile's directory lies inside it (a worktree the agent set with `agora set --cwd`). `updated_at` changes only when the directory or branch changed.

## Activity

An agent is active unless its status is `left`, while it has a session that has not ended or its profile was updated within 6 hours. The session state shown (`busy`, `idle`, `offline`) is the state of its live session, or `offline`.

## Leaving

`Leave` and the end of an agent's last session mark it `left` and remove it from every resource queue in one transaction.

## Lookup

`Who` receives either an absolute directory (the CLI resolves anything written like a path, or an existing directory, before sending it) or a plain query:

| Query | Matches |
| --- | --- |
| directory | agents whose directory is it or lies inside it, or contains it |
| `57` or `#57` | agents with that declared pull request |
| anything else | the agent's name, its exact branch, or a part of its branch longer than two characters |
