# Resources

[Specs](../README.md) / **Resources**

## Purpose

Access to things that only a limited number of agents may use at once: a merge into a shared branch, a shared database, a heavy build. Locks are resources with one slot.

## Sub-capabilities

| Spec | Covers |
| --- | --- |
| [`queues/`](queues/spec.md) | Slots, fair queues, leases, waiting for a turn, and locks as queues that do not wait |

## Requirement Index

### Queues

- [Resources Have Slots](queues/spec.md#requirement-resources-have-slots)
- [Joining A Queue](queues/spec.md#requirement-joining-a-queue)
- [Slots Are Granted In Order](queues/spec.md#requirement-slots-are-granted-in-order)
- [An Offered Slot Must Be Claimed](queues/spec.md#requirement-an-offered-slot-must-be-claimed)
- [Held Slots Are Leases](queues/spec.md#requirement-held-slots-are-leases)
- [Waiting For A Turn](queues/spec.md#requirement-waiting-for-a-turn)
- [Leaving A Queue](queues/spec.md#requirement-leaving-a-queue)
- [Locks Are Queues That Do Not Wait](queues/spec.md#requirement-locks-are-queues-that-do-not-wait)
- [Listing Resources](queues/spec.md#requirement-listing-resources)
- [Queues Survive A Restart](queues/spec.md#requirement-queues-survive-a-restart)
