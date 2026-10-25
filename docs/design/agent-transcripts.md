# Design: transcript recording and replay for all backends

Status: proposed, 24 October 2026. Agreed so far: a one-time cleanup to the Codex baseline,
a standard transcript schema, a backend-neutral
`internal/transcript`, and separate record and replay commands per backend.
Steps 1 and 2 are ready to implement; step 2 includes the review correction
that protocol rules belong to the backends. The Claude scenario list,
the handling of Claude's random IDs and the Claude activity detector are
still open.

This document extends the Codex design in `pkg-agent-codex-transcripts.md` to
every backend. That document still describes the format, recording harness,
replay peer and matching rules as implemented today. It stays the reference
for those parts until step 2 below lands; step 2 then folds what remains valid
into this document and removes it.

---

## Problem

Transcript replay covers only Codex. The Claude adapter has scripted-peer
unit tests and live integration tests, but nothing checks it against
recorded protocol traffic, so drift in Claude Code's stream-json protocol is
found only by live runs, which cost quota.

The existing infrastructure cannot be reused as is:

- `internal/transcript` imports `internal/agent/codex`. `Config` uses Codex
  types (`AskForApprovalPolicy`, `SandboxMode`, `ReasoningEffort`,
  `ReasoningSummary`), and `input_read.go` decodes into them. Claude tests
  that import it would build and rerun whenever Codex changes.
- Fixture headers carry Codex names: `codex_version` and
  `developer_instructions`.
- Before the one-time cleanup, Codex fixtures span nine versions (0.133.0 to
  0.163.0). Pruning them reduces this migration's scope. Future upgrades retain
  previous recordings; every retained version remains a live replay input and
  must be migrated with subsequent schema changes.

## Goals

- Transcript files share one schema for all backends. The keys are standard;
  the values come from the backend that recorded the file.
- `internal/transcript` depends on `internal/agent` only.
- Codex and Claude tests do not depend on each other.
- Prune older Codex fixtures once before this migration; future upgrades retain
  previous recordings until an explicit cleanup decision.
- Claude gets recorded replay coverage for the behaviour that scripted-peer
  tests cannot show against the real protocol.

## Non-goals

- Shared scenarios recorded by both backends. Scenarios are per backend; only
  the format and tooling are shared.
- Mapping backend settings (approval policy, sandbox) to neutral concepts.
  Claude's permission model does not map one to one to Codex's
  (`pkg-agent-claude.md`), so a neutral setting would carry meaning that
  neither backend keeps.
- Rewriting Codex replay tests beyond what the schema change requires.
- Upgrade comparison tooling for Claude.

---

## Decisions

| Topic | Decision | Why |
|---|---|---|
| Versions kept | One-time removal of Codex 0.133.0 through 0.140.0 before migration; future cleanup is explicit | Reduces this migration while preserving future recordings for review |
| Upgrade | Record into the new version directory, compare against the current baseline, and retain the old directory after success | Human review and later comparison still need the recordings; they may not yet be committed |
| Schema | Standard header keys with backend-specific values; backend-owned `options` | Files read the same for every backend, following `agent.Settings` |
| Package boundary | `internal/transcript` imports `internal/agent` only | Removes the Codex dependency from shared code |
| Commands | Separate commands per backend: `codex-record`, `codex-replay`, `claude-record`, `claude-replay` | Keeps each backend's tests building only their own peer, and keeps backend setup (Codex version pinning, Claude environment) out of a shared command. Reverses the 16 October decision for one `agent-record` |
| Migration | One mechanical rewrite of the remaining fixtures; no reader for the old keys | Avoids a second format with no clear removal point |

Retained version directories are live test inputs, not an archive:
`TestReplayFixtures` discovers every version, and model transcript tests discover
`*/model-*` cases. Schema migrations must rewrite every retained version's fixtures
and authoring files, including versions added between implementation steps.
The pinned baseline identifies the current supported CLI version; it does not
filter the replay suite.

### Older versions hold no unique scenarios

Checked on 24 October 2026: every case under 0.133.0 to 0.140.0 also exists
under 0.163.0, with the same prompt text. The authoring files differ only in
`model` and, for `reasoning-streaming`, `reasoning_effort`. 0.163.0 adds the
`model-*` cases. No case needs a fresh recording before the older directories
are deleted.

---

## Standard transcript schema

### Output front matter

```yaml
name: approvals_file_change
backend: codex            # agent.Name()
backend_version: 0.163.0  # CLI version that recorded the file
model: gpt-5.6-luna
effort: medium            # optional; the backend's own effort name
system_prompt: "..."      # optional
options:                  # optional; recording setup, validated by the backend
  ask_for_approval: on-request
  sandbox: read-only
actions: [...]
expect: {...}
```

Changes from today:

