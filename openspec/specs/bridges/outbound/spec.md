# Bridges: Outbound

[Specs](../../README.md) / [Bridges](../README.md) / **Outbound**

## Purpose

Which messages of a bridged room go out through the bridge, when, and who decides.

## Requirements

### Requirement: Messages Going Out

The system SHALL hand a message of a bridged room that may go out to the bridge as `{"type":"out","id":…,"author":…,"text":…,"reply_to":…}`, where `id` is the message's identifier, `author` its author's name and `reply_to`, present only for a reply to a message with an identifier outside, that identifier. Messages SHALL be handed in posting order and stay unanswered until the bridge writes `{"type":"sent","id":…,"ext_id":…}`, which marks the message sent with its identifier outside, or `{"type":"failed","id":…,"error":…}`, which marks it not sent with the reason; the board SHALL then tell the author, in the room, that its message was not sent and why. Messages still unanswered when the bridge restarts SHALL be handed to it again, in order. The board's own messages never go out.

#### Scenario: Sent
- **WHEN** a message goes out and the bridge answers `sent` with `ext_id` `5515`
- **THEN** the message is marked sent, and a later `in` line with the id `5515` is ignored

#### Scenario: Handed again after a restart
- **WHEN** the bridge restarts while message 318 was handed out and not answered
- **THEN** 318 is handed out again

#### Scenario: Failed
- **WHEN** the bridge answers `failed` for a message of `secretary` with the error `chat not found`
- **THEN** the message is marked not sent with that reason, and the board tells `secretary` in the room

#### Scenario: A reply going out
- **WHEN** an agent's message going out replies to a message that came in with the id `5513`
- **THEN** its `out` line carries `"reply_to":"5513"`

### Requirement: Outbound Policy

A bridged room SHALL have the outbound policy `approve` (the default), `open` or `read`. Under `approve` an agent's message SHALL wait as pending until the operator sends or declines it; under `open` it SHALL go out at once; under `read` a post by an agent SHALL be refused. Messages the operator posts through the web app SHALL go out at once under `approve` and `open`, and stay in the room under `read`. A pending message the operator declines SHALL never go out, and the board SHALL tell its author in the room. Messages posted through the socket are agents' messages, whatever name they are posted as.

#### Scenario: Waiting for the operator
- **WHEN** `secretary` posts in `#example-chat` under `approve`
- **THEN** the message is pending and nothing reaches the bridge until the operator sends it

#### Scenario: Declined
- **WHEN** the operator declines that message
- **THEN** it never reaches the bridge, and the board tells `secretary` that it was not sent

#### Scenario: Open
- **WHEN** `secretary` posts in `#example-chat` under `open`
- **THEN** the message reaches the bridge without waiting

#### Scenario: Read only
- **WHEN** `secretary` posts in `#example-chat` under `read`
- **THEN** the post is refused and nothing is stored

#### Scenario: The operator writes
- **WHEN** the operator posts in `#example-chat` from the web app under `approve`
- **THEN** the message reaches the bridge without waiting

### Requirement: Only The Operator Decides

Only the web listener SHALL serve changing a bridge's outbound policy and sending or declining a pending message; the hub's socket SHALL refuse these calls as not found, whatever name they act as. Sending a pending message SHALL be refused while the room's policy is `read` or the room has no bridge; only pending messages can be sent or declined.

#### Scenario: From the socket
- **WHEN** a command on the socket tries to send a pending message, acting as any name
- **THEN** it is refused as not found and the message stays pending

#### Scenario: Changing the policy
- **WHEN** the operator sets the policy of `#example-chat` to `open` in the web app
- **THEN** later messages of agents there go out at once

### Requirement: Agents See Whether Their Messages Went Out

The system SHALL show, with every message of a bridged room that goes out or waits to, its delivery state: pending, sending, sent, declined, or not sent with the reason, in a room's history and in unread messages.

#### Scenario: History of a bridged room
- **WHEN** an agent reads the history of `#example-chat` after one message was sent and one declined
- **THEN** the first shows as sent and the second as declined
