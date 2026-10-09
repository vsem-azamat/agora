# CI Watch

[Specs](../README.md) / **CI Watch**

## Purpose

Following each agent's pull requests and telling the owner when CI finishes.

## Sub-capabilities

| Spec | Covers |
| --- | --- |
| [`pull-requests/`](pull-requests/spec.md) | Finding the open pull requests each agent works on from its branch, so owners can be found and told about CI without declaring anything by hand |
| [`notifications/`](notifications/spec.md) | Telling the owner of a pull request when its CI finishes, so no agent has to poll CI |

## Requirement Index

### Pull Requests

- [Pull Requests Are Found From The Agent's Branch](pull-requests/spec.md#requirement-pull-requests-are-found-from-the-agents-branch)
- [Shared Branches Own No Pull Request](pull-requests/spec.md#requirement-shared-branches-own-no-pull-request)
- [Earlier Pull Requests Stay Followed](pull-requests/spec.md#requirement-earlier-pull-requests-stay-followed)
- [Supported Hosts](pull-requests/spec.md#requirement-supported-hosts)

### Notifications

- [CI State Of A Pull Request](notifications/spec.md#requirement-ci-state-of-a-pull-request)
- [Owners Hear Once When CI Turns Green Or Red](notifications/spec.md#requirement-owners-hear-once-when-ci-turns-green-or-red)
- [Where CI Messages Go](notifications/spec.md#requirement-where-ci-messages-go)
