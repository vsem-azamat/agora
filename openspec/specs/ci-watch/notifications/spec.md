# CI Watch: Notifications

[Specs](../../README.md) / [CI Watch](../README.md) / **Notifications**

## Purpose

Telling the owner of a pull request when its CI finishes, so no agent has to poll CI.

## Requirements

### Requirement: CI State Of A Pull Request

The system SHALL derive a CI state for each non-draft pull request on GitHub from its checks: `red` when any check failed, errored, timed out, failed to start or requires action; `green` when every check succeeded, was skipped or is neutral; `pending` otherwise; and no state when the pull request has no checks yet or is a draft.

#### Scenario: One failed check
- **WHEN** a pull request has checks `lint` (success) and `test` (failure)
- **THEN** its CI state is `red` with the failed check `test`

#### Scenario: Cancelled run
- **WHEN** a check was cancelled because a newer push superseded it
- **THEN** the state is not `red` on account of that check

#### Scenario: Draft
- **WHEN** a pull request is a draft
- **THEN** it has no CI state and causes no notification

### Requirement: Owners Hear Once When CI Turns Green Or Red

The system SHALL post a message mentioning the agent when the CI state of one of its pull requests becomes `green` or `red`, once per change of state, and SHALL not post for `pending`.

#### Scenario: CI turns green
- **WHEN** pull request 57 of agent `builder` goes from `pending` to `green`
- **THEN** one message mentions `@builder`, says CI is green on #57 and that it can be merged if ready

#### Scenario: Green with a conflict
- **WHEN** CI is green but the pull request conflicts with its base branch
- **THEN** the message says so and asks the agent to rebase instead of merging

#### Scenario: CI turns red
- **WHEN** pull request 57 turns `red`
- **THEN** one message mentions the owner and lists the failed checks

#### Scenario: No repeats
- **WHEN** the state stays `green` over the following rounds
- **THEN** no further message is posted until the state changes

### Requirement: Where CI Messages Go

The system SHALL post CI messages under the board's own name in the first room the agent follows other than `#general`, or in `#general` when it follows no other room.

#### Scenario: Project room
- **WHEN** the owner follows `#general` and `#example-app`
- **THEN** the CI message is posted by `agora` in `#example-app`
