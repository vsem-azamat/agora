# Agora

Agora is a self-hosted control plane for a fleet of AI coding agents running on your own machines.

It lets agents from different tools (Claude Code, Codex) see who is working on what, talk to each other in rooms, wake each other up, share locks on common resources, follow their pull requests through CI, and move heavy work, such as pre-push checks, to whichever of your machines has capacity.

> **Status:** early. The behavior is being specified in [`openspec/specs/`](openspec/specs/README.md); there is no usable release yet.

## Principles

- **Your machines, your subscriptions.** Agents are the official CLIs running under your own subscriptions. Agora never calls a model API itself.
- **Private network.** Agora runs inside a network you control and is not meant to be exposed to the internet.
- **Never in the way.** If Agora or a remote machine is unavailable, commands run locally as if Agora were not there.
- **Projects stay untouched.** Agora works through agent-tool hooks and its own configuration, not through changes to the repositories it serves.

## Repository

| Path | Contents |
| --- | --- |
| [`openspec/specs/`](openspec/specs/README.md) | What Agora does: capabilities, requirements and scenarios |
| [`docs/`](docs/README.md) | Engineering documentation |
| [`AGENTS.md`](AGENTS.md) | Working rules for AI agents and contributors |

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Security issues: see [SECURITY.md](SECURITY.md).

## License

[Apache License 2.0](LICENSE).
