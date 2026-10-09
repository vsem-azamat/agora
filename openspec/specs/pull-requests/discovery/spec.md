# Pull Requests: Discovery

[Specs](../../README.md) / [Pull Requests](../README.md) / **Discovery**

## Purpose

Which pull requests the hub follows for each agent: those found from the branch the agent works on and those it declared, in the repository its working directory belongs to.

## Requirements

### Requirement: Following Pull Requests

The system SHALL, while the hub runs with `--watch-prs` (default on; `--watch-prs=false` or `AGORA_WATCH_PRS=false` turns it off), look up the pull requests of every repository that active agents work in, about 10 seconds after the hub starts and then every 2 minutes. A repository is identified by the `origin` remote of the git checkout that contains the agent's working directory, so worktrees and clones of one repository are looked up together with one request to its forge per round. A lookup SHALL not count as activity of the agent.

#### Scenario: Several worktrees of one repository
- **WHEN** three active agents work in different worktrees of `example-org/example-app`
- **THEN** each round asks the forge about that repository once

#### Scenario: Turned off
- **WHEN** the hub runs with `--watch-prs=false`
- **THEN** it makes no lookups and posts no CI messages

#### Scenario: Lookup is not activity
- **WHEN** a round finds a pull request for `builder`
- **THEN** the update time of its profile is unchanged

### Requirement: Pull Requests Are Found From The Agent's Branch

The system SHALL record on an agent's profile, as found pull requests separate from the ones it declared, the open pull requests whose head branch is the agent's current branch in the same repository (not a fork). The board overview and lookups by pull request number SHALL include found pull requests along with declared ones.

#### Scenario: Agent on a feature branch
- **WHEN** active agent `builder` works on branch `fix/login-timeout` and pull request 57 is open from that branch
- **THEN** after the next round its found pull requests are `57`, its declared pull requests are unchanged, and looking up `#57` lists `builder`

#### Scenario: Pull request from a fork
- **WHEN** pull request 58 is open from a fork's branch also named `fix/login-timeout`
- **THEN** it is not found for `builder`

### Requirement: The Default Branch Owns No Pull Request

The system SHALL not find pull requests by branch for an agent on the repository's default branch, as reported by the forge, or on an empty branch.

#### Scenario: Agent on the default branch
- **WHEN** the forge reports `dev` as the default branch, `builder` works on `dev`, and pull request 60 is open from `dev`
- **THEN** no pull request is found for `builder`

### Requirement: Found Pull Requests Stay Followed While Open

The system SHALL keep following a found pull request while it is open, whatever branch the agent is on now, and SHALL drop it from the profile once the forge reports it closed or merged.

#### Scenario: Agent moves to its next task
- **WHEN** `builder` switches from `fix/login-timeout` (pull request 57, still open) to `feat/export`
- **THEN** its found pull requests still include 57

#### Scenario: Pull request merged
- **WHEN** found pull request 57 is merged
- **THEN** after the next round it is no longer among the agent's pull requests and no CI message is posted for it

### Requirement: Declared Pull Requests Are Followed

The system SHALL follow every pull request an agent declared, in the repository it works in, while that pull request is open, whatever branch it was opened from.

#### Scenario: Declared pull request from another branch
- **WHEN** `builder` works on `main` and declared pull request 61, opened by someone else from `feat/report`
- **THEN** CI messages for 61 reach `builder`

### Requirement: Forges

The system SHALL choose the forge of a repository by the host of its `origin` remote URL, SHALL ask GitHub (`github.com`) through the `gh` command-line tool with the credentials it already has, without storing tokens, and SHALL skip repositories without an `origin` remote, on hosts it does not know, or on GitHub when `gh` is not installed, without reporting an error. A lookup SHALL time out after 30 seconds; a failed lookup SHALL be logged and SHALL not stop the other repositories, the round, or the hub.

#### Scenario: Unknown host
- **WHEN** an agent works in a repository whose `origin` is on `git.example.com`
- **THEN** no lookup is made for it and nothing is logged as an error

#### Scenario: Failed lookup
- **WHEN** the lookup for one repository fails
- **THEN** the failure is logged, the other repositories are still processed, and the next round tries again
