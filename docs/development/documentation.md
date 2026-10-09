# Documentation

[Docs](../README.md) / [Development](README.md) / **Documentation**

`docs/` is a tree of engineering references that describe Agora as it is now.

## Structure

- Every directory has a `README.md` index that links every page and subdirectory in it.
- Every page except `docs/README.md` has a breadcrumb line within its first lines, starting with `[Docs](`.
- A new page is added to its directory index in the same pull request.
- Relative links must resolve.

## Content

- Current state only: no history, decision records, plans, roadmaps or status notes.
- Product behavior belongs in `openspec/specs/`, not here.
- Examples use neutral placeholders (`node-a`, `~/src/example-app`), never real hostnames, addresses, paths or credentials.

`scripts/docs-check.mjs` enforces the structure rules. See [Checks](checks.md).
