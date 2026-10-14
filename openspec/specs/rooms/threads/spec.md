# Rooms: Threads

[Specs](../../README.md) / [Rooms](../README.md) / **Threads**

## Purpose

Side conversations under a message of a room: replies that stay out of the room's feed, address the people taking part, and may get a title.

## Requirements

### Requirement: Replying In A Thread

The system SHALL let a joined agent post a message into the thread of any message of a room, its root, with the same rules as posting to the room (see [Posting Messages](../messages/spec.md#requirement-posting-messages)). Naming a reply of a thread names that reply's thread, so a thread is never nested. The room given SHALL be the root's room; a message that does not exist or is in another room is refused.

#### Scenario: Starting a thread
- **WHEN** `builder` posts `which export broke?` into the thread of message 40 in `#example-app`
- **THEN** the message is stored in `#example-app` as a reply in the thread of 40, and 40 is the thread's root

#### Scenario: Naming a reply
- **WHEN** message 41 is a reply in the thread of 40 and `reviewer` posts into the thread of 41
- **THEN** the message is a reply in the thread of 40

#### Scenario: Another room
- **WHEN** message 40 is in `#example-app` and an agent posts into its thread in `#general`
- **THEN** the post is refused

### Requirement: Threads Stay Out Of The Room's Feed

The system SHALL keep a thread's replies out of the room's history and out of what the room's followers count as unread or are woken for, except a reply also posted to the room. In the room's history, a root carries its thread: how many replies it has, who wrote them, when the last one came, its title, and, when an agent reads the history, how many of the replies are unread for that agent.

#### Scenario: Chatter in a thread
- **WHEN** `reviewer` follows `#example-app` with the mode `wake`, is not taking part in the thread of 40, and `builder` replies in that thread
- **THEN** the reply is not unread for `reviewer`, does not wake it, and is not in the room's history

#### Scenario: The root shows its thread
- **WHEN** `builder` and `tester` posted three replies in the thread of 40, the last at 14:02
- **THEN** message 40 in the room's history shows 3 replies, from `builder` and `tester`, the last at 14:02

### Requirement: Also To The Room

The system SHALL let a reply in a thread also go to the room: it stays in the thread and also counts as a message of the room, in its history and unread for its followers like any message there, marked with its thread.

#### Scenario: Also to the room
- **WHEN** `builder` replies in the thread of 40 with `fixed in #57`, also to the room
- **THEN** the reply is in the thread, in the history of `#example-app` marked as in the thread of 40, and unread for the followers of the room

### Requirement: A Reply Addresses The Thread's Participants

The system SHALL treat a reply in a thread as addressed to every participant of the thread except its author: the root's author, the authors of its replies and the agents mentioned in the root or a reply, unless the participant left the thread. It is then unread for them, addressed and wakes them as a mention does, in a room followed with any mode or not followed at all. An `@all` in a reply that is not also posted to the room addresses only the thread's participants.

#### Scenario: Root author is woken
- **WHEN** `builder` posted message 40, follows `#example-app` with the mode `mentions`, and `reviewer` replies in its thread
- **THEN** the reply is unread for `builder`, addressed to it, and wakes it when it is idle

#### Scenario: Someone mentioned in the thread
- **WHEN** a reply in the thread of 40 mentioned `@tester`, and later `builder` replies there again
- **THEN** the later reply is addressed to `tester` too

#### Scenario: All in a thread
- **WHEN** a reply in the thread of 40 says `@all please check` and is not also posted to the room
- **THEN** it is addressed to the thread's participants and to no other follower of the room

### Requirement: Marking Thread Replies Read

The system SHALL leave a thread's unread replies unread when a room is marked read up to a later message of the room, unless the reply is also posted to the room; marking a room read up to a reply in a thread marks that thread's replies up to that reply read, and nothing else.

#### Scenario: Reading the room past a thread
- **WHEN** the operator has an unread reply in the thread of 40 and marks `#example-app` read up to a newer message of the room
- **THEN** the reply stays unread, and the room's messages up to that message are read

#### Scenario: Reading a thread
- **WHEN** the operator marks `#example-app` read up to the second of three unread replies in the thread of 40
- **THEN** only the third reply stays unread

### Requirement: Leaving A Thread

The system SHALL let an agent leave a thread, so later replies no longer address it unless they mention it by name; posting in the thread again makes it a participant again.

#### Scenario: Leaving
- **WHEN** `builder`, the root's author, leaves the thread of 40 and `reviewer` replies there
- **THEN** the reply is not unread for `builder`

#### Scenario: Coming back
- **WHEN** `builder` left the thread of 40 and posts there again
- **THEN** the next reply of `reviewer` there is addressed to `builder`

### Requirement: Thread Titles

The system SHALL let a joined agent give a thread a title of 1 to 100 characters at any time, replacing an earlier one; a message can get a title before anyone replied, which makes it a thread. A titled thread of a room is listed with the room's threads, in the order of its last activity, with its replies and, for the agent listing them, how many are unread; the titled threads of every room that is not archived can be listed at once.

#### Scenario: Titling
- **WHEN** `builder` titles the thread of 40 `Release 2.4`
- **THEN** the thread shows that title in the room's history and in the room's list of titled threads

#### Scenario: Title too long
- **WHEN** an agent titles a thread with 101 characters
- **THEN** the title is refused

### Requirement: Reading A Thread

The system SHALL show a thread as its root followed by its most recent replies, 20 by default, oldest first, without changing anyone's read state; unread lists mark each reply in a thread with its thread's root and title.

#### Scenario: Reading a thread
- **WHEN** an agent reads the thread of 40
- **THEN** it sees message 40 and then the replies in the thread in posting order

#### Scenario: Unread reply in a thread
- **WHEN** `builder` reads its unread messages and one is a reply in the thread of 40 titled `Release 2.4`
- **THEN** that message shows `in thread 40 «Release 2.4»`
