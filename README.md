# coderoom

> Script your agents like you script your shell.

coderoom is an interactive, scriptable shell for coding agents. Work with named
agents in a shared terminal room. One writes, another reviews, and you decide
where to dig deeper. Capture recurring interactions as commands.

## Work live. Script what repeats.

Start with a conversation:

```text
/invite ada
/invite turing
@ada investigate the failing tests
@turing review Ada's changes for edge cases
```

Then capture a repeated check:

```text
/def tests /shell go test ./...
/loop @ada make the tests pass without weakening them /until /tests /max 3
```

After each turn, coderoom runs `/tests`. If they fail, Ada gets the output and
tries again. The loop ends when the tests pass or after three turns. Every step
stays visible in the room.

Like shell aliases and scripts, your room grows around the way you work.

## Keep iterations short

Use agents to clarify requirements, question assumptions, and review focused
changes. Short loops give you room to examine architecture, testability, and
trust boundaries while building your own understanding of the system.

Split review by role when useful: one agent checks tests and clarity, another
examines architecture, and another looks for security issues. Agents surface
material for scrutiny; you bring the engineering context and make the decisions.

## Your agents. Your rules.

You write the commands and set their limits. coderoom never interprets an agent's
text output as a room command. Commands run only when you enter them directly
or invoke a definition you created.

---

## Try it

You need Node.js with `npx` and a working Codex CLI setup. Support for other
agent CLIs is planned.

### Download a release

Prebuilt archives are published on the [GitHub Releases](https://github.com/roomscript/coderoom/releases/latest) page.

Choose the archive for your platform, extract it, and run `coderoom`:

```bash
tar -xzf coderoom_<version>_<os>_<arch>.tar.gz
./coderoom
```

Archives are available for macOS and Linux on Arm64 and AMD64. Checksums are
published with each release as `checksums.txt`.

### Build from source

You will also need Go (see `go.mod`). Then run:

```bash
make build
./bin/coderoom
```

If needed, check the Codex setup with `npx @openai/codex app-server`.

### Start a room

Invite an agent and send it a message:

```text
/invite ada
@ada implement a small change: ...
```

`ada` is the name you use to address that agent in the room. All agents work in
the same git workspace. Invite more agents when you want to divide work or ask
one to review another.

## Give agents roles

An invitation works without any configuration. To give an agent a reusable
role, add a participant definition and role prompt:

```text
.coderoom/
  participants/
    ada.yaml
  prompts/
    roles/
      builder.md
```

Minimal participant definition:

```yaml
alias: ada
role: builder
```

Minimal role prompt:

```md
You are a builder.

Implement the requested change directly in the codebase.
Follow existing code patterns and keep edits minimal, focused, and practical.
```

See [`docs/participants.md`](docs/participants.md) for the full setup and
validation rules.

## What works today

coderoom is early-stage. Today it provides one shared room, named agents backed
by Codex app-server, direct messages, broadcasts, handoffs, shell commands,
reusable command definitions, and bounded loops.

Definitions last for the current room and do not accept parameters. A loop can
send one prompt to an agent and use one shell-backed command to decide when it
is done. Nested and concurrent loops are not yet supported.

## Commands

```text
/invite <alias>                         # start an agent
@<alias> <prompt>                       # send to one agent
<prompt>                                # broadcast to all agents
/handoff <from> <to>                    # transfer latest output
/shell <program>                        # execute a shell program
/def <name> /shell <program>            # define a reusable command
/<name>                                 # invoke a defined command
/loop @<alias> <prompt> /until /<name> /max <turns>
/who                                    # show participants
/cancel <alias>                         # interrupt current work (best-effort)
/remove <alias>                         # stop and remove an agent
/policy enable send-notices             # notify other agents after @alias sends
/help                                   # show commands
/quit                                   # exit
```

With one agent in the room, plain text goes to that agent.

Direct `@alias` sends notify only the addressed agent by default. Run
`/policy enable send-notices` to also send listener notices to the other
participants for the remainder of the room.

---

## Learn more

- [coderoom overview and engineering ideas](https://www.trigosec.com/coderoom)
- [Prompt language](docs/design/prompt-language.md)
- [Participant roles](docs/design/participant-roles.md)
- [Architecture](docs/design/architecture.md)

---

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