| Today | Standard |
|---|---|
| (none) | `backend` |
| `codex_version` | `backend_version` |
| `developer_instructions` | `system_prompt` |
| (authoring files only) `reasoning_effort` | `effort` |
| (authoring files only) `ask_for_approval`, `sandbox`, `reasoning_summary` | `options` |

`actions`, `expect` and the JSONL steps are unchanged. The expectations
already use `agent` types only, and the steps are generic `recv`/`send`
envelopes whose payloads are protocol-specific by nature.

### Authoring files

`prompt.md` and `conversation.md` use the same standard keys in their front
matter: `model`, `effort`, `system_prompt` and `options`. The rules for
action files stay as they are.

### Options

`options` is a map from string to string. `internal/transcript` reads and
writes it without interpreting it. Each backend's recorder converts it to its
client options and rejects keys it does not know. Replay ignores `options`;
the recorded steps already contain the behaviour they produced.

Codex options: `ask_for_approval`, `sandbox`, `reasoning_summary`. Claude
options: none yet; they are added with the scenarios that need them.

### Recording setup and observed settings

`model`, `effort`, `system_prompt` and `options` describe how the recording
run was set up. They are not evidence of what the backend actually
established. The recorded protocol responses are the authority for that, and
`expect` asserts it where a scenario cares (the `model-*` cases).

How each adapter applies the setup, checked on 24 October 2026:

| Setting | Codex | Claude |
|---|---|---|
| model | `WithModel`; also sent on the wire | `--model` launch argument |
| effort | `-c model_reasoning_effort` launch argument | no startup option; changed at runtime by `SelectModel` |
| system prompt | `WithSystemPrompt`; sent on the wire | `--append-system-prompt` launch argument |

Replay replaces the launch command (`WithAppServerCommand`, `WithCommand`),
so launch arguments never reach the replay peer. So:

- Replay does not apply `effort` for either backend. Codex replay keeps
  passing only `model` and `system_prompt`, as it does today
  (`internal/agent/codex/replay_test.go`), and its setup does not change.
- Claude replay exercises none of the startup settings on the wire. Unit
  tests on the launch arguments (`claudeArgs`) remain their coverage.

A recorder rejects a scenario that sets a header field it cannot apply at
startup, so a fixture never claims setup that did not happen. The Claude
recorder therefore rejects a nonempty `effort`. A Claude scenario that needs
an effort uses explicit `model-select` actions, which are recorded as a model
selection exchange and asserted like any other action.

---

## Package boundary

The boundary is about protocol knowledge, not only imports. Today two shared
components know Codex's wire protocol without importing it:

- `Observer` (`observer.go`) turns each outbound line into a replay match by
  method: `initialize`, `thread/start`, `turn/start` and `turn/interrupt` keep
  selected fields, everything else is matched in full.
- `TurnActivityObserver` (`turn_activity.go`) signals activity when it
  receives `item/started` with a `userMessage` item. `ModelActions` waits for
  that signal before interrupting a model selection (`model-interrupt`).

Claude's exchange is different (control requests, a replayed prompt with a
uuid), so these rules cannot be shared.

`internal/transcript` keeps what knows no protocol:

- the schema, reading and writing, and the upgrade comparison (`expect` only);
- `Observer` as a recorder of steps, with the match rule supplied by the
  backend: `func(payload any) any`;
- the activity signal (`Reset`, `Wait`, and a method to signal), fed by a
  detector the backend supplies: `func(raw string) bool`;
- `ModelActions`, which drives `agent.ModelSelector` and depends only on the
  activity signal and the `agent.ModelSelector` contract (see below);
- the replay matcher from `cmd/codex-replay/serve.go`, which compares JSON
  values.

Each backend owns:

- its match rule and its activity detector. They are protocol knowledge, so
  they live in the adapter package next to the protocol types, as small
  exported functions, like `codex.IsStderrLogStream` today. Both the recorder
  command and the replay test (`package codex_test`) use them;
- converting the standard header and `options` into client options, and
  rejecting fields it cannot apply;
- its recorder command and replay peer command;
- its replay test and its notice-turn anchor. `cmd/codex-record` and
  `replay_test.go` hard-code `codex:notice-turn`; the shared code takes the
  anchor from the caller.

Fixtures stay in each backend's package:
`internal/agent/<backend>/testdata/transcripts/<version>/<case>/`.

### Model action assertions

`ModelActions.checkSelection` currently requires the requested model and
effort in the outcome even when the selection fails or is interrupted. That
is what Codex reports, but `agent.ModelSelector` does not promise it: a
backend may keep the previous settings or report unknown values after a
failed change (`participant-models.md`). The shared actions assert only the
contract:

| Action | Asserts |
|---|---|
| `model-select` | no error; outcome model equals the requested model; outcome effort equals the requested effort when one was requested (no effort means "select the model only"); outcome settings equal `Settings()` |
| `model-select-failed`, `model-interrupt` | an error; outcome settings equal `Settings()` |
| `model-settings` | `Settings()` labels equal the expected model and effort |

