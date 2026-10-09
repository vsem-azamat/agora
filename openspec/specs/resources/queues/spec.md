# Resources: Queues

[Specs](../../README.md) / [Resources](../README.md) / **Queues**

## Purpose

Fair, first-come access to resources that only a limited number of agents may use at once, such as a merge into a shared branch, a shared database or a heavy build. Each resource has a number of slots; agents queue for a slot, hold it under a lease, and give it back. A lock is a resource with one slot.

## Requirements

### Requirement: Resources Have Slots

The system SHALL give every resource a key and a number of slots, 1 unless set otherwise, SHALL accept keys of up to 64 characters made of lowercase letters, digits, `.`, `_`, `-` and `/` that start with a letter or digit and contain no `..`, and SHALL let an agent change the number of slots of a resource to any number from 1 to 1000.

#### Scenario: First use of a key
- **WHEN** an agent joins the queue of `example-app/merge`, which nobody used before
- **THEN** the resource exists with 1 slot

#### Scenario: More slots
- **WHEN** the slots of `heavy/typecheck` are set to 3
- **THEN** up to 3 agents hold it at the same time

#### Scenario: Fewer slots than holders
- **WHEN** the slots of a resource with 3 holders are set to 1
- **THEN** the 3 holders keep their slots, and no further slot is granted until fewer than 1 agent holds it

#### Scenario: Invalid key
- **WHEN** an agent uses the key `../etc` or `Example`
- **THEN** the request is refused

### Requirement: Joining A Queue

The system SHALL let an agent join the queue of a resource with an optional note and a lease duration (30 minutes by default, from 1 second to 7 days); SHALL grant a slot at once when one is free and nobody is waiting; and SHALL otherwise place the agent at the end of the queue and report its position. Joining a queue the agent is already in SHALL keep its place, update its note and, when a lease duration is given, use that duration from then on: a slot the agent holds then lasts the new duration from now.

#### Scenario: Free resource
- **WHEN** an agent joins the queue of a free resource
- **THEN** it holds a slot until its lease ends

#### Scenario: Busy resource
- **WHEN** an agent joins a 1-slot resource that another agent holds while one more agent waits
- **THEN** it waits at position 2

#### Scenario: Joining twice
- **WHEN** an agent at position 2 joins the same queue again with a new note
- **THEN** it is still at position 2 and its note is the new one

#### Scenario: Joining again with a new lease
- **WHEN** an agent holding a slot with a 10-minute lease joins the same queue again with a 2-hour lease
- **THEN** it holds the slot until 2 hours from now, and a renewal extends it by 2 hours

#### Scenario: Waiting agent joins again with a new lease
- **WHEN** a waiting agent joins the same queue again with a 2-hour lease and later gets the slot
- **THEN** it holds the slot for 2 hours

#### Scenario: Joining again without a lease
- **WHEN** an agent holding a slot with a 2-hour lease joins the same queue again without giving a lease
- **THEN** its lease end and lease duration are unchanged

#### Scenario: Lease out of bounds
- **WHEN** an agent joins a queue with a lease of 1 millisecond or 8 days
- **THEN** the join is refused with the allowed range in the error

#### Scenario: Simultaneous joins
- **WHEN** several agents join the queue of a free 1-slot resource at the same moment
- **THEN** exactly one of them holds it and the others wait in a definite order

### Requirement: Slots Are Granted In Order

The system SHALL offer a freed slot to the waiting agent that joined first, and SHALL never let a later agent take a slot while an earlier one waits.

#### Scenario: Slot freed
- **WHEN** the holder releases a 1-slot resource with agents A and B waiting, in that order
- **THEN** the slot is offered to A, and B moves to position 1

### Requirement: An Offered Slot Must Be Claimed

The system SHALL give an agent offered a slot 2 minutes to claim it; SHALL move an agent that misses its turn to the end of the queue; and SHALL remove an agent from the queue after it misses its turn twice. An agent that is waiting for its turn when the slot is offered claims it at once.

#### Scenario: Claiming
- **WHEN** an agent offered a slot claims it within 2 minutes
- **THEN** it holds the slot for its lease duration

#### Scenario: Missed turn
- **WHEN** an agent does not claim an offered slot within 2 minutes
- **THEN** it moves to the end of the queue and the slot is offered to the next agent

#### Scenario: Missed twice
- **WHEN** the same agent misses its turn a second time
- **THEN** it is removed from the queue

