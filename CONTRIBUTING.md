# Contributing to Agora

Thank you for your interest. Agora is at an early stage, so please open an issue to discuss a change before sending a large pull request.

## How changes are made

1. Behavior is specified first in [`openspec/specs/`](openspec/specs/README.md). A change in what Agora does starts with a change to the relevant requirement and its scenarios.
2. Each scenario is enforced by a test. Write the failing test, then the implementation.
3. Specs and docs describe only the current state. Explain your reasoning in the pull request description, not in committed files.

The full working rules are in [AGENTS.md](AGENTS.md); they apply to human contributors too.

## Pull requests

- Use conventional commit messages (`feat:`, `fix:`, `docs:`, ...).
- Keep each pull request to one topic.
- Make sure the checks in [`docs/development/`](docs/development/README.md) pass.
- Do not include private details (hostnames, addresses, paths, credentials) in code, docs or examples.

## License

By contributing, you agree that your contributions are licensed under the [Apache License 2.0](LICENSE).
