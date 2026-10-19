# Package design: Codex transcript recording and replay

This document defines a deterministic test strategy for `internal/agent/codex`
based on recorded JSON-RPC transcripts.

The goal is to move most Codex protocol behavior tests out of the live
integration suite and into the normal test suite, while keeping a small number
of real-Codex compatibility tests.

---

## Problem

Current integration tests launch a fresh Codex process per test case.

This has three costs:

- slow bootstrap (`npx`, process startup, handshake)
- brittle dependence on live Codex behavior in every test
- unclear ownership of failures: our adapter logic vs upstream Codex changes

Many existing tests are verifying adapter behavior that is deterministic once
the wire protocol is fixed:

- handshake sequencing
- turn lifecycle handling
- approval request normalization and response generation
- output / reasoning / file-change message translation
- buffering and ordering across stdout notifications

Those tests do not need a live Codex process every time.

---

## Goals

- Define one structured transcript format for recording and replay.
- Record transcripts at the same boundary already exposed by
  `ProtocolObserver`: raw JSON-RPC lines sent and received.
- Record high-level assertions from interaction at the `agent.Agent` boundary
  during the same live run.
- Add a replay peer process that can replace Codex app-server in tests.
- Run replay-based tests in the default test suite.
- Keep a small live integration suite for upstream compatibility checks.
- Start with one seed scenario derived from `TestApprovals_fileChange`.

## Non-goals

- Replacing all live Codex tests immediately.
- Building a general-purpose protocol simulator with arbitrary branching.
- Encoding every field of every message as an exact string match.
- Reusing the existing human-oriented wire log format as the fixture format.

---

## Test classification

### Replay tests

Replay tests use a recorded transcript plus a replay peer process. They are:

- deterministic
- fast enough for the normal test suite
- focused on adapter behavior

They do **not** require a real Codex installation at test time.

### Live integration tests

Live integration tests still launch real Codex. They remain under
`//go:build integration` and answer a narrower question:

- does the currently targeted Codex version still work with this adapter?

The live suite should be small. A broader smoke scenario is preferred over many
small, overlapping live tests.

The broad transcript sweep is protocol-oriented. Its purpose is to detect
adapter/protocol regressions against a live Codex build, not to validate every
case-specific semantic behavior. In particular, thread-scoped startup
configuration such as `developerInstructions` should be covered by dedicated
live integration tests that start a fresh client for the scenario under test,
rather than by the shared transcript sweep.

---

## Boundary: what is recorded

The fixture is captured from two boundaries during one live recording pass.

### Protocol boundary

The JSONL step stream is captured at the raw JSON-RPC line boundary, exactly
where `ProtocolObserver` observes traffic:

- `OnSend(msg string)`
- `OnReceive(msg string)`

This is the correct boundary because it captures the actual stdio protocol
without depending on our parsing logic.

### Agent boundary

The YAML front matter is captured by a recording harness interacting with the
`agent.Agent` interface:

- `Send` / `SendNotice`
- `Read`
- approval listener callbacks

This is the correct boundary for high-level assertions because it observes the
adapter behavior that tests care about:

- emitted `agent.Message` values
- accumulated visible output / reasoning
- changed file paths
- executed commands
- normalized approval request / decision pairs

The fixture format is **not** the same as the current debug wire log. The
current `wirelog.Observer` is intended for humans. Transcript fixtures need a stable
machine-readable schema.

---

## One structured format

Transcript fixtures use one structured JSONL format.

We do **not** want two half-maintained formats for the same protocol.

That said, the human debug wire log remains useful and should stay as-is for
manual inspection. The structured transcript format is for recording and replay,
not for replacing debugging output.

### Why a structured format

Raw line capture alone is not enough for stable replay because:

- some fields are incidental
- literal string equality is too brittle
- replay needs to distinguish "expected inbound line" from "line to emit"

So each transcript step must capture:

- direction
- raw message payload
- optional semantic match constraints
- optional timing metadata

---

## Transcript schema

Transcript fixtures live under:

```text
internal/agent/codex/testdata/transcripts/<codex-version>/<test-case>/
```

Each case directory contains either:

- `prompt.md` — single-action scenario source plus recording configuration
  front matter

or:

- `conversation.md` — shared configuration for a multi-action scenario
- `conversation-01.md`, `conversation-02.md`, ... — ordered scenario actions

and always:

- `output.transcript` — recorded transcript fixture

The transcript fixture itself uses one file with:

- YAML front matter for scenario metadata and derived assertions captured at the
  `agent.Agent` boundary
- JSONL steps for the executable protocol script captured at the protocol
  boundary