What settings a backend holds after a failed or interrupted selection is
backend behaviour, so a scenario that cares asserts it with an explicit
`model-settings` action after the failing one. Step 2 adds that action to
the Codex `model-failure` and `model-interrupted` cases, with the values
Codex reports today: the requested ones, which the current check already
proves. `model-settings` reads local state and sends nothing, so adding it
does not change the steps.

---

## Implementation steps

Each step is a separate change, so each stays easy to review.

### 1. One-time cleanup to the Codex baseline

- Delete `internal/agent/codex/testdata/transcripts/0.133.0` to `0.140.0`.
- Point the `resolveSelection` test in `cmd/codex-record/main_test.go`, which
  reads the real fixture root at 0.133.0, at the baseline.
- Keep previous recordings after successful upgrades; do not add automatic
  deletion to `scripts/codex-upgrade-next.sh`.
- Keep `scripts/codex-compare.sh` for comparison during upgrades and later review.
- Update the layout and example version in `pkg-agent-codex-transcripts.md`.

Verify: `make test`; the replay suite passes for every retained version (0.163.0
immediately after this cleanup, plus any later upgrades).

### 2. Standard schema and neutral package

- Replace `Config` with the standard fields and `Options map[string]string`.
  Remove the `codex` import from `internal/transcript`.
- Rename the `Output` fields (`Backend`, `BackendVersion`, `SystemPrompt`,
  `Effort`, `Options`).
- Move the replay matcher into `internal/transcript`; `cmd/codex-replay`
  calls it.
- Move the Codex match rule and activity detector into `internal/agent/codex`.
  `Observer` and the activity signal take them as arguments.
- Move the Codex option conversion and validation into `cmd/codex-record`.
- Narrow the failed-selection assertions to the contract above, and add the
  `model-settings` actions to `model-failure` and `model-interrupted`.
- Rewrite fixtures and authoring files in all retained version directories once,
  by script, including any versions added since step 1.
  Front matter only; the steps are not touched.
- Fold the still-valid parts of `pkg-agent-codex-transcripts.md` into this
  document and delete it.

Verify:

- `go list -deps ./internal/transcript` shows no `internal/agent/codex`.
- The non-test files of `internal/transcript` name no protocol method or
  message type (no `thread/`, `turn/`, `item/`, `userMessage`), and its tests
  use synthetic messages and a fake detector rather than Codex traffic.
- The migration preserves the steps exactly: for every fixture, the JSONL
  body after the rewrite is byte-identical to the body before it.
- The moved Codex match rule gives the same matches as before: a table test
  with fixed outbound lines for each normalized method (`initialize`,
  `thread/start`, `turn/start`, `turn/interrupt`) and one other method. The
  activity detector gets the same kind of test.
- The Codex replay suite and model transcript tests pass for all retained versions.
- A live re-recording (on request) cannot reproduce the steps, since IDs,
  output and stream fragmentation change between runs. It checks that the
  file is in the new schema, that it replays, and that
  `codex-upgrade-compare` against the migrated fixture passes.

### 3. Claude recording and replay

- `cmd/claude-record` and `cmd/claude-replay` on top of `internal/transcript`.
- A Claude match rule and activity detector in `internal/agent/claude`.
- `internal/agent/claude/testdata/transcripts/<claude-version>/`.
- A replay test in `internal/agent/claude`, using `WithCommand`.
- Recording uses the installed Claude Code version, haiku, and runs only when
  the human asks.

Verify: the Claude replay suite runs in `make test` without Claude Code
installed; `go list -deps` for the Claude test package shows no
`internal/agent/codex`.

---

## Open questions

These are for step 3 and do not block steps 1 and 2.

1. **Capability requirements.** Which behaviours the `agent.Agent` contract
   promises, marked "both backends" or "backend-specific". The Claude
   scenarios follow from that list. A candidate first set: success, failure
   with a provider explanation, interrupt, approval allow and deny, notice,
   model change. Self-started turns and unmatched replays stay with the
   scripted-peer tests, since they are hard to trigger reliably live.
2. **Random IDs.** The Claude client generates a session ID (`--session-id`,
   echoed in `system/init`) and a uuid per prompt, and matches the prompt's
   replay by that uuid. A replay peer sending the recorded uuid would not
   match the new one. Options: inject the IDs in the replay test (a seam for
   prompt IDs exists as `newPromptID`), or teach the matcher to capture a
   value from a received line and substitute it into later sent lines.
   Claude's `request_id` values come from Claude's own lines, so the client
   only echoes them and they should need neither. To check against a real
   recording.
3. **Claude version in the path.** Claude Code is not pinned. The directory
   name is the version that recorded the baseline. Recording on a newer version
   creates a new directory and retains the previous one until explicit cleanup.
4. **Claude activity detector.** Which received line shows that a Claude model
   selection is in progress and can be interrupted, or whether
   `model-interrupt` applies to Claude at all. Decide with the scenario list.
