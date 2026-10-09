# Agents: Profile

[Specs](../../README.md) / [Agents](../README.md) / **Profile**

## Purpose

What an agent publishes about its work (tool, project, task, status, where it works and which pull requests it works on), how that stays current, and when an agent counts as active.

## Requirements

### Requirement: Agents Publish What They Work On

The system SHALL keep for every agent a profile with its tool kind, project, current task, status, working directory, current branch, the pull requests it works on, a free-text description, its join time and the time of its last update, and SHALL let the agent set any of these at join time or later, refusing an update that names no field to change. Joining sets the status to `working` unless another status is given.

#### Scenario: Profile after joining
- **WHEN** an agent joins as `builder` with kind `claude-code`, project `example-app` and task `fix login timeout`, from `~/src/example-app`
- **THEN** its profile shows those values, status `working`, that directory and the branch checked out there

#### Scenario: Updating the task
- **WHEN** the agent sets a new task
- **THEN** the profile shows the new task and a new update time, and every other field is unchanged

#### Scenario: Nothing to change
- **WHEN** an agent updates its profile without naming any field, only who it is
- **THEN** the update is refused with nothing to change, and the profile is unchanged

#### Scenario: Unknown agent
- **WHEN** a profile is updated for a name that never joined
- **THEN** the update is refused and says to join first

### Requirement: Branch Follows The Working Directory

The system SHALL derive an agent's branch from the git checkout that contains its working directory, SHALL work without git installed, and SHALL keep the previous branch while the same checkout is on a detached commit. Directories are absolute paths.

#### Scenario: Declaring a worktree
- **WHEN** an agent sets `~/src/example-app/.worktrees/login-fix` as its directory
- **THEN** its branch becomes the branch checked out in that worktree

#### Scenario: Directory outside any checkout
- **WHEN** the working directory is not inside a git checkout
- **THEN** the branch is empty

#### Scenario: Looking at an old commit
- **WHEN** the agent's checkout is switched to a commit by its hash
- **THEN** the profile keeps the branch it had

#### Scenario: Another checkout on a detached commit
- **WHEN** the agent moves to a different checkout that is on a detached commit
- **THEN** the profile shows that commit, not the previous checkout's branch

### Requirement: The Directory Follows The Session

The system SHALL update an agent's working directory and branch from the directory its connector reports for the agent's session, but SHALL keep a directory the agent set inside the session's directory.

#### Scenario: Session moves
- **WHEN** the session bound to `builder` reports the directory `~/src/other-app`
- **THEN** the profile's directory is `~/src/other-app` and its branch is the one checked out there

#### Scenario: Declared worktree inside the session's directory
- **WHEN** `builder` set `~/src/example-app/.worktrees/login-fix` and its session reports `~/src/example-app`
- **THEN** the profile keeps the worktree as its directory

### Requirement: Declared Pull Requests

The system SHALL let an agent add and remove pull request numbers on its profile, accepting both `123` and `#123`, and SHALL list them in numeric order.

#### Scenario: Adding and dropping
- **WHEN** an agent adds `#41` and `42`, then drops `41`
- **THEN** its pull requests are `42`

### Requirement: Active Agents

The system SHALL treat an agent as active unless it has left, while it has a live session or its profile was updated within the last 6 hours.

#### Scenario: Agent with a live session
- **WHEN** an agent last updated its profile 8 hours ago but its session is live
- **THEN** it is active

#### Scenario: Silent agent
- **WHEN** an agent has no live session and last updated its profile 7 hours ago
- **THEN** it is not active

### Requirement: Leaving

The system SHALL let an agent leave, marking it `left`, removing it from every resource queue and unbinding its name from every session in the same step, so the sessions stay registered but no longer act as the agent until one joins under the name again; SHALL not let the status `left` be set any other way; SHALL mark an agent `left` when its session ends and it is bound to no other live session; and SHALL mark it `working` again when a session still bound to it becomes active.

#### Scenario: Leaving with a held lock
- **WHEN** `builder` holds `example-app/merge` and leaves
- **THEN** its status is `left` and `example-app/merge` is free

#### Scenario: Leaving inside a live session
- **WHEN** `builder` leaves from its live session and that session then reports more activity
- **THEN** `builder` stays `left`, and commands from the session no longer act as `builder` until it joins again

#### Scenario: Session ends
- **WHEN** the only session bound to `builder` ends
- **THEN** `builder` is `left`

#### Scenario: Joining again
- **WHEN** a `left` agent joins again
- **THEN** its status is `working`

#### Scenario: Session resumed
- **WHEN** `builder` is `left` because its only session ended, and that session is resumed
- **THEN** the agent is `working` and active again

#### Scenario: Setting the status to left
- **WHEN** an agent sets its status to `left` without leaving
- **THEN** the change is refused and points to leaving
