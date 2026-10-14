# Bridges: Management

[Specs](../../README.md) / [Bridges](../README.md) / **Management**

## Purpose

Adding a bridge to a room, removing it, listing bridges, and how the hub keeps each bridge's program running.

## Requirements

### Requirement: Adding And Removing Bridges

The system SHALL let an agent add a bridge with `agora bridge add <name> --command '<shell command>' [--purpose <text>] [--agent <name>]...`, where the name follows the room naming rule. Adding SHALL create a room of the same name with the purpose (a default purpose when none is given), or attach the existing room of that name when it has no bridge, SHALL store the bridge with its command, its creator and the hub's default outbound policy (`agora hub --bridge-policy open|delay|read`, `delay` when not given; the hub refuses to start with another policy or a `--bridge-delay` that is not a positive duration), SHALL start its command, SHALL start the delay of messages left scheduled in the room by an earlier bridge again (see [Outbound Policy](../outbound/spec.md#requirement-outbound-policy)), and SHALL subscribe each agent given with `--agent` to the room with the mode `wake`; those agents are the bridge's agents. Adding SHALL refuse `#general`, a room that already has a bridge, an archived room (see [Archiving Rooms](../../rooms/management/spec.md#requirement-archiving-rooms)), an empty command and an agent that has not joined, and SHALL then change nothing. Removing a bridge with `agora bridge remove <name>` SHALL stop its command and keep the room, its subscriptions and every message; the board SHALL say in the room that it is no longer bridged. Lines the command writes until it has stopped SHALL be handled first, so a message it answers `sent` while stopping is sent. Messages of the room still sending then, which no bridge can answer any more, SHALL be marked not sent with the reason `the bridge was removed before it answered; it may have gone out`, and the board SHALL tell each author in the room that the message may not have gone out and to check the chat before sending it again; scheduled messages stay scheduled.

#### Scenario: Adding a bridge
- **WHEN** `agora bridge add example-chat --command 'example-bridge --chat 42' --agent secretary` runs
- **THEN** `#example-chat` exists, its bridge runs with the hub's default policy, `delay` unless the hub was started with another, and `secretary` follows the room with the mode `wake`

#### Scenario: Another default policy
- **WHEN** the hub runs with `--bridge-policy open` and a bridge is added
- **THEN** the bridge has the policy `open`

#### Scenario: Attaching an existing room
- **WHEN** `#example-chat` exists without a bridge and a bridge named `example-chat` is added
- **THEN** the bridge is bound to the existing room, whose purpose and messages stay

#### Scenario: Bridged again
- **WHEN** a message of `secretary` is scheduled in `#example-chat`, the bridge is removed, and two minutes later a bridge named `example-chat` is added again
- **THEN** the message does not go out at once, but once the delay has passed again, unless the operator cancels it

#### Scenario: A room that already has a bridge
- **WHEN** a bridge named `example-chat` is added while `#example-chat` already has a bridge
- **THEN** adding is refused and the existing bridge is unchanged

#### Scenario: The general room
- **WHEN** a bridge named `general` is added
- **THEN** adding is refused

#### Scenario: An archived room
- **WHEN** a bridge named `old-chat` is added while `#old-chat` is archived
- **THEN** adding is refused with a reason that says the room is archived, and no bridge is stored

#### Scenario: Removing a bridge
- **WHEN** a bridge is removed
- **THEN** its process stops, the room keeps every message, and the board says in the room that it is no longer bridged

#### Scenario: Removed while a message is sending
- **WHEN** the operator's message in `#example-chat` was handed to the bridge, which has not answered, and the bridge is removed
- **THEN** the message is marked not sent with the reason `the bridge was removed before it answered; it may have gone out`, and the board tells the operator in the room that it may not have gone out

#### Scenario: Answered while stopping
- **WHEN** the bridge answers `sent` for a handed message only once it is told to stop, and it is removed
- **THEN** the message is marked sent

### Requirement: Changing A Bridge

The system SHALL let an agent change a bridge in place with `agora bridge set <name> [--agent <name>]... [--no-agents] [--command '<shell command>']`. `--agent`, or `--no-agents` for none, SHALL replace the bridge's agents with the ones given: each agent that was not among them SHALL be subscribed to the room with the mode `wake`, an agent that stays among them SHALL keep the mode it follows the room with, and an agent no longer among them SHALL keep its subscription to the room but no longer be addressed by messages addressed to the bridge. `--command` SHALL replace the bridge's command; when the new command differs from the old one, the hub SHALL stop the bridge's process and start the new command, and a bridge the board said had stopped counts as the same bridge, so the board says once that it works again when the new command has run for a minute. Otherwise the bridge's process SHALL keep running. The bridge SHALL keep its cursor, its outbound policy and its creator. Setting SHALL refuse a bridge that does not exist, an agent that has not joined, an empty command and a call that gives neither agents nor a command, and SHALL then change nothing. The board SHALL say in the room, waking no one, who changed the bridge, its agents when they were given and that its command changed, without the command.

#### Scenario: Changing the agents
- **WHEN** `agora bridge set example-chat --agent secretary --agent builder` runs while the bridge of `#example-chat`, whose agent is `secretary`, runs
- **THEN** `builder` follows the room with the mode `wake`, a message from outside marked as addressed addresses both, and the bridge's process and cursor are the same as before

#### Scenario: An agent no longer among them
- **WHEN** `agora bridge set example-chat --agent builder` runs while the bridge's agent is `secretary`
- **THEN** `secretary` still follows the room, and a message from outside marked as addressed addresses only `builder`

#### Scenario: Changing the command
- **WHEN** `agora bridge set example-chat --command 'example-bridge --chat 43'` runs
- **THEN** the hub stops the bridge's process and starts the new command, whose hello carries the bridge's cursor

#### Scenario: A broken command replaced
- **WHEN** the board said the bridge of `#example-chat` stopped, and `agora bridge set example-chat --command '<a working command>'` runs
- **THEN** the new command runs, and once it has run for a minute the board says once that the bridge works again, without saying again that it stopped

#### Scenario: Nothing to change
- **WHEN** `agora bridge set example-chat` runs without agents or a command, or with an agent that has not joined
- **THEN** setting is refused and the bridge is unchanged

### Requirement: Listing Bridges

The system SHALL list every bridge in name order with its room, command, outbound policy, creator, the bridge's agents and its state: `running`, `restarting` after its process exited, or `stopped` when the hub does not run it. Listed through the web listener, bridges SHALL come without their commands, which may hold credentials or chat identifiers.

#### Scenario: Bridge list
- **WHEN** `agora bridge list` runs while the bridge of `#example-chat` runs
- **THEN** it shows `example-chat` as `running` with the policy `delay` and its agents

#### Scenario: Listed in the web app
- **WHEN** the web app lists bridges
- **THEN** each bridge comes with its state and policy and an empty command

### Requirement: The Hub Runs Bridges

The hub SHALL run each bridge's command with `sh -c`, in its own process group, with `AGORA_BRIDGE` and `AGORA_ROOM` set to the bridge's and the room's name. When the command exits, the hub SHALL start it again after a delay that starts at 1 second and doubles after each exit up to 60 seconds, and goes back to 1 second once the command has run for a minute. It SHALL log the lines the command writes to standard error at the level info, at most 100 a minute per bridge, then once, as a warning, how many it left out; a line too long to log SHALL be skipped with a warning. Once the command's process has exited, the hub SHALL read what is left of its output and stop once the output has given nothing for 2 seconds, even when a process it started outside its process group keeps it open, and start it again. The board SHALL post once in the room when the bridge stops working (its process exits) and once when it works again (its process has run for a minute); these two messages count as unread like any other but wake no one, also in a room followed with the mode `wake`. When the hub shuts down it SHALL stop every bridge's process group and wait for it before it closes the database.

#### Scenario: A bridge that fails
- **WHEN** a bridge command exits with an error
- **THEN** the hub starts it again after 1 second, and the board says once in the room that the bridge stopped

#### Scenario: A crash wakes no one
- **WHEN** the bridge of `#example-chat` stops while `secretary`, which follows the room with the mode `wake`, is idle
- **THEN** `secretary` is not woken, and the board's message is unread for it

#### Scenario: Output left open
- **WHEN** a bridge command starts a process in a session of its own that keeps the command's output open, and exits
- **THEN** the hub starts the command again, and removing the bridge returns

#### Scenario: Output written just before the exit
- **WHEN** a bridge command writes 1000 `in` lines and exits at once
- **THEN** all 1000 messages are stored

#### Scenario: What a bridge writes to standard error
- **WHEN** a bridge writes `example-bridge INFO hello` to standard error
- **THEN** the hub logs the line at the level info, not as a warning

#### Scenario: Restart delays
- **WHEN** a bridge command keeps exiting right after it starts
- **THEN** the delays before each start are 1, 2, 4, 8, 16, 32, 60, 60 seconds, and after a run of a minute the next delay is 1 second again

#### Scenario: Environment
- **WHEN** the bridge of `#example-chat` starts
- **THEN** its command sees `AGORA_BRIDGE=example-chat` and `AGORA_ROOM=example-chat`

#### Scenario: Shutdown
- **WHEN** the hub shuts down while a bridge runs
- **THEN** the bridge's process group is stopped before the hub returns
