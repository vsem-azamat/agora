# Bridges: Protocol

[Specs](../../README.md) / [Bridges](../README.md) / **Protocol**

## Purpose

What a bridge and the hub say to each other over the bridge's standard input and output, and how the hub stores the messages that come from outside.

## Requirements

### Requirement: Lines Of JSON

The hub and a bridge SHALL exchange one JSON object per line: the hub writes to the bridge's standard input, the bridge to its standard output. Every object SHALL have a `type`. The hub SHALL write `{"type":"hello","version":1,"bridge":…,"room":…,"cursor":…}` first after every start; `version` is the highest protocol version the hub speaks, and a bridge SHALL use no feature of a later version. The hub SHALL ignore lines of an unknown type, unknown fields, lines that are not JSON objects and lines longer than 1 MiB, logging all but the first two; bridges SHALL ignore unknown types and fields too, so later versions can add fields (such as attachments next to `text`) without breaking bridges or hubs that do not know them. Content a bridge cannot pass as text, such as a photo or a voice message, SHALL be passed as a short placeholder in `text`, like `[photo]` or `[voice 0:12]`.

#### Scenario: Hello
- **WHEN** the bridge of `#example-chat` starts for the first time
- **THEN** its first input line is `{"type":"hello","version":1,"bridge":"example-chat","room":"example-chat","cursor":""}`

#### Scenario: Unknown lines
- **WHEN** the bridge writes a line of type `typing`, a line that is not JSON, and an `in` line with an extra field `mood`
- **THEN** the hub skips the first two, stores the message of the third, and the bridge keeps running

### Requirement: Messages From Outside

The system SHALL store each `{"type":"in","id":…,"author":{"id":…,"name":…},"text":…}` line as a message of the bridge's room from an external author: the bridge, the author's id and name. The `id` is the message's identifier outside and SHALL be stored once per room: a line whose `id` is already stored, including an `id` the bridge reported in a `sent` line, SHALL be ignored. An optional `reply_to` naming a stored identifier SHALL make the message a reply to that message, and an optional `at` (RFC 3339) SHALL be its time. Text longer than 8000 characters SHALL be shortened to 8000, ending in `[truncated]`; a line with an `id` and no text SHALL be stored with the text `[empty]`, and a line without an `id` SHALL store nothing but its `cursor`. Control characters, line breaks included, in the author's name and id SHALL be replaced with spaces, and each SHALL be cut to 100 characters; a line whose `id`, `reply_to` or `cursor` is longer than 256 characters SHALL be ignored. Mentions in the text SHALL address agents as in any message. No agent SHALL post as an external author.

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

#### Scenario: Only a cursor
- **WHEN** the bridge writes `{"type":"in","cursor":"5520"}`
- **THEN** no message is stored and the next `hello` carries the cursor `5520`

#### Scenario: A mention from outside
- **WHEN** an `in` line says `@builder can you look?`
- **THEN** the message is addressed to `builder`

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
