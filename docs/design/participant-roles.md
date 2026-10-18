# Participant Definitions

## Goal

Introduce durable participant configuration under `.coderoom/` so a named
participant can have a stable identity and optional role outside a single
`/invite` command.

This change is intended to support participant-aware and role-aware system
prompt synthesis first. It does **not** introduce capability enforcement,
initiative policy, or participant-specific prompt overrides in the initial
implementation.

---

## Motivation

Today, `/invite <alias>` starts a participant directly from command input.
That is sufficient for process lifecycle, but weak for product semantics:

- participant identity is not persisted in the repo
- role selection is too transient for a multi-agent workspace when a role is
  desired
- role behavior cannot be inspected or reviewed as project configuration
- prompt construction risks being scattered across session code

coderoom's design already treats participants as named collaborators with
stable identity and a behavioral contract. Persisting participant definitions in
the repo makes that model explicit.

---

## Non-goals

Version 1 does not attempt to solve:

- capability enforcement
- initiative configuration
- display color configuration
- participant-specific prompt fragments

This is intentionally a narrow slice:

- define participant identity in YAML
- define role behavior in Markdown prompt files
- synthesize a system prompt from those sources when available

---

## File Layout

Participant definitions live in the repo-local `.coderoom` directory:

```text
.coderoom/
  participants/
    ada.yaml
    turing.yaml
  prompts/
    roles/
      builder.md
      reviewer.md
      tester.md
      architect.md
      security-reviewer.md
```

The split is deliberate:

- `participants/*.yaml` is machine-validated configuration
- `prompts/roles/*.md` is human-authored behavioral guidance

Version 1 does not include `prompts/participants/*.md`.

---

## Participant YAML

Each participant is defined by one YAML file:

```yaml
alias: ada
role: reviewer
```

Supported fields in V1:

- `alias`
- `role`

No additional fields are read in the initial implementation. A participant
definition is optional; `/invite <alias>` may still be used without a matching
YAML file.

### Validation

All participant files are loaded and validated once, at startup (see
`docs/design/multi-backend-config.md`). Loading fails fast when:

- the file is malformed YAML
- `alias` is empty
- the filename stem does not match `alias`
- `role` is present but `.coderoom/prompts/roles/<role>.md` does not exist

If `role` is omitted, empty, or `null`, Version 1 treats that the same as no
role configured.

The loader should return structured validation errors where possible, but the
user-facing requirement is simple: the failure should clearly identify the bad
participant file and the invalid field.

---

## Role Prompt Files

Role prompts are file-backed. A role is valid if and only if the corresponding
Markdown file exists:

- `.coderoom/prompts/roles/<role>.md`

These files define the behavioral contract for the role. Version 1 does not
impose a built-in role list in configuration; the prompt files are the source
of truth for which roles are available in a repo, and the YAML role string is
the source of truth for the participant's configured role at runtime.

Example `reviewer.md`:

```md
You are the reviewer in a multi-agent coding room.

Focus on:
- correctness issues
- edge cases
- regressions
- missing tests
- deviations from the stated design

Prefer concise findings with concrete evidence from the current diff.
Do not implement code changes unless explicitly asked.
```

If a YAML file references a role whose prompt file is missing, invite should
fail with a clear error rather than silently omitting role behavior from the
system prompt.

---

## Runtime Model

This design does not replace the current session and participant model.
Instead, it becomes an input layer for existing runtime types, while making the
configured role a file-backed string rather than a hardcoded role set.

Current runtime fields already exist on `participant.Participant` and
`session.InviteCommand`:

- alias
- role

The participant definition loader should map directly onto those fields after
validation when a definition file exists. The runtime role value should come
from the YAML definition and should not be constrained by a built-in enum or
hardcoded role list. Role validity comes from the presence of the corresponding
role prompt file.

Version 1 assumes a single backend: `codex`. Backend selection is not part of
participant configuration in this design.

The session remains the owner of:

- participant lifecycle
- agent startup and teardown
- message routing
- runtime participant state transitions

The definition files do not move those responsibilities into configuration.

---

## Invite Flow

Version 1 keeps the command shape:

```text
/invite <alias>
```

But the semantics change from:

- "start an ad hoc participant named `<alias>` with identification only"

to:

- "load `.coderoom/participants/<alias>.yaml` if it exists, then start that
  participant"

Invite flow:

1. at startup, system loads and validates every participant file and the role
   prompts they reference
2. user runs `/invite ada`
3. system takes ada's configuration from what was loaded at startup
4. if ada's files changed since startup, system shows a notice and continues
   with the loaded configuration
5. system synthesizes the participant's system prompt from the available data
6. system passes that prompt into agent startup
7. system starts the agent

Version 1 is permissive. If the participant file does not exist, invite still
succeeds using identification-only prompt synthesis.

The system should emit a record indicating whether it:

- loaded participant configuration from disk
- started with identification only because no participant definition existed

---

## Prompt Synthesis

The system prompt should be assembled from at most three layers:

1. identification
2. data from participant YAML
3. role prompt from `.coderoom/prompts/roles/<role>.md`

Identification is always included. Suggested form:

```text
Your name is ada.
You will be referred to as ada or @ada.
```

If a participant definition exists, the system may include a short generated
statement derived from YAML fields. Example:

```text
You are a builder.
```

If a role is configured, the corresponding role prompt file is appended after
the identification and YAML-derived statements.

The synthesized participant prompt is startup configuration, not a normal room
message. For Codex, Version 1 should deliver it during `thread/start` using
thread-scoped developer instructions rather than sending it as the participant's
first conversational turn.

If no participant definition exists for an alias, the system prompt should
include only identification. Version 1 should not invent default role behavior
or silently fall back to a missing role prompt.

Version 1 should not support participant-specific prompt files or additional
global prompt layers. If richer prompt composition becomes necessary later, it
should be added explicitly rather than overloading the YAML file with prose
instructions.

---

## Why YAML Plus Markdown

The split between YAML and Markdown is intentional.

YAML is a better fit for participant identity because:

- fields can be validated directly
- diffs stay compact
- command/runtime mapping is straightforward
- future schema extension is easy

Markdown is a better fit for role prompts because:

- prompt content is prose, not machine state
- reviewers can read and edit role behavior naturally
- prompt files can evolve without changing config parsing

Putting prompt text into participant files would couple structured config with
free-form instructions too early.

---

## Package Boundaries

The likely implementation split is:

- a repo-local loader for `.coderoom/participants/*.yaml`
- a repo-local loader for `.coderoom/prompts/roles/*.md`
- session/agent startup consumes loaded definitions, not raw files

The important boundary is:

- file loading and validation happen before agent start
- session works with validated runtime values
- prompt synthesis happens once per participant startup
- agent startup receives the synthesized prompt as startup configuration, not as
  a user-visible turn

This preserves the current design where session coordinates runtime behavior and
participant owns transition invariants.

---

## Future Extensions

This design intentionally leaves room for later additions:

- `initiative`
- `capabilities`
- `color`
- participant-specific prompt overlays
- capability enforcement at the session layer
- backend-specific prompt assembly rules

Those should extend the same participant definition model rather than invent a
second source of truth.

For now, the minimum useful feature is durable participant definitions plus
role-based prompt synthesis.
