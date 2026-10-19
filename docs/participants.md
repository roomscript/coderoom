# Participant Setup

This document describes how repo-local participant and role configuration works
in coderoom.

For design rationale and runtime integration details, see
`docs/design/participant-roles.md`.

## Overview

coderoom separates two concerns:

- a **participant** is the named collaborator you invite into the room
- a **role** is the behavioral prompt attached to that participant

Participant definitions are stored in YAML. Role prompts are stored in
Markdown. Both live under the repo-local `.coderoom/` directory.

## File Layout

```text
.coderoom/
  participants/
    ada.yaml
    tim.yaml
  prompts/
    roles/
      builder.md
      reviewer.md
```

- `participants/*.yaml` defines participant identity and role selection
- `prompts/roles/*.md` defines the corresponding role behavior

Role names used in `participants/*.yaml` must have a matching prompt file in
`prompts/roles/`.

## Participant Definitions

Each participant is defined by one YAML file:

```yaml
alias: ada
role: builder
```

Supported fields:

- `alias`
- `role`
- `backend`

Rules:

- the filename stem must match `alias`
- `alias` must not be empty
- `role` is optional
- if `role` is set, `.coderoom/prompts/roles/<role>.md` must exist

If `role` is omitted, empty, or `null`, the participant starts without a role
prompt.

## Backends

`backend` selects the agent CLI that runs the participant:

- `codex`: OpenAI Codex, run through `npx`.
- `claude`: Claude Code, the `claude` command on `PATH`.

Each backend uses the login and default model configured in its own CLI.

```yaml
alias: tim
role: reviewer
backend: claude
```

When a file does not set `backend`, or an alias has no file, the participant
uses the default: the `CODEROOM_BACKEND` environment variable if set, otherwise
`codex`. The environment variable is per user, so people sharing a repository
can use different defaults.

The room shows each participant's backend when it starts, and `/who` lists it:

```text
[ada starting · codex]
[agents] ada (codex), tim (codex)
```

After startup, coderoom checks in the background that the tools each backend in
use needs are installed. A failure appears in the room and does not block
anything:

```text
[codex unavailable: npx is installed: not found in PATH]
[claude unavailable: claude is installed: not found in PATH]
```

## Role Prompts

Role prompts are plain Markdown files. Keep them short, concrete, and focused
on the behavior you want in this repository.

Example:

```md
You are a reviewer.

Review changes critically for correctness, regressions, and missing coverage.
Prioritize real defects and risky assumptions over style feedback.
```

The prompt file is the source of truth for what that role means in the repo.

## Loading at Startup

coderoom reads every file in `.coderoom/participants/` and the role prompts they
reference once, when it starts. Files that do not end in `.yaml`, and
subdirectories, are ignored.

If any file is invalid, coderoom does not start. It lists every broken file,
not only the first:

```text
coderoom: configuration errors:
  .coderoom/participants/ada.yaml: invalid participant yaml: ...
  .coderoom/participants/tim.yaml: role prompt not found: ...
```

Startup fails when:

- the YAML is malformed or has unknown keys
- `alias` is empty
- the filename does not match `alias`
- the configured role prompt file is missing
- a participant file or `CODEROOM_BACKEND` names an unknown backend

These failures should be fixed in repo configuration rather than worked around
at runtime.

## Invite Behavior

When you run:

```text
/invite ada
```

coderoom builds the participant startup prompt from the configuration loaded at
startup (identity plus role prompt) and starts the agent.

If `.coderoom/participants/ada.yaml` does not exist, invite still succeeds with
identification-only setup.

## Editing During a Session

Configuration is not reloaded. If `ada.yaml` or the role prompt it uses changed
since startup (edited, deleted, or created for an alias that had no file),
`/invite ada` shows a notice and continues with the loaded version:

```text
[ada.yaml changed since startup; using the loaded version. Restart coderoom to apply it]
```

The check only compares file contents; a broken edit does not block the invite.
Restart coderoom to apply changes.

## Current Repository Example

This repository currently defines:

- `.coderoom/participants/ada.yaml` with role `builder`
- `.coderoom/participants/tim.yaml` with role `reviewer`
- `.coderoom/prompts/roles/builder.md`
- `.coderoom/prompts/roles/reviewer.md`
