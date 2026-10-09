# Agents: Profile

[Specs](../../README.md) / [Agents](../README.md) / **Profile**

## Purpose

What an agent publishes about itself (tool, project, task, status, where it works and which pull requests it owns), how that information stays current, and when an agent counts as active.

## Requirements

### Requirement: Agents Publish What They Work On

The system SHALL keep for every agent a profile with its tool kind, project, current task, status, working directory, current branch, the pull requests it works on, a free-text description, and when it joined and was last updated.

#### Scenario: Profile after joining
- **WHEN** an agent joins with kind `claude-code`, project `example-app` and task `fix login timeout`
- **THEN** its profile shows those values, status `working`, its working directory and the branch checked out there

#### Scenario: Updating the task
- **WHEN** the agent sets a new task or status
- **THEN** the profile shows the new values and a new update time, and other fields stay unchanged

### Requirement: Branch Follows The Working Directory

The system SHALL derive an agent's branch from the git checkout that contains its working directory, without running git, and SHALL update it whenever the working directory changes.

#### Scenario: Declaring a worktree
- **WHEN** an agent declares `~/src/example-app/.worktrees/login-fix` as its directory
- **THEN** its branch becomes the branch checked out in that worktree

#### Scenario: Directory outside any checkout
- **WHEN** the working directory is not inside a git checkout
- **THEN** the branch is empty

### Requirement: Declared Pull Requests

The system SHALL let an agent add and remove pull request numbers on its profile, accepting both `123` and `#123`, and SHALL combine declared numbers with numbers found automatically when it reports the agent's pull requests.

#### Scenario: Adding and dropping
- **WHEN** an agent adds `#41` and `42`, then drops `41`
- **THEN** its declared pull requests are `42`

#### Scenario: Combined list
- **WHEN** an agent declared `42` and pull request `57` was found for its branch
- **THEN** its pull requests are reported as `42` and `57`, in numeric order

### Requirement: Active, Stale And Left Agents

The system SHALL treat an agent as active unless it has left or its profile has not been updated for 6 hours, and SHALL hide inactive agents from default listings while keeping their profiles.

#### Scenario: Silent agent
- **WHEN** an agent's profile was last updated 7 hours ago
- **THEN** it is missing from the default status listing and shown when all agents are requested

#### Scenario: Reading messages keeps an agent active
- **WHEN** an agent reads its unread messages
- **THEN** its profile update time is refreshed

### Requirement: Leaving Releases The Agent's Locks

The system SHALL mark an agent that leaves as `left` and SHALL release every lock it holds.

#### Scenario: Leaving with a held lock
- **WHEN** an agent holding the lock `merge-queue` leaves
- **THEN** its status is `left` and `merge-queue` is free
