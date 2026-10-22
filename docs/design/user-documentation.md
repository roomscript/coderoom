# New-user documentation

Status: initial manual draft prepared under `docs/man/`.

The draft includes installation, a first-room walkthrough with an optional
scripting step, separate migration guides, shared room concepts, participant
configuration, and individual command pages. The README links into the manual.
Command implementations contain relative links to their reference pages.

Backend selection rules belong to `participants.md`; installation shows only
the launch choices needed to get started. Model command behavior belongs to
`model.md`. The previous participant-guide path links to the new page.

Review checks cover local links, anchors, index coverage, and syntax agreement
with command help. Backend launch and behavior claims were checked against code;
upstream setup links were checked against official documentation. Existing UI,
interpreter, session, and echo-agent tests pass. Live Codex and Claude sessions
now supply recorded command examples under `docs/man/examples/`. They check
basic delivery, cancellation, and loop behavior. The complete repository-edit
and review walkthrough has not been run on both backends.

## Command documentation and code

Keep authoritative command pages together under `docs/man/`. This lets readers
and reviewers browse the manual as a whole and check consistency without
navigating implementation directories. Do not duplicate pages next to code or
add tooling to assemble them from scattered sources.

When writing each command page, add a relative documentation link near its
implementation, for example in `internal/interpreter/std/command_cancel.go`:

```go
// User documentation: ../../../docs/man/cancel.md
```

Use the same approach for UI-owned commands such as `/quit`, adjusting the
relative path. Add links when their target pages exist.

Command behavior changes should include review of the linked page. An unchanged
page can be correct, so do not require a documentation edit for every code edit.
The link is a visible review reminder, not automatic detection of semantic drift.

## Audience and goal

Readers already use Codex CLI or Claude Code. Explain coderoom's coordination
model and controls without teaching agent-assisted coding from scratch.
The first successful outcome is a reader starting a room in their repository,
inviting an agent on their preferred backend, and requesting a small change.
The next outcome is a second participant reviewing the shared diff.

## Proposed structure

| Location | Purpose |
| --- | --- |
| `README.md` | Explain the product, show one short collaboration example, link to installation, first room, migration guides, and reference. Keep development links separate. |
| `docs/man/index.md` | Manual index grouped by getting started, collaboration, configuration, and commands. |
| `docs/man/install.md` | Release installation, source build, backend dependencies and authentication prerequisites, backend selection, and checks for common setup failures. |
| `docs/man/first-room.md` | One complete walkthrough: launch from a repository, invite, send, invite a reviewer, review the diff, cancel if needed, and exit. |
| `docs/man/coming-from-codex.md` | Explain differences from the Codex terminal experience and link to shared concepts and reference. |
| `docs/man/coming-from-claude.md` | Explain differences from the Claude Code terminal experience and link to shared concepts and reference. |
| `docs/man/room.md` | Shared worktree, participant identity and context, message routing and visibility, busy participants and waiting input, approval interaction, cancellation, and session lifetime. |
| `docs/man/<command>.md` | Reference for each public room command. Use `send.md` for addressed input and `broadcast.md` for plain text. |
| `docs/man/participants.md` | Participant files, role prompts, backend defaults, configuration loading, and model configuration. Migrate the existing participant guide here. |

Keep a link at `docs/participants.md` when moving its contents so existing links
still lead to the guide. Maintain configuration instructions in one place.
Do not reorganize engineering or playground documentation as part of this work.

## Content boundaries

The README should answer what coderoom adds, what works today, and where to
start. Move detailed installation, role setup, and command semantics into the
manual. Keep the example small; introduce definitions and loops after basic
collaboration works.

Migration guides should answer concrete expectations: how a named participant
maps to an agent conversation; which commands belong to coderoom; what backend
configuration is used; how instructions, models, tools, and approvals behave;
what other participants receive; and what survives exit or reinvitation.
Verify each backend-specific statement against its adapter. Do not imply that
the native interactive CLI interface is embedded or that all of its features
are available. Link shared behavior to `room.md` rather than maintaining two
copies.

Installation must let readers choose either backend without installing both.
Explain the built-in Codex default, `CODEROOM_BACKEND=claude`, and per-participant
backend selection. Current code launches Codex through `npx` and Claude through
`claude` on `PATH`; distinguish this from simply having a native CLI installed.
Link upstream installation and login instructions after checking official docs
when authoring the manual. Do not duplicate upstream setup documentation.

Each command page should contain purpose, syntax, a minimal example, observable
behavior, relevant waiting/failure/cancellation behavior, scope or persistence,
and links to related commands. Include backend differences only where they
affect the user. Explain how the command behaves rather than its internal Go
implementation. Avoid empty sections where a category does not apply.

## Writing style

Write plain, factual sentences. Say what a command does and what the reader
will see. Avoid rhetorical constructs such as "it is not A, it is B". Mention
what something does not do only when readers would otherwise assume it, and
state that as a separate fact. Prefer short sentences and common words.

## Examples and media

Show short interactions that establish the situation, use the command, and
explain the result. For cancellation, start a task and cancel it while the
agent is working. Capture actual output from the app and link each excerpt to
its source transcript. Preserve the captured wording and order. Do not invent
agent replies or activity lines. Use input-only blocks for walkthrough steps
that have no recording.

Record the date, backend versions, and capture method with the
transcripts. State which examples use live agents and which use echo agents.
Refresh a recording when its documented behavior changes. Agent wording and
model availability can vary between runs.

Use copyable command syntax and interaction fragments as the baseline. A short
recording on the README or first-room page can later show simultaneous activity,
waiting input, and approvals where motion helps. Do not require a video for
every command.
Recordings supplement the written instructions and should identify the release
they demonstrate. Add media only after the walkthrough and terminal behavior
are stable enough to maintain.

## Implementation sequence

1. Inventory public commands from the interpreter's help catalog and UI
   application commands. Verify behavior through implementations and tests;
   design documents include intended behavior as well as implemented behavior.
2. Write installation, first-room, room concepts, and participant setup. Preserve
   the existing participant link during migration.
3. Write both migration guides against the shared pages. Check claims about
   instructions, approvals, context, persistence, and backend configuration
   against the code rather than assuming native CLI parity.
4. Write command pages and the manual index. Cross-check syntax with `/help`.
5. Shorten and refresh the README, including current Claude support and the
   command index. Link developer material separately.
6. Walk through both backend onboarding paths and check relative links and
   example syntax. Check keyboard controls against their handlers and tests as
   well as help text; the manual and help can share the same outdated claim.
   Live agent verification consumes quota and should be an
   intentional part of documentation validation.

## Acceptance criteria and risks

- A reader can use either supported backend without reading engineering docs.
- A walkthrough explains both the commands to enter and the result to expect.
- Readers understand shared file edits, separate agent conversations, actual
  message delivery, and the effects of cancellation and exit.
- Every public command is discoverable from the index and agrees with help.
- Configuration and shared room behavior each have one authoritative user page.
- Current functionality is distinguished from design aspirations. Check claims
  against implementations and behavior tests, including backend support and
  command availability.
- Links, examples, and backend-specific claims are checked before publication.

The main maintenance risk is drift between help, code, and prose. Keep command
syntax checks in the documentation review process; defer documentation
generation or a new publishing toolchain until a concrete need warrants it.

## Product decisions

Handoff previews and structured delivery events own handoff observability.
Expected availability errors use plain messages and preserve their causes.
The record renderer owns indentation for wrapped and unwrapped text.

See [handoff observability](handoff-command.md#handoff-observability-v2) and
[inline formatting](pkg-ui-inlinefmt.md) for the lasting design details.
