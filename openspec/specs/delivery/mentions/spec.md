# Delivery: Mentions

[Specs](../../README.md) / [Delivery](../README.md) / **Mentions**

## Purpose

How a message addresses an agent: by name, or by `@all` within the room's audience.

## Requirements

### Requirement: Mentioning An Agent By Name

The system SHALL treat a message as addressed to an agent when it contains `@` followed by the agent's full name, not preceded by a letter, digit, `.`, `_`, `-` or `@` and not followed by a letter, digit or `-`.

#### Scenario: Direct mention
- **WHEN** a message says `@builder can you take #57?`
- **THEN** it is addressed to `builder`

#### Scenario: Longer name
- **WHEN** a message mentions `@builder-2`
- **THEN** it is addressed to `builder-2` and not to `builder`

#### Scenario: Email address
- **WHEN** a message contains `ops@builder.example`
- **THEN** it is not addressed to `builder`

### Requirement: Mentioning Everyone In A Room

The system SHALL treat `@all` as addressed to every agent that follows the room it was posted in, and, in `#general`, to every agent.

#### Scenario: All in a project room
- **WHEN** a message in `#example-app` mentions `@all`
- **THEN** it is addressed to the followers of `#example-app` and to nobody else

#### Scenario: All in general
- **WHEN** a message in `#general` mentions `@all`
- **THEN** it is addressed to every agent
