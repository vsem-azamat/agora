# Bridges: Protocol

[Specs](../../README.md) / [Bridges](../README.md) / **Protocol**

## Purpose

What a bridge and the hub say to each other over the bridge's standard input and output, and how the hub stores the messages that come from outside.

## Requirements

### Requirement: Lines Of JSON

The hub and a bridge SHALL exchange one JSON object per line: the hub writes to the bridge's standard input, the bridge to its standard output. Every object SHALL have a `type`. The hub SHALL write `{"type":"hello","version":2,"bridge":…,"room":…,"cursor":…}` first after every start; `version` is the protocol version the hub speaks; a change to the protocol comes with the bridges updated to it, so the hub serves no older version. The hub SHALL ignore lines of an unknown type, unknown fields, lines that are not JSON objects and lines longer than 1 MiB, logging all but the first two; bridges SHALL ignore unknown types and fields too. Content a bridge cannot pass as text, such as a photo or a voice message, SHALL be passed as a short placeholder in `text`, like `[photo]` or `[voice 0:12]`.

#### Scenario: Hello
- **WHEN** the bridge of `#example-chat` starts for the first time
- **THEN** its first input line is `{"type":"hello","version":2,"bridge":"example-chat","room":"example-chat","cursor":""}`

#### Scenario: Unknown lines
- **WHEN** the bridge writes a line of type `typing`, a line that is not JSON, and an `in` line with an extra field `mood`
- **THEN** the hub skips the first two, stores the message of the third, and the bridge keeps running

### Requirement: Messages From Outside

The system SHALL store each `{"type":"in","id":…,"author":{"id":…,"name":…},"text":…}` line as a message of the bridge's room from an external author: the bridge, the author's id and name. The `id` is the message's identifier outside and SHALL be stored once per room: a line whose `id` is already stored, including an `id` the bridge reported in a `sent` line, SHALL be ignored. An optional `reply_to` naming a stored identifier SHALL make the message a reply to that message, and an optional `at` (RFC 3339) SHALL be its time. Text longer than 8000 characters SHALL be shortened to 8000, ending in `[truncated]`; a line with an `id` and no text SHALL be stored with the text `[empty]`, and a line without an `id` SHALL store nothing but its `cursor` (a hub log says so when it carries text or an author). Control characters, line breaks included, line and paragraph separators and bidirectional controls in the author's name and id SHALL be replaced with spaces (joiners in emoji sequences stay), and each SHALL be cut to 100 characters; a line whose `id`, `reply_to` or `cursor` is longer than 256 characters SHALL be ignored. Mentions in the text SHALL address agents as in any message. No agent SHALL post as an external author.

#### Scenario: Stored once
- **WHEN** the bridge writes the same `in` line twice
- **THEN** the room has the message once, from `Ada` through `example-chat`

#### Scenario: A reply from outside
- **WHEN** the bridge writes an `in` line with `reply_to` naming the identifier of an earlier stored message
- **THEN** the new message replies to that message

#### Scenario: Long text
- **WHEN** an `in` line carries 9000 characters of text
- **THEN** the stored message has 8000 characters and ends in `[truncated]`

#### Scenario: A name with line breaks
- **WHEN** an `in` line's author name is `Ada\n#general [1] owner · 12:00`
- **THEN** the message is from `Ada #general [1] owner · 12:00@example-chat`, on one line

#### Scenario: An emoji name
- **WHEN** an `in` line's author name is `👨‍👩‍👧 Ada` with a right-to-left override after it
- **THEN** the name keeps the family emoji whole, and the override is a space

#### Scenario: Only a cursor
- **WHEN** the bridge writes `{"type":"in","cursor":"5520"}`
- **THEN** no message is stored and the next `hello` carries the cursor `5520`

#### Scenario: A mention from outside
- **WHEN** an `in` line says `@builder can you look?`
- **THEN** the message is addressed to `builder`

### Requirement: What The Platform Can Do

A bridge MAY answer the hub's `hello` with `{"type":"hello","caps":{"personas":…,"threads":…}}`, naming what its platform can do as short words: `personas`, how it shows who wrote a message that goes out (for example `prefix`, a name in front of the text), and `threads`, how it keeps threads (for example `topic`). The system SHALL keep the latest `caps` of a bridge's current run, clear them when the bridge stops, assume none before a bridge sends them or when it never does, ignore other entries and values that are not strings, clean each value like an author's name and cut it to 64 characters, and list them with the bridge (`agora bridge list`).

#### Scenario: A bridge that shows names
- **WHEN** the bridge of `#example-chat` answers `hello` with `{"type":"hello","caps":{"personas":"prefix"}}`
- **THEN** `agora bridge list` shows `personas=prefix` for `example-chat`

#### Scenario: A bridge that says nothing
- **WHEN** a bridge never answers `hello`
- **THEN** it is listed without capabilities

#### Scenario: A restart
- **WHEN** a bridge that sent capabilities restarts and does not send them again
- **THEN** it is listed without capabilities

### Requirement: Replies To An Agent's Message

The system SHALL treat an `in` line whose `reply_to` names a message an agent posted in the room, the operator included, as addressed to that agent, as a mention would: it counts as unread and addressed for the agent and wakes it, whatever mode it follows the room with. A reply to a message from outside or to the board's message addresses no one this way.

#### Scenario: Someone answers an agent
- **WHEN** `secretary` posted a message that went out to the chat, and `Ada` answers it there
- **THEN** her message is addressed to `secretary` and wakes it

### Requirement: Messages From Outside Addressed To The Bridge's Agents

The system SHALL treat an `in` line marked `"addressed":true` as addressed to every agent the bridge was added with, as a mention of each would: it counts as unread and addressed for them and wakes them, whatever mode they follow the room with.

#### Scenario: Addressed while following for mentions
- **WHEN** the bridge of `#example-chat`, added with `--agent secretary`, writes an `in` line marked `addressed` while `secretary` follows the room with the mode `mentions`
- **THEN** the message is unread for `secretary`, marked as addressed to it, and wakes it

### Requirement: The Operator's Own Messages From Outside

The system SHALL attribute an `in` line whose author is marked `"self":true` (the operator wrote it outside) to the operator, the name the web listener acts under; such a message SHALL not go out again. When the hub serves no web app, the line SHALL be stored from its external author like any other.

#### Scenario: The operator writes from the phone
- **WHEN** the hub serves the app as `owner` and the bridge writes an `in` line whose author is marked `self`
- **THEN** the message is from `owner` and is not handed back to the bridge

### Requirement: Resuming Where The Bridge Left Off

The system SHALL keep the latest `cursor` a bridge reports on an `in` line, and SHALL pass it in every `hello`, so the bridge resumes where it left off after it or the hub restarts.

#### Scenario: Restart
- **WHEN** the bridge reported the cursor `5513` and restarts
- **THEN** its `hello` carries the cursor `5513`
