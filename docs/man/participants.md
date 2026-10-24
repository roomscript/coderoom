# Participants and roles

A participant is a named agent in the room. A participant file selects its
backend and an optional role prompt. An alias works without a file.

coderoom reads configuration from the directory where you launch it:

```text
.coderoom/
  participants/
    ada.yaml
    turing.yaml
  prompts/
    roles/
      builder.md
      reviewer.md
```

## Participant files

For `.coderoom/participants/ada.yaml`:

```yaml
alias: ada
role: builder
backend: codex
```

For `.coderoom/participants/turing.yaml`:

```yaml
alias: turing
role: reviewer
backend: claude
```

Supported fields are `alias`, `role`, and `backend`. The filename stem must
match `alias`. The role and backend are optional. An omitted, empty, or null role
starts the agent with identity instructions only.

Files define settings. Use [`/invite`](invite.md) to start the participants.

## Backend selection

The backend is selected in this order:

1. The participant file's `backend`, if set.
2. The `CODEROOM_BACKEND` environment variable, if set.
3. The built-in default, `codex`.

The supported backends are `codex` and `claude`. Each uses its CLI's login and
configured model defaults. See [installation](install.md) for dependencies.

For example, launch with a Claude default:

```bash
CODEROOM_BACKEND=claude coderoom
```

An alias without a file then uses Claude. A participant with `backend: codex`
still uses Codex. To set your usual default, export `CODEROOM_BACKEND` in your
shell configuration. A shared participant file supplies the same explicit
backend to everyone using that file.

The room shows the backend at startup. [`/who`](who.md) lists each participant
and its backend.

## Role prompts

Create `.coderoom/prompts/roles/builder.md`:

```markdown
You are a builder.

Implement the requested change. Follow existing code patterns.
Keep edits focused and explain how you checked them.
```

Create `.coderoom/prompts/roles/reviewer.md`:

```markdown
You are a reviewer.

Review the current diff for correctness, regressions, and missing coverage.
Report concrete findings. Leave files unchanged.
```

Role prompts describe behavior. Backend tool permissions determine what tools
can do. A reviewer role alone does not enforce read-only filesystem access.

coderoom supplies the participant's name plus its role text as startup
instructions. Codex receives these as thread developer instructions. Claude
receives them through an appended system prompt. The backend also handles its
own repository instructions.

## Models and effort

Each participant starts with the model and effort configured by its backend.
The room shows these when it joins, with its role, backend and resume ID. Unreported values appear as `codex-managed`
or `claude-managed`.

Use [`/model`](model.md) to inspect or change a participant's current settings.
Changes keep the conversation and last until removal. They leave participant
files and CLI configuration unchanged. To choose defaults for future
invitations, configure the backend CLI outside the room.

## Loading and changes

coderoom reads all `.yaml` files in `.coderoom/participants/` and their role
prompts once at startup. Subdirectories and files with other extensions are
ignored. New files and edits take effect after restarting coderoom.

If a participant file or its role prompt changes during a room, `/invite`
shows a notice and uses the loaded version. Creating a file for an alias that
previously had none also needs a restart. A broken edit made during the room
does not block invitation using the loaded version.

## Validation

Startup reports configuration problems together and exits before opening the
room. Check every path listed in the error.

Startup rejects:

- Malformed YAML or unknown fields.
- An empty or invalid alias, or a filename that does not match it.
- Invalid role names or missing referenced role prompts.
- An unknown backend in a participant file or `CODEROOM_BACKEND`.

To invite an unconfigured participant, simply enter `/invite <alias>`. Its name
is supplied to the backend and the default backend is used.

[Manual](index.md)
