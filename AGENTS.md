# coderoom: Agent Instructions

This repo is **coderoom**: a CLI-native multi-agent coding workspace. Agents collaborate through a shared git worktree; `git diff` is the shared language of change.

## Read First (design + expectations)

- Product concept: `docs/design/concept.md`
- System architecture: `docs/design/architecture.md`
- Coding + review style guide (authoritative): `docs/code-review.md`

## Commands

```
make build              # build ./cmd/coderoom
make lint               # run golangci-lint
make test               # unit tests
make test-integration-lint # lint with the integration build tag
make test-integration   # integration lint + tests (requires Codex and Claude Code installed)
make test-integration-codex   # Codex adapter integration tests only
make test-integration-claude  # Claude adapter integration tests only (live, uses quota)
make pre-commit         # lint + test
make test-all           # lint + test + test-integration
make codex-upgrade-next # advance pinned Codex version by one stable release
make codex-upgrade-last # jump directly to the latest stable Codex release
make install-hooks      # install pre-commit hook
```

## Experiments / Playground

We keep research notes in `docs/playground/` and runnable prototypes in `playground/`.

- Synthesis doc: `docs/playground/research-llm-cli.md`
- Run a POC from the repo root: `make pg-codex-stdio`, etc. (targets in `playground/Makefile`)

Key findings to keep in mind when implementing:
- Both agent CLIs are driven over stdio pipes, with no PTY: Codex through `app-server` (see `docs/playground/poc-codex-stdio.md`), Claude Code through bidirectional stream-json (see `docs/playground/poc-claude-stream-json.md`). Their interactive modes assume a TTY; these machine modes do not.

## Code Style (follow `docs/code-review.md`)

Highlights:
- Keep functions roughly one screen; top-level functions should read like algorithms via named helpers.
- Naming must signal side effects: `Get*/Read*/Parse*` are pure-by-contract; `Create*/Update*/Start*` imply effects.
- Prefer early returns; keep error handling local (`if err != nil { return err }`).
- Tests: use table-driven tests for input variants; don’t assert on error strings; prefer fakes over mocks.
- Tag integration tests with `//go:build integration` and run them separately (they require external CLIs).

## Working Agreement

- Make minimal, focused changes aligned with the design docs.
- Don’t auto-commit; leave staging/commits to the human unless explicitly asked.
- If a change impacts architecture/UX, update the relevant doc under `docs/` alongside the code.

