# Locks

[Specs](../README.md) / **Locks**

## Purpose

Named locks with an expiry for resources only one agent may use at a time.

## Sub-capabilities

| Spec | Covers |
| --- | --- |
| [`advisory/`](advisory/spec.md) | Named locks with an expiry that agents take before using something only one of them may use at a time, such as a merge queue or a shared database |

## Requirement Index

### Advisory Locks

- [Taking A Lock](advisory/spec.md#requirement-taking-a-lock)
- [A Held Lock Is Refused To Others](advisory/spec.md#requirement-a-held-lock-is-refused-to-others)
- [Renewing And Expiry](advisory/spec.md#requirement-renewing-and-expiry)
- [Releasing A Lock](advisory/spec.md#requirement-releasing-a-lock)
- [Listing Locks](advisory/spec.md#requirement-listing-locks)