Example:

```text
---
name: approvals_file_change
codex_version: 0.133.0
model: gpt-5.4
actions:
  - kind: prompt
    text: "Use the built-in file editing capability (not shell commands) to create codex_file_approval_test.txt with the contents: ok"

expect:
  output:
    num_messages: 0
    content: ""
  reasoning:
    num_messages: 0
    content: ""
  file_change:
    num_messages: 2
    files:
      - codex_file_approval_test.txt
  command:
    num_messages: 0
    executed: []
  approvals:
    - kind: fileChange
      decision: accept
---
{"kind":"recv","match":{"method":"initialize"}}
{"kind":"send","message":{"id":1,"result":{"capabilities":{"experimentalApi":true}}}}
{"kind":"recv","match":{"method":"thread/start"}}
{"kind":"send","message":{"id":2,"result":{"thread":{"id":"th1"}}}}
{"kind":"recv","match":{"method":"turn/start","params":{"input":[{"type":"text","text":"Use the built-in file editing capability (not shell commands) to create codex_file_approval_test.txt with the contents: ok"}]}}}
{"kind":"send","message":{"method":"item/fileChange/requestApproval","id":17,"params":{}}}
{"kind":"recv","match":{"id":17,"result":{"decision":"accept"}}}
{"kind":"send","message":{"method":"item/completed","params":{"turnId":"tu1","item":{"type":"fileChange","id":"fc1","status":"completed","changes":[{"path":"codex_file_approval_test.txt","diff":"@@ ...","kind":{"type":"insert"}}]}}}}
{"kind":"send","message":{"method":"turn/completed","params":{"threadId":"th1","turn":{"id":"tu1","status":"completed","items":[]}}}}
```

### Front matter

The YAML front matter is descriptive. It captures:

- scenario identity
- Codex version / model used to record it
- the normalized action list used to drive the scenario
- expected adapter-level behavior observed during the live recording run

The front matter is not a duplicate protocol trace. It exists so tests can
assert high-level outcomes without re-deriving every expectation from raw wire
steps.

### Authoring files

Scenario authoring files are the source-of-truth inputs for `codex-record`.

#### Single-action scenarios

Single-action scenarios use `prompt.md`.

Its front matter configures the live recording run, for example:

- `model`
- `developer_instructions`
- `ask_for_approval`
- `sandbox`
- `approval_strategy`
- `reasoning_effort`
- `reasoning_summary`

Its Markdown body is the text sent to Codex as one `prompt` action.

Example:

```md
---
model: gpt-5.4
---
Use the built-in file editing capability (not shell commands) to create
codex_file_approval_test.txt with the contents: ok
```

#### Multi-action scenarios

Multi-action scenarios use:

- `conversation.md` for shared run configuration
- `conversation-01.md`, `conversation-02.md`, ... for ordered actions

`conversation.md` contains only shared configuration front matter.

Each `conversation-NN.md` file contains:

- optional per-action front matter
- a Markdown body, which becomes the text sent to Codex for that action

Supported per-action fields:

- `kind`, default `prompt`

Unknown per-action front matter keys are invalid and should be rejected by the
implementation until they are explicitly added to the format.

Action kinds:

- `prompt` maps to `Send`
- `notice` maps to `SendNotice`

Example:

`conversation.md`

```md
---
model: gpt-5.4
---
```

`conversation-01.md`

```md
---
kind: notice
---
The magic word is PICASSO.
```

`conversation-02.md`

```md
---
kind: prompt
---
Reply with a single word: done
```

Rules:

- exactly one authoring style may be used in a case directory
- a case may define `prompt.md` or `conversation.md`, but not both
- `conversation-NN.md` files are loaded in lexical order
- `NN` is a zero-padded decimal sequence starting at `01`
- numbering must be contiguous

This keeps prompt bodies in Markdown without introducing an ambiguous inline
block grammar.

### One-pass recording workflow

The fixture format has two data sources captured concurrently during one live
run:

1. JSONL steps are recorded directly from `ProtocolObserver` and are the source
   of truth for wire replay.
2. YAML front matter is collected by a recording harness interacting with the
   `agent.Agent` interface and the approval listener boundary.
3. The normalized action list executed during recording is written into
   `output.transcript` front matter so replay remains self-contained.

This boundary is intentional:

- `ProtocolObserver` can see only raw JSON-RPC lines
- adapter-level assertions such as `file_change.files` and `command.executed`
  require observing translated `agent.Message` values
- approval summaries are best observed as normalized request / decision pairs
  at the listener boundary rather than inferred from raw wire traffic

