# coderoom

> Script your agents like you script your shell.

coderoom is an interactive, scriptable shell for coding agents. Invite named
agents backed by Codex or Claude Code, give them focused tasks, and review their
work in one terminal room.

Each agent has its own conversation. All agents work on the same files.

## Work together

Start `coderoom` from your repository, then enter:

```text
/invite ada
@ada Fix the failing parser test. Keep the change focused.
```

After Ada finishes, ask another agent to review the shared diff:

```text
/invite turing
@turing Review the current git diff for correctness and missing tests. Leave files unchanged.
```

Use `/handoff ada turing` to send Ada's latest completed output to Turing as
additional context. Agents receive context through messages and handoffs. You
inspect the changes with your usual git tools.

## Save what repeats

In a Go project, save a test command and use it to check an agent's attempts:

```text
/def tests /shell go test ./...
/loop @ada Make the tests pass without weakening them /until /tests /max 3
```

coderoom waits for each agent turn, then runs `/tests`. A passing check ends the
loop. A failed check supplies evidence for the next attempt. This example allows
up to three turns.

You enter the commands and set the limits. Agent responses stay text; coderoom
executes commands submitted by you.

## Get started

Release archives are available for macOS and Linux, on AMD64 and Arm64. Install
the dependencies for Codex or Claude Code, then start a room in your repository.

- [Install coderoom](docs/man/install.md)
- [Your first room](docs/man/first-room.md)
- [Coming from Codex](docs/man/coming-from-codex.md)
- [Coming from Claude Code](docs/man/coming-from-claude.md)
- [Participants, roles, and backend selection](docs/man/participants.md)
- [Full manual and command reference](docs/man/index.md)

## Current scope

coderoom is early-stage. It supports one shared room, named Codex and Claude Code
participants, addressed messages, broadcasts, handoffs, model selection,
approvals, shell commands, saved shell-backed commands, and bounded loops.

Definitions take no arguments and last for the running room. A loop uses one
agent prompt and one saved shell command as its condition. New room launches
start fresh conversations and room settings.

## For contributors

- [Product concept](docs/design/concept.md)
- [Architecture](docs/design/architecture.md)
- [Code review guide](docs/code-review.md)
- [Prompt-language design and implementation status](docs/design/prompt-language.md)
- [coderoom overview and engineering ideas](https://g18a.com/coderoom)

## Development

```bash
make test          # quick test suite
make pre-commit    # golangci-lint and test suite with -race
make test-all      # full suite, including integration tests
```

Integration tests (require external CLIs):

```bash
make test-integration
```

To upgrade Codex directly to a specific stable release, recording fresh
fixtures and running the upgrade checks against the current pinned version:

```bash
TARGET_CODEX_VERSION=0.163.0 ./scripts/codex-upgrade-next.sh
```

This runs live agent calls and consumes quota. The pin changes only after all
checks pass. Without the target option, the script advances one stable release;
`make codex-upgrade-next` runs that single-step upgrade.

Use `make codex-upgrade-last` to jump directly to the latest published release,
recording and validating only that release against the current pin. It succeeds
without recording when the latest version is already pinned.
