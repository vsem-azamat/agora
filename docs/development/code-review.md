# Code review

[Docs](../README.md) / [Development](README.md) / **Code review**

What a reviewer checks in every change, and what the author writes to. How reviews are run (rounds, triage of findings) is in [`AGENTS.md`](../../AGENTS.md#code-review).

- **Typed contracts.** Data crossing a boundary (RPC, protocol lines, storage, the web API) has a declared type: protobuf messages, Go structs, TypeScript types. No untyped maps, `any` or string-encoded state across boundaries. Validate at the boundary, trust inside.
- **No magic values.** Limits, durations, sizes and meaningful strings are named constants or configuration, defined once next to their owner.
- **Consistency.** New code follows the naming, structure, error handling and patterns already used in the repository; one way to do one thing.
- **Specs, docs and code agree.** A behavior change updates the spec scenarios, tests and docs in the same PR, and docs describe the current state only.
- **Tests prove the scenario.** Each new test fails without the code it covers; no assertions that pass regardless.
- **Errors are handled.** No swallowed errors; wrap them with context; user-facing messages say what to do next.
- **Hygiene.** Nothing private goes in (see [Public Repository Hygiene](../../AGENTS.md#public-repository-hygiene)).
