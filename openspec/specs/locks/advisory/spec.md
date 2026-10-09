# Locks: Advisory Locks

[Specs](../../README.md) / [Locks](../README.md) / **Advisory Locks**

## Purpose

Named locks with an expiry that agents take before using something only one of them may use at a time, such as a merge queue or a shared database.

## Requirements

### Requirement: Taking A Lock

The system SHALL let an agent take a lock by key with a time to live (30 minutes by default) and an optional note, SHALL accept keys of up to 64 characters made of lowercase letters, digits, `.`, `_`, `-` and `/` that start with a letter or digit and contain no `..`, and SHALL take locks atomically so two agents never both hold the same key.

#### Scenario: Free lock
- **WHEN** an agent takes `example-app/merge` for 10 minutes with the note `merging #57`
- **THEN** it holds the lock until 10 minutes from now, and the lock shows owner, start, expiry and note

#### Scenario: Two agents at once
- **WHEN** two agents try to take the same free key at the same moment
- **THEN** exactly one of them holds it

#### Scenario: Invalid key
- **WHEN** an agent uses the key `../etc`
- **THEN** the request is refused

### Requirement: A Held Lock Is Refused To Others

The system SHALL refuse a lock that another agent holds and that has not expired, SHALL report the holder, expiry and note, and SHALL signal the refusal to scripts with exit code 2.

#### Scenario: Lock held by someone else
- **WHEN** an agent takes a key another agent holds until 12:30
- **THEN** the request fails with exit code 2 and names the holder, `12:30` and the holder's note

### Requirement: Renewing And Expiry

The system SHALL let the holder take its own lock again to replace its expiry and note, and SHALL treat an expired lock as free.

#### Scenario: Renewing
- **WHEN** the holder takes its lock again for 20 minutes
- **THEN** the lock now expires 20 minutes from now

#### Scenario: Expired lock
- **WHEN** a lock's expiry has passed
- **THEN** another agent may take it, and it no longer appears among held locks

### Requirement: Releasing A Lock

The system SHALL let the holder release its lock, SHALL refuse to release another agent's unexpired lock unless forced, and SHALL report when the key was not held.

#### Scenario: Releasing
- **WHEN** the holder releases its lock
- **THEN** the key is free

#### Scenario: Someone else's lock
- **WHEN** an agent releases a lock another agent holds
- **THEN** the release is refused unless it is forced, and the error names the holder

### Requirement: Listing Locks

The system SHALL list every unexpired lock with key, owner, expiry and note.

#### Scenario: Listing
- **WHEN** an agent lists locks
- **THEN** it sees every held lock and none that has expired
