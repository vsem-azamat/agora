# Resource Queues

[Docs](../README.md) / [Architecture](README.md) / **Resource queues**

How `internal/queue` implements the behavior in [`openspec/specs/resources/queues/`](../../openspec/specs/resources/queues/spec.md).

## Tables

| Table | Holds |
| --- | --- |
| `resources` | One row per key: `slots`, and `slots_set` when the slots were set explicitly (such a resource is listed even when idle) |
| `entries` | One row per agent in a queue: `state` (`waiting`, `offered`, `held`), `seq` (order), `lease_ms`, `joined_at`, `expires_at` (lease end or claim deadline), `skips` (missed turns) |
| `removals` | Forced removals: when, which entry, and who forced it |

Times are unix milliseconds.

## Settling

Every operation on a resource first settles it inside its transaction, in this order:

1. Delete held entries whose lease has ended.
2. Offers past their claim deadline: remove the entry if this is its second miss; otherwise move it to the end of the queue (new `seq`) as `waiting` and count the miss.
3. Offer free slots (`slots` minus held and offered entries) to waiting entries in `seq` order, with a claim deadline of `ClaimWindow` (2 minutes).

Settling looks at one instant, so a long gap without any operation advances one step at a time; the hub's one-second sweep keeps queues moving between operations.

## Operations

- **Join** holds a slot at once only when one is free and nobody waits; otherwise it appends a `waiting` entry. Joining again updates the note, and with a lease given also `lease_ms` and, for a held entry, `expires_at` (now plus the new lease); the CLI sends a lease only when `--lease` is passed. Leases are 1 second to 7 days. With `no_wait` (locks), a new agent is inserted only if it can hold a slot at once; a holder's lease is renewed for the new duration; an agent that waits or is offered is refused without losing its place.
- **Claim** (used by waiting clients) turns an offer into a held slot and leaves other states unchanged. **Renew** does the same and also extends a held lease to now plus the entry's lease duration.
- **Release** deletes the `holder`'s entry (the acting agent's own when `holder` is empty) and settles, which offers the freed slot. Removing another agent's entry needs `force` and writes a `removals` row.
- **Wait** (in the hub) loops: subscribe to the change signal, claim, send the entry when its state or position changed, return once held.

Positions are not stored; they are counted from `seq` among waiting entries when a resource is loaded.
