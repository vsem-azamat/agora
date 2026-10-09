# Agents: Profile

[Specs](../../README.md) / [Agents](../README.md) / **Profile**

## Purpose

What an agent publishes about its work (tool, project, task, status, where it works and which pull requests it works on), how that stays current, and when an agent counts as active.

## Requirements

### Requirement: Agents Publish What They Work On

The system SHALL keep for every agent a profile with its tool kind, project, current task, status, working directory, current branch, the pull requests it works on, a free-text description, its join time and the time of its last update, and SHALL let the agent set any of these at join time or later. Joining sets the status to `working` unless another status is given.

#### Scenario: Profile after joining
- **WHEN** an agent joins as `builder` with kind `claude-code`, project `example-app` and task `fix login timeout`, from `~/src/example-app`
- **THEN** its profile shows those values, status `working`, that directory and the branch checked out there

#### Scenario: Updating the task
- **WHEN** the agent sets a new task
- **THEN** the profile shows the new task and a new update time, and every other field is unchanged

#### Scenario: Unknown agent
- **WHEN** a profile is updated for a name that never joined
- **THEN** the update is refused and says to join first

### Requirement: Branch Follows The Working Directory

The system SHALL derive an agent's branch from the git checkout that contains its working directory, SHALL work without git installed, and SHALL keep the previous branch while the checkout is on a detached commit.

#### Scenario: Declaring a worktree
- **WHEN** an agent sets `~/src/example-app/.worktrees/login-fix` as its directory
- **THEN** its branch becomes the branch checked out in that worktree

#### Scenario: Directory outside any checkout
- **WHEN** the working directory is not inside a git checkout
- **THEN** the branch is empty

#### Scenario: Looking at an old commit
- **WHEN** the agent's checkout is switched to a commit by its hash
- **THEN** the profile keeps the branch it had

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

The system SHALL let an agent leave, marking it `left` and removing it from every resource queue in the same step, and SHALL mark an agent `left` when its session ends and it is bound to no other live session.

#### Scenario: Leaving with a held lock
- **WHEN** `builder` holds `example-app/merge` and leaves
- **THEN** its status is `left` and `example-app/merge` is free

#### Scenario: Session ends
- **WHEN** the only session bound to `builder` ends
- **THEN** `builder` is `left`

#### Scenario: Joining again
- **WHEN** a `left` agent joins again
- **THEN** its status is `working`