#### Scenario: Waiting agent
- **WHEN** a slot is offered to an agent that is waiting for its turn
- **THEN** the agent holds the slot immediately and its wait ends

### Requirement: Held Slots Are Leases

The system SHALL keep a slot held until its lease ends, SHALL let the holder renew the lease for another lease duration, and SHALL free the slot when the lease ends without renewal.

#### Scenario: Renewing
- **WHEN** the holder of a slot with a 10-minute lease renews it after 8 minutes
- **THEN** the lease now ends 10 minutes after the renewal

#### Scenario: Expired lease
- **WHEN** a lease ends without renewal
- **THEN** the slot is free and offered to the next waiting agent, if any

### Requirement: Waiting For A Turn

The system SHALL let an agent wait for its turn and report its position whenever it changes, SHALL end the wait when the agent holds a slot, and SHALL end it with an error when the agent is no longer in the queue. A wait SHALL continue across a restart of the hub, and SHALL end with an error saying the hub cannot be reached once the hub has not answered for 30 seconds.

#### Scenario: Turn comes
- **WHEN** an agent at position 2 waits and the two agents ahead of it release their slots
- **THEN** it sees its position change to 1, then holds the slot and the wait ends

#### Scenario: Removed while waiting
- **WHEN** a waiting agent is removed from the queue
- **THEN** its wait ends with an error saying it is no longer queued

#### Scenario: Hub restarts during a wait
- **WHEN** the hub restarts while an agent waits for its turn, and the slot is freed after the restart
- **THEN** the agent's wait continues and ends when it holds the slot

#### Scenario: No hub
- **WHEN** an agent waits for its turn and the hub cannot be reached for 30 seconds
- **THEN** the wait ends with an error saying the hub cannot be reached

### Requirement: Leaving A Queue

The system SHALL let an agent leave a queue whether it holds a slot or waits, SHALL refuse to remove another agent from a queue unless forced, and SHALL record who forced a removal.

#### Scenario: Releasing a slot
- **WHEN** the holder releases its slot
- **THEN** the slot is free and offered to the next waiting agent, if any

#### Scenario: Someone else's slot
- **WHEN** an agent releases a slot another agent holds
- **THEN** the request is refused unless forced, and the error names the holder

#### Scenario: Not queued
- **WHEN** an agent releases a resource it neither holds nor waits for
- **THEN** it is told that it was not queued

### Requirement: Locks Are Queues That Do Not Wait

The system SHALL offer locks as a shorthand: taking a lock joins the resource's queue only if a slot can be held at once, and otherwise leaves the queue unchanged and reports the holders with their notes and lease ends, and any agent whose turn it is, signalling the refusal to scripts with exit code 2. Taking a lock the agent already holds renews it for the new duration; taking a lock while only waiting for it is refused. Releasing a lock leaves the queue, and releasing a lock someone else holds is refused with the holder's name unless forced.

#### Scenario: Free lock
- **WHEN** an agent takes the lock `example-app/merge` for 10 minutes with the note `merging #57`
- **THEN** it holds the resource's slot until 10 minutes from now

#### Scenario: Taken lock
- **WHEN** an agent takes a lock another agent holds until 12:30
- **THEN** the command exits with code 2, names the holder, `12:30` and the holder's note, and the agent is not queued

#### Scenario: Locking again
- **WHEN** the holder of a lock that ends in 5 minutes takes it again for 2 hours
- **THEN** it holds the lock until 2 hours from now

#### Scenario: Locking while queued
- **WHEN** an agent that waits in a resource's queue takes its lock
- **THEN** the command exits with code 2 and the agent keeps its place in the queue

#### Scenario: Releasing someone else's lock
- **WHEN** an agent releases a lock another agent holds, without force
- **THEN** the command fails and names the holder

### Requirement: Listing Resources

The system SHALL list resources with their slots, holders (with note and lease end), agents offered a slot (with claim deadline) and waiting agents in order, and SHALL omit resources that nobody holds or waits for unless their slots were set.

#### Scenario: Listing
- **WHEN** an agent lists resources while `heavy/typecheck` has 3 slots, 3 holders and 2 waiting agents
- **THEN** it sees the resource with those 3 holders and the 2 waiting agents at positions 1 and 2

### Requirement: Queues Survive A Restart

The system SHALL keep resources, holders, offers and waiting agents across a restart of the hub.

#### Scenario: Restart
- **WHEN** the hub restarts while an agent holds a slot and another waits
- **THEN** after the restart the same agent holds the slot and the other waits at the same position