So the protocol observer is not expected to populate the front matter by
itself.

### `expect` fields

The initial `expect` schema is:

- `output`
  - `num_messages`
  - `content`
- `reasoning`
  - `num_messages`
  - `content`
- `file_change`
  - `num_messages`
  - `files`
- `command`
  - `num_messages`
  - `executed`
- `approvals`
  - ordered list of approval kind / decision pairs

Rules:

- `num_messages` counts emitted `agent.Message` values in that category, not raw
  RPC envelopes
- lifecycle-only anchor flushes are excluded from these counts
- `content` is only used where accumulated text is meaningful to assert
- `file_change.files` is the distinct list of file paths observed in
  `agent.FileChangeSet`
- `command.executed` is the normalized list of command strings observed in
  `agent.Command`

Lifecycle note:

- `turn/completed` currently emits an `activeTurnStreamID` flush
  `agent.Output{}` message even when no visible output text was produced
- this message is part of the adapter contract and must still be validated by
  replay tests
- it is not counted in `expect.output.num_messages`, which tracks visible
  category messages rather than lifecycle anchors

### Step fields

Each JSONL step represents one protocol action.

- `kind`
  - `"recv"`: the replay peer expects to receive a line from the client
  - `"send"`: the replay peer emits a line to the client
- `message`
  - full JSON payload to emit for `send`
- `match`
  - semantic constraints used to validate `recv`
- `delay_ms`
  - optional delay before a `send`, used only when ordering/timing matters

### Matching rules (v1)

Replay matching should stay intentionally small in v1.

Supported checks:

- top-level `id`
- top-level `method`
- selected nested fields inside `params` or `result`

Matching is semantic, not byte-for-byte:

- extra fields in the actual line are tolerated unless the test explicitly
  constrains them
- object key order is irrelevant

This is enough because the client sends only a small set of request/response
shapes today:

- `initialize`
- `thread/start`
- `turn/start`
- approval responses
- `turn/interrupt`

If more precision is needed later, the matcher can grow carefully.

---

## Recording harness design

Add a dedicated recording CLI:

```text
cmd/codex-record
```

It runs one real scenario and captures both the JSONL step stream and the front
matter summary during that run.

The initial CLI shape is intentionally simple:

```text
codex-record [<codex-version> [<test-case>]]
```

Fixture layout:

```text
internal/agent/codex/testdata/transcripts/<codex-version>/<test-case>/
  prompt.md
  or:
  conversation.md
  conversation-01.md
  conversation-02.md
  output.transcript
```

The authoring files contain the live run configuration plus Markdown bodies for
one or more actions. `output.transcript` is the generated fixture.

Usage:

```text
codex-record [<codex-version> [<test-case>]]
```

Behavior:

- no args: record every case under the transcript root
- one arg: record every case for that Codex version
- two args: record one case

### Protocol observer

The transcript recorder should live in a dedicated `internal/transcript`
package and be exposed as a `ProtocolObserver` implementation so it can be
wired into the existing send/receive hooks with no new protocol tap points.

Example shape:

```go
type Observer struct { ... }
```

Responsibilities:

- receive raw JSON lines from `OnSend` / `OnReceive`
- parse them into JSON values
- write JSONL step entries in the canonical fixture format

The protocol observer does **not** derive:

- `agent.Message` category counts
- changed file lists
- executed command lists
- approval summaries

Those come from the recording harness at the `agent.Agent` boundary.

### Recording harness

The recording harness drives a real scenario through the `agent.Agent`
interface and collects the front matter assertions during that same run.

The harness is responsible for:

- parsing the action sequence from the authoring files
- executing each action in order through `Send` or `SendNotice`
- waiting for each action to reach its completion condition before starting the
  next one
- counting category-level `agent.Message` values
- accumulating visible output / reasoning text when asserted
- collecting distinct file paths from `agent.FileChangeSet`
- collecting executed commands from `agent.Command`
- recording approval summaries using the rules below

This keeps the protocol capture boundary clean while still allowing descriptive
scenario metadata in the final fixture.

The harness reads scenario configuration and actions from the authoring files,
launches a real Codex run, executes the scenario in order, and writes the final
combined fixture to `output.transcript` only after both the front matter
summary and JSONL step stream are complete.

### Action completion semantics

Action ordering must be explicit and deterministic.

The next action must not start until the previous action has reached its
completion condition.

Completion conditions:

- `prompt` completes when the action's turn-end flush is observed
- `notice` completes when the synthetic `codex:notice-turn` flush is observed

These completion rules apply equally during recording and replay-driven tests.

### Approval summary provenance

