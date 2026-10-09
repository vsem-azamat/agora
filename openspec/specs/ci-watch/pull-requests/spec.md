# CI Watch: Pull Requests

[Specs](../../README.md) / [CI Watch](../README.md) / **Pull Requests**

## Purpose

Finding the open pull requests each agent works on from its branch, so owners can be found and told about CI without declaring anything by hand.

## Requirements

### Requirement: Pull Requests Are Found From The Agent's Branch

The system SHALL every 2 minutes look up the open pull requests of each repository that active agents work in, SHALL record for each agent the open pull requests whose source branch is its current branch, and SHALL not count this lookup as activity of the agent.

#### Scenario: Agent on a feature branch
- **WHEN** an active agent works on branch `fix/login-timeout` and pull request 57 is open from that branch
- **THEN** within 2 minutes its found pull requests include 57
- **AND** its profile update time is unchanged

#### Scenario: Several worktrees of one repository
- **WHEN** three agents work in different worktrees of the same repository
- **THEN** the repository's pull requests are looked up once for all three

### Requirement: Shared Branches Own No Pull Request

The system SHALL skip agents whose branch is empty or a shared branch (`main`, `master`, `dev`, `develop`, `staging`) when finding pull requests.

#### Scenario: Agent on main
- **WHEN** an agent works on `main`, which is the source branch of no pull request
- **THEN** no pull request is found for it

### Requirement: Earlier Pull Requests Stay Followed

The system SHALL keep following a pull request found earlier or declared by the agent while it stays open, even after the agent has moved to another branch.

#### Scenario: Agent moves to its next task
- **WHEN** an agent switches from `fix/login-timeout` (pull request 57, still open) to `feat/export`
- **THEN** its pull requests include 57 until 57 is closed or merged

### Requirement: Supported Hosts

The system SHALL look up pull requests for repositories whose `origin` remote is on GitHub or Azure DevOps, SHALL skip repositories on other hosts or without a remote, and SHALL skip a repository for the current round when its lookup fails.

#### Scenario: Unsupported host
- **WHEN** an agent works in a repository hosted elsewhere
- **THEN** no pull request lookup is made for it and nothing is reported as an error

#### Scenario: Failed lookup
- **WHEN** the lookup for one repository fails
- **THEN** the failure is logged, the other repositories are still processed, and the next round tries again
