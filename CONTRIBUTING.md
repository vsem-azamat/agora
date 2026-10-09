# Contributing to Agora

Thank you for your interest. Agora is at an early stage, so please open an issue to discuss a change before sending a large pull request.

## How changes are made

1. Behavior is specified in [`openspec/specs/`](openspec/specs/README.md). A change in what Agora does starts with a change to the relevant requirement and its scenarios.
2. Each scenario is enforced by a test. Write the failing test, then the implementation.
3. The spec change, the tests and the code land in one pull request, so the specs always describe what Agora actually does.
4. Explain your reasoning in the pull request description, not in committed files. Ideas for behavior that does not exist yet belong in an issue.

The full working rules are in [AGENTS.md](AGENTS.md); they apply to human contributors too.

## Pull requests

- Use conventional commit messages (`feat:`, `fix:`, `docs:`, ...).
- Keep each pull request to one topic.
- Run `make check` before pushing; it runs what CI runs (see [Checks](docs/development/checks.md)). `make fmt` applies the formatters.
- Do not include private details (hostnames, addresses, paths, credentials) in code, docs or examples.

## License

By contributing, you agree that your contributions are licensed under the [Apache License 2.0](LICENSE).