Approval summaries need one explicit provenance rule.

During the live recording run, the harness must observe:

- the normalized approval request kind passed to the listener
- the normalized decision returned by the listener

That listener-boundary observation is the source of truth for
`expect.approvals`.

Why this is necessary:

- command and file-change approvals encode the decision explicitly on the wire
- permissions approvals do not: any non-accept choice currently collapses to
  the same `{permissions:{}}` response

So approval summaries should not be reconstructed from replay-only inference.
Replay still validates approval behavior by checking the raw outbound response
against the transcript step stream.

### Important rule

The JSONL step stream is the source of truth for replay behavior. The recorded
`actions` front matter is the source of truth for replay-side scenario driving.
The remaining front matter is the source of truth for high-level adapter
expectations observed during recording.

This avoids drift between "record format" and "replay format".

---

## Replay peer design

Add a dedicated replay CLI:

```text
cmd/codex-replay
```

This is a separate subprocess, not an in-process goroutine fake.

The replay-side test harness must execute the normalized `actions` sequence
stored in `output.transcript`:

- `prompt` actions call `Send`
- `notice` actions call `SendNotice`

Replay does not depend on `prompt.md` or `conversation*.md` at runtime. Those
files are authoring inputs for recording only.

### Why a subprocess

The current Codex adapter is built around a managed child process with
stdin/stdout/stderr pipes. A replay subprocess preserves that contract and lets
tests exercise:

- process startup
- stdio framing
- handshake logic
- worker goroutines
- approval response writes

without launching real Codex.

### Replay behavior

The replay peer reads the transcript sequentially and acts as a strict protocol
peer:

1. for a `recv` step, read one line from stdin and validate it against `match`
2. for a `send` step, optionally wait `delay_ms`, then emit the JSON line on
   stdout
3. on mismatch, exit with an error and a clear explanation
4. on EOF before transcript completion, fail
5. on extra unexpected client input after transcript completion, fail

This makes the transcript an executable protocol script, not just a passive log.

### stderr

In v1, stderr support is optional. The seed scenario does not require it.
Structured stderr scripting can be added later if replay tests need to cover
log-path behavior.

---

## Codex client process override

Add a new client option:

```go
func WithAppServerCommand(cmd []string) Option
```

Exact signature may vary, but the meaning is:

- replace the default `npx @openai/codex ... app-server` launch command
- preserve the rest of the adapter lifecycle unchanged

### Why this option

Tests need to replace the launched app-server with `codex-replay` without
changing the client’s read/write logic.

This is preferable to environment-variable-only configuration because it keeps
the override explicit at the call site and available to both tests and tooling.

### Scope

`WithAppServerCommand` affects only process launch. It does not alter:

- protocol parsing
- worker behavior
- approval handling
- message translation

---

## Seed scenario

The first recorded scenario is derived from:

- `internal/agent/codex/integ_approvals_test.go`
- `TestApprovals_fileChange`

Prompt:

```text
Use the built-in file editing capability (not shell commands) to create codex_file_approval_test.txt with the contents: ok
```

What this scenario must cover:

- normal startup handshake
- `turn/start`
- file-change approval request from Codex
- approval response from the client
- turn completion

The initial replay-based test should assert at least:

- the client surfaces `agent.ApprovalFileChange`
- the client accepts the listener decision and writes the expected approval
  response
- the turn completes cleanly

This first slice is enough to validate the end-to-end transcript approach before
adding more scenarios.

---

## Initial implementation plan

1. Add this design doc.
2. Add `WithAppServerCommand` to the Codex client/process layer.
3. Implement a structured `TranscriptObserver`.
4. Implement `cmd/codex-replay`.
5. Record one transcript for the file-change approval scenario.
6. Add one replay-based test in the normal test suite using that transcript.
7. Do not migrate existing live integration tests yet.

---

## Follow-up scenarios

After the seed scenario works, good next candidates are:

- command execution approval
- file-change stream + flush behavior
- reasoning delta handling
- notice buffering / synthetic notice flush
- a broader live-Codex smoke transcript covering context, reasoning, text
  generation, and file editing

That broader live scenario should remain a real integration test and can serve
as a recording source for future replay fixtures.

---

## Open questions

- Exact `WithAppServerCommand` API:
  - raw argv slice
  - command string plus args
  - launcher interface
- Transcript storage layout:
  - by Codex version
  - by model
  - by scenario name
- Whether `delay_ms` is needed in the first transcript or can wait until a
  timing-sensitive scenario appears
- Whether approval-response assertions belong in transcript matching only, or
  also in the replay test body
