# Agora Specifications

[Repository](../../README.md) / **Specs**

`openspec/specs/` is the source of truth for what Agora does. One directory per capability; each capability is split into sub-capability specs that hold the requirements (`SHALL`) and scenarios (`WHEN`/`THEN`) that tests enforce. Every directory has a `README.md` index; follow the indexes instead of guessing a path.

Specs describe the current behavior only. Layout and format rules: [docs/development/specs.md](../../docs/development/specs.md).

## Capabilities

| Capability | Covers |
| --- | --- |
| [Agents](agents/README.md) | Agent names and how the agent behind a command is recognised. |
| [Connectors](connectors/README.md) | Agent tool integrations: sessions and the Claude Code connector. |
| [Resources](resources/README.md) | Fair queues for resources with a limited number of slots; locks are one-slot queues. |
