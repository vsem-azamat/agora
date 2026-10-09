# Agora Agent Instructions

This file is the working contract for AI agents and contributors in this repository. It says how to work and where to look; facts about the project live in [`docs/`](docs/README.md) and [`openspec/specs/`](openspec/specs/README.md).

## Sources Of Truth

- `openspec/specs/` defines what Agora does. Read the touched capability before changing behavior, and navigate through the `README.md` indexes instead of guessing paths.
- `docs/` describes how Agora is built and operated today. Start at [`docs/README.md`](docs/README.md).
- Code is the source of truth for how something is implemented. If code and a spec disagree, stop and reconcile them; never let them drift.

## Current State Only

- Specs and docs describe the system as it is now. No history, decision logs, plans, roadmaps or status notes.
- A behavior change edits `openspec/specs/` in the same PR as the tests and code that implement it, so `main` never specifies behavior that does not exist. A PR that changes only specs may only reword them without changing behavior. `openspec/changes/` is never committed.
- Behavior that is not built yet is discussed in an issue or a pull request, not in `openspec/specs/` on `main`.
- Rationale belongs in the PR description; deferred work belongs in an issue.
- Do not commit scratch files, agent plans, handoff notes, logs or temporary output. Use a directory outside the repository.

## Public Repository Hygiene

This repository is public. Before every commit and PR, check that nothing private goes in:

- no real hostnames, IP addresses, network names, home-directory paths or usernames of any machine;
- no names of private projects, companies or clients that Agora is used with;
- no emails, tokens, keys or `.env` contents;
- examples use neutral placeholders such as `node-a`, `node-b`, `~/src/example-app`.

## Order Of Work

Within one branch and one pull request:

1. Inspect the current checkout and read the relevant specs and docs.
2. Specify: add or change the requirement and its scenarios.
3. Write a failing test that enforces the scenario and watch it fail for the expected reason.
4. Implement the smallest change that makes it pass, then clean up.
5. Update docs when the engineering picture changes.
6. Run `make check` before claiming success or pushing; `make fmt` applies the formatters. The checks are described in [`docs/development/checks.md`](docs/development/checks.md).

## Quality Bar

- Fix root causes. Do not ship code you cannot explain.
- State uncertainty and risks explicitly.
- No abstraction until two real call sites need it.
- Do not mix unrelated cleanup into a change.
- Never patch generated code by hand; regenerate it.
- Fix lint findings instead of silencing them. A `//nolint:<linter>` comment gives its reason on the same line.

## Git And Pull Requests

- Branch from `main`; one topic per branch; conventional commit messages (`feat:`, `fix:`, `docs:`, `chore:`, `test:`, `refactor:`).
- Every change lands through a pull request with green CI. The PR description says what changed, why, and how it was verified.
- Releases are tags on `main` and are created by the maintainer only.
- Do not add AI attribution lines or trailers to commits or PRs.

## Instruction Files

`AGENTS.md` is the only agent instruction file. Do not add `CLAUDE.md` or tool-specific copies; CI rejects them.
