# Specs

[Docs](../README.md) / [Development](README.md) / **Specs**

`openspec/specs/` is a two-level tree in the [OpenSpec](https://github.com/Fission-AI/OpenSpec) format.

## Layout

```
openspec/specs/
  README.md                     index of capabilities
  <capability>/
    README.md                   purpose, sub-capability table, requirement index
    <sub-capability>/spec.md    requirements and scenarios
```

- One capability per top-level directory, split into one or more sub-capabilities. A capability has no `spec.md` of its own, and nothing is nested deeper than one level.
- Every capability is linked from `openspec/specs/README.md`; every sub-capability spec is linked from its capability `README.md`, which also lists every requirement with an anchor link.
- Each file starts with a breadcrumb line: `[Specs](../../README.md) / [Capability](../README.md) / **Sub-capability**`.

## Format

```markdown
# <Capability>: <Sub-capability>

[Specs](../../README.md) / [<Capability>](../README.md) / **<Sub-capability>**

## Purpose

What this part of Agora is responsible for, in a short paragraph.

## Requirements

### Requirement: <Title In Title Case>

The system SHALL ...

#### Scenario: <name>
- **WHEN** ...
- **THEN** ...
- **AND** ...
```

- Requirements state rules and outcomes (states, permissions, limits, failure behavior), not function or type names.
- Every requirement has at least one scenario; each scenario is enforced by a test.
- Behavior behind a flag or setting names that flag or setting.
- Specs describe the current behavior only. Changes edit the spec in place; `openspec/changes/` is not used in this repository.

## When specs change

A requirement reaches `main` in the same pull request as the tests and code that implement it. Within that branch the order is spec, failing test, code. A pull request that changes only specs may reword them, fix indexes or move requirements, but never add, remove or change behavior.

## Validation

`openspec validate --specs` checks the format, and `scripts/spec-layout-check.sh` checks the tree. See [Checks](checks.md).
