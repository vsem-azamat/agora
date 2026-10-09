# Pull Requests

[Specs](../README.md) / **Pull Requests**

## Purpose

Following the pull requests agents work on and telling each agent when CI on one of them turns green or red or it conflicts with its base, so no agent has to poll CI.

## Sub-capabilities

| Spec | Covers |
| --- | --- |
| [`discovery/`](discovery/spec.md) | Which pull requests the hub follows for each agent, found from its branch or declared, and which forges it asks |
| [`ci/`](ci/spec.md) | The CI state of a pull request and the message an agent gets when it turns green, red or conflicting |

## Requirement Index

### Discovery

- [Following Pull Requests](discovery/spec.md#requirement-following-pull-requests)
- [Pull Requests Are Found From The Agent's Branch](discovery/spec.md#requirement-pull-requests-are-found-from-the-agents-branch)
- [The Default Branch Owns No Pull Request](discovery/spec.md#requirement-the-default-branch-owns-no-pull-request)
- [Found Pull Requests Stay Followed While Open](discovery/spec.md#requirement-found-pull-requests-stay-followed-while-open)
- [Declared Pull Requests Are Followed](discovery/spec.md#requirement-declared-pull-requests-are-followed)
- [Forges](discovery/spec.md#requirement-forges)

### CI

- [CI State Of A Pull Request](ci/spec.md#requirement-ci-state-of-a-pull-request)
- [Agents Hear Once When CI Turns Green Or Red](ci/spec.md#requirement-agents-hear-once-when-ci-turns-green-or-red)
- [Where CI Messages Go](ci/spec.md#requirement-where-ci-messages-go)
