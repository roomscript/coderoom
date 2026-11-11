# Design: transcript recording and replay for all backends

Status: steps 1 and 2 implemented, 26 October 2026. Step 3 designed,
6 November 2026; steps 3.1 to 3.5 implemented, including ten live Claude
2.1.304 fixtures. Step 3.6, a live scenario sweep, designed on 11 November
2026; implemented, live run pending. Agreed so far: a one-time cleanup to the Codex baseline,
a standard transcript schema, a backend-neutral
`internal/transcript`, and separate record and replay commands per backend.
Step 2 includes the review correction that protocol rules belong to the
backends. Step 3 decides the Claude scenario list, replay-time binding of
Claude's prompt IDs, and that `model-interrupt` does not apply to Claude
(see [Claude recording and replay](#claude-recording-and-replay)).

This document is the reference for the shared transcript format, recording
harness, replay peer and backend boundaries. The standard schema and Codex
migration in step 2 and Claude recording and replay in step 3 are implemented.

---

## Problem

Transcript replay covers only Codex. The Claude adapter has scripted-peer
unit tests and live integration tests, but nothing checks it against
recorded protocol traffic, so drift in Claude Code's stream-json protocol is
found only by live runs, which cost quota.

Before step 2, the infrastructure could not be reused as is:

- `internal/transcript` imported `internal/agent/codex`. `Config` used Codex
  types (`AskForApprovalPolicy`, `SandboxMode`, `ReasoningEffort`,
  `ReasoningSummary`), and `input_read.go` decodes into them. Claude tests
  that import it would build and rerun whenever Codex changes.
- Fixture headers carried Codex names: `codex_version` and
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
options: `approval`, the decision the recorder gives every approval request
(`accept`, the default, or `decline`). Further options are added with the
scenarios that need them.

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

The boundary is about protocol knowledge, not only imports. Before step 2, two shared
components knew Codex's wire protocol without importing it:

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
  backend: `func(Message) (ReplayMatch, error)`, using JSON objects with
  `json.RawMessage` field values;
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

`ModelActions.checkSelection` previously required the requested model and
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

## Recording and replay format

Fixtures live under
`internal/agent/<backend>/testdata/transcripts/<version>/<case>/`.
Each case contains `output.transcript` and one authoring style:

- `prompt.md`: configuration front matter followed by one prompt body.
- `conversation.md`: configuration front matter with an empty body, plus
  `conversation-01.md`, `conversation-02.md`, etc. for ordered actions.

Conversation numbering starts at 01, is zero-padded and contiguous. Each action
file has an optional `kind` front matter field (default `prompt`), followed by
its text body. Unknown configuration and action fields are rejected. The
recorder validates backend options before starting the client.

`output.transcript` contains YAML front matter delimited by `---`, followed by
one JSON object per line. The header records `name`, `backend`,
`backend_version`, optional recording settings (`model`, `effort`,
`system_prompt`, `options`), normalized `actions`, and `expect`. The recorder
writes the combined file only after the scenario completes. Authoring files
are recording inputs; replay drives the recorded `actions` directly.

The recording harness observes two boundaries during the same run:

- Raw protocol lines supply the executable steps, independently of adapter
  parsing. `Observer` captures sends and receives and uses the backend's
  supplied match rule for outbound messages.
- Agent messages and approval listener callbacks supply high-level assertions.
  Approval kind and decision come from the listener boundary; some backend
  wire responses do not distinguish every normalized decision.

The `expect` fields describe visible adapter behavior:

| Field | Contents |
|---|---|
| `output`, `log` | `num_messages`, accumulated `content` |
| `reasoning` | `num_messages`, `content`, `num_streams`, `all_flushed` |
| `file_change` | `num_messages`, distinct `files` |
| `command` | `num_messages`, normalized `executed` commands |
| `notice` | optional `num_turn_flushes` |
| `approvals` | ordered `kind` / `decision` pairs |

Counts refer to agent messages, not protocol envelopes. Lifecycle-only output
flushes do not count as visible output. Replay still checks lifecycle behavior,
including the final turn flush and notice anchors. For legacy files without
`actions`, `input` supplies one prompt action; absent notice expectations default
to the number of recorded notice actions. These defaults are independent of
the standard header migration; old Codex header names are rejected.

Each JSONL step has `kind` and a payload:

- `recv`: read a client line and match it against `match`.
- `send`: emit `message`, optionally after `delay_ms`.

The shared matcher compares JSON values semantically. Expected object keys
must exist with matching values; additional actual keys are tolerated. Arrays
match in order and length; scalar values and types must match. Object key order
does not matter. The peer fails on mismatches, premature EOF, unknown step
kinds, malformed JSON or unexpected extra client input after the script.

The replay peer runs in a separate subprocess, preserving the real client's
stdio framing, process lifetime, handshake, approval writes and worker behavior.
Codex launches it through `WithAppServerCommand`; no real Codex installation
is needed for replay. Recording uses the real CLI with stdio pipes and no PTY.

Codex normalization keeps request IDs and methods, plus these stable fields:

| Method | Matched parameters |
|---|---|
| `initialize` | client name and capabilities |
| `thread/start` | model and developer instructions |
| `turn/start` | thread ID, input, output schema, model and effort |
| `turn/interrupt` | thread ID and turn ID |
| other messages | complete payload |

Codex action completion waits for the turn-end flush for `prompt` and the
synthetic `codex:notice-turn` flush for `notice`. Model operations use the shared
`ModelActions` runner and the contract above. Interruption waits on backend
activity detection without consuming agent messages.

The Codex tooling remains backend-specific:

```text
codex-record [<codex-version> [<test-case>]]
codex-replay --transcript <path>
```

Codex recording with no arguments selects every case under the fixture root; one
argument selects a version; two select one case. The version pin identifies
the supported live CLI. Replay discovers every retained version. Upgrade
comparison reads high-level expectations; it does not compare incidental raw
protocol traffic. Previous recordings remain available after upgrades.

Live integration tests use the `integration` build tag and real CLIs. They
answer whether the current CLI works with the adapter; replay is deterministic
regression coverage in the normal test suite. Thread startup settings, such as
system prompts, need fresh-client live tests because a shared live transcript
sweep cannot validate every scenario's startup configuration.

---

## Claude recording and replay

Designed on 6 November 2026 from the adapter on main and Claude Code 2.1.304
(installed). Facts marked "observed" come from the code or earlier live
checks. The first recording confirmed both conditional scenarios, as documented
in Scenarios below.

### Capability requirements

The `agent.Agent` and `agent.ModelSelector` contracts promise the same
behaviour for both backends; how each backend delivers it differs. The table
assigns each behaviour to the coverage that can show it. Replay shows the
adapter against real protocol traffic; scripted-peer tests show cases that
are hard to trigger live or depend on timing; live tests show what replay
cannot see, such as launch arguments.

| Behaviour | Scope | Claude coverage |
|---|---|---|
| Start: handshake, catalog, startup settings | both | replay (every case), scripted `TestStart_*`, `TestModels_startup*` |
| Prompt turn: anchor, streamed output, one anchor flush | both | replay |
| Failed turn: provider explanation logged before the flush, no `Read` error | both | replay if it can be triggered (`turn-failed`), scripted `TestRead_failedTurnExplanation` |
| Notice: reply kept out of `Read`, notice anchor flush | both | replay |
| Reasoning streams, flushed | both, when the model reasons | replay if haiku reasons (`reasoning-streaming`), scripted `TestDecode_reasoning*` |
| Commands with exit code | both | replay |
| File changes | both | replay |
| Approvals: kind, options, accept and decline | both; option sets differ | replay |
| Approvals: accept for session, cancel | both; Claude maps them to `updatedPermissions` and deny with interrupt | scripted `TestPermission_choices`, live `TestClientLiveAcceptForSession` |
| Approval withdrawal (turn end, `control_cancel_request`) | Claude mechanism | scripted `TestPermission_withdrawn*`, live `TestClientLiveInterruptPendingApproval` |
| Interrupt of a prompt turn | both | scripted `TestInterrupt*`, live `TestClientLiveInterrupt*` (see [Interruption](#interruption)) |
| Model list, select, failed select, settings | both, through `ModelSelector` | replay |
| Model selection interrupted | Codex only | not applicable to Claude |
| Self-started turns, unmatched and late replays, process exit, parent cancellation | Claude | scripted |
| Leading `/` escaped | Claude | scripted `TestSend_escapesLeadingSlash`, live `TestClientLiveSlashPrompt` |
| Model, system prompt and session ID at launch | Claude launch arguments | unit `TestClaudeArgs`, `TestClientLaunchesWithResumeID`; live `TestClientLiveSessionID` |

Replay cannot see launch arguments: `WithCommand` replaces the whole
invocation. So Claude gets no `system-prompt` case, unlike Codex, where the
system prompt is on the wire.

### Scenarios

Fixtures live under
`internal/agent/claude/testdata/transcripts/<claude-version>/<case>/`. The
first baseline was recorded on 2.1.304. Every case except `turn-failed` sets
`model: haiku` in its header; `turn-failed` uses an unknown model to exercise
the failure path. Prompts stay small.

| Case | Authoring | Shows |
|---|---|---|
| `turn-text` | prompt: no tools, reply only OK | handshake, prompt ID binding, streamed output, result, anchor flush |
| `command-approved` | prompt: run `echo hello > command-output.txt` with Bash | `can_use_tool`, allow, `Command` with exit code |
| `file-change-approved` | prompt: create a file with Write | `FileChangeSet`, allow |
| `file-change-declined` | as above, `options: {approval: decline}` | deny response, failed file change, turn still ends with the flush |
| `notice-turn-flush` | one notice | notice anchor flush, no visible output |
| `notice-silent-ack` | notice, then prompt | notice filtered, next prompt admitted after the notice flush |
| `model-success` | list, prompt, select a second model with an effort, select haiku without effort, settings, prompt | `set_model`, `apply_flag_settings`, `get_settings` readback, the `set_model` local-command replay ignored, effort carried over |
| `model-failure` | list, `model-select-failed` with an unknown model, settings unchanged, select a listed model | `set_model` error, readback after failure |
| `turn-failed` | header `model` set to an unknown model, one prompt | provider explanation logged before the flush |
| `reasoning-streaming` | select an effort that makes haiku reason, then a prompt that needs a short reasoning step | reasoning stream and flush |

Both initially conditional cases are retained. Claude Code accepted an unknown
`--model` at startup, then failed the first turn with a provider explanation
and the anchor flush, without a `Read` error. Haiku at high effort emitted
summarized reasoning and a flush for the short arithmetic question.

The model names in `model-success`, and the effort, come from the catalog at
recording time. `model-settings` compares labels, so the authoring files use
the full IDs that `get_settings` reports. The final prompt in `model-success`
runs on haiku.

Steps 3.4 and 3.5 are implemented with ten authoring scenarios and live
recordings under `2.1.304`. The installed catalog confirmed haiku and sonnet,
their full model IDs and high effort support. `model-failure` first selects
haiku with high effort so the failed selection has a known settings baseline.
All model turns run on haiku; the sonnet selection is control traffic only.

Recording observations:

- The initial prompt hit the account session limit. That response was discarded
  rather than retained as a successful `turn-text` baseline. Recording resumed
  after the limit reset and produced the expected `OK` output.
- Plain `echo hello` was auto-approved. The command case now redirects its
  output into `command-output.txt` in the temporary recording directory, which
  triggered a command approval. All three approval cases have recorded
  decisions, including `decline` for `file-change-declined`.
- Every replay of our prompt retains its recorded outbound UUID; the fresh
  replay client's UUID is bound at runtime. Account email and organization
  fields are redacted, and the home directory is absent from every fixture.

### Generated IDs

Observed in `internal/agent/claude`: the only value the client generates per
run and sends on the wire is the prompt `uuid` (`newPromptID`, one per `Send`
or `SendNotice`). Claude echoes it in the prompt's replay, and the client
claims the turn only on a match.

- The session ID is generated in `New` but sent only as `--session-id`, a
  launch argument. User lines carry `session_id: "default"`. The client does
  not read `session_id` from Claude's lines. Replay sends the recorded
  session ID and the client ignores it, so it needs no handling.
- Control request IDs the client sends are fixed (`coderoom-init`,
  `coderoom-settings`) or numbered in send order (`coderoom-set-model-1`), so
  the same actions produce the same IDs.
- Claude's own request IDs (`can_use_tool`) come from Claude's lines; the
  client echoes them, so the recorded values match.

Three ways to handle the prompt uuid:

| Option | Cost |
|---|---|
| Inject deterministic IDs in recording and replay | Needs a production option that the recorder uses against the live CLI. Every recording sends the same uuids into the user's Claude session history; whether Claude accepts reused uuids across sessions is unknown. Fixtures stop showing what production sends |
| Capture and substitute with placeholders in the shared format | Adds a placeholder syntax to every fixture and a second meaning to `match`, for one backend's need |
| Bind at replay time (**chosen**) | Fixtures keep the recorded values. A backend rule names the generated values in each outbound line; the shared peer substitutes them in later lines |

Contract, in `internal/transcript`:

```go
// BindReplayValues returns the values in one outbound line that the client
// generates per run, as pairs of recorded value and the value the replayed
// client sent. expected is the recorded match, actual the line received.
// It must not mutate its inputs or perform I/O. Values it does not return
// are matched as recorded.
type BindReplayValues func(expected, actual map[string]any) (map[string]string, error)

// ServeReplay plays steps against a client on stdin and stdout. bind may be
// nil.
func ServeReplay(stdin io.Reader, stdout io.Writer, steps []Step, bind BindReplayValues) error
```

`ServeReplay` keeps one binding table per run:

1. For a `recv` step, it decodes the client line, calls `bind`, and adds the
   pairs. The table is one to one: a recorded value already bound to a
   different actual value, or an actual value already bound to a different
   recorded value, is an error, and so is an empty recorded or actual value.
   Values generated per run are unique by nature, so a collision means the
   client reused one. Without this check, a client that reused a prompt uuid
   would have a late replay of an earlier prompt rewritten to the pending
   prompt's uuid, and the replay could still pass.
2. It substitutes the bindings into the expected match, then matches as
   today. Substitution replaces JSON string values equal to a recorded value,
   at any depth. Object keys and substrings are not touched.
3. For a `send` step, it substitutes the bindings into the message before
   writing it.

`ServeReplay` reads client lines with no length limit, as the Claude
adapter's `readLines` does. The current loop's `bufio.Scanner` stops at
64 KiB, and an approval answer echoes the tool input (`updatedInput`), which
can hold a whole file. Removing the limit only relaxes Codex replay. Lines
the peer writes need no change: the JSON encoder has no limit, and both
clients read without one.

Claude's rule, `claude.BindReplayIDs`: when the expected line has
`type: user` and a string `uuid`, and the actual line has a string `uuid`,
bind the first to the second. Nothing else is bound. A late replay of a
released prompt maps to that prompt's own uuid, and the `set_model`
local-command replay, which carries Claude's fresh uuid, passes through
unchanged. Codex passes `nil`.

Claude needs no match rule: `transcript.NewObserver(nil)` matches outbound
lines in full. The proof is the replay itself: it runs a fresh client with
new uuids, so it passes only if every other outbound field reproduces.

If the adapter later reads the session ID from Claude's lines (for example
to check the `system/init` echo from #25), binding cannot cover it, because
the session ID never appears in an outbound line. That change would revisit
this decision.

### Interruption

**`model-interrupt` does not apply to Claude.** A Codex selection runs an
acknowledgement turn, which `turn/interrupt` can stop, so `ModelActions`
waits for the turn to start before interrupting. A Claude selection is only
control requests (`set_model`, `apply_flag_settings`, `get_settings`): no
turn runs, `Interrupt` is a no-op while the exchange holds the gate
(`isIdle` sees no turn and no pending prompt), and the exchange has no
cancellation by design (`pkg-agent-claude-models.md`). There is no event that
makes interruption safe, because there is nothing to interrupt.

So the Claude recorder and the Claude replay test reject `model-interrupt`,
as the recorder rejects a header `effort`. Claude needs no activity detector:
both pass `transcript.NewTurnActivityObserver(nil, nil)` to `ModelActions`,
which never signals and is never waited on.

Interrupting a prompt turn stays with scripted and live tests. Neither
backend has a transcript action for it, and adding one is a shared feature
outside this step. If one is added later, the safe point for Claude is the
replay of our prompt (`type: user`, `isReplay: true`, not local-command
output). Before that, the interrupt may reach Claude before the turn starts,
and the prompt stays pending until `Interrupt` releases it after a turn end.

### Recording environment

- The recorder runs the real `claude` from a temporary directory
  (`claude-record-*`) with the adapter's normal launch arguments and
  environment. It reads `claude --version` and refuses to record into a
  version directory with a different name: Claude Code is not pinned.
- Personal configuration loads, as agreed for the adapter. User allow rules
  can remove an approval a scenario expects. The approval cases are checked
  by the human when reviewing the fixture (`expect.approvals` not empty).
- **Personal data.** Observed in the spike recordings: the `initialize`
  answer carries the account `email` and `organization`, and `system/init`
  carries home directory paths and the user's plugins and skills. The
  recorder redacts before writing: string values of the keys `email` and
  `organization` become `<redacted>`, and the home directory in any
  string becomes `~` only when it is the whole string or followed by `/`.
  The same function applies to every step in both directions and to every
  string in `expect`, so echoed values still match
  and replay output equals the expectations. The adapter reads none of these
  fields. Plugin and skill names are not redacted; the human reviews
  fixtures before committing them.
- **Stderr.** Observed: the adapter logs stderr lines on `claude:log`, the
  stream of its own logs. Replay has no Claude stderr, so a recording that
  counted them would not replay. The adapter moves stderr lines to
  `claude:stderr` and exports `claude.IsStderrLogStream`, like Codex; the
  recorder leaves them out of `expect.log`. Nothing outside the package keys
  on either stream ID.

### Shared recording and replay code

Two pieces of Codex code are backend-neutral but private to Codex:

- the replay loop in `cmd/codex-replay/serve.go`;
- the action runner and the collector that turns agent messages into
  `Expect`: in `cmd/codex-record` for recording, and again in
  `internal/agent/codex/replay_test.go` for replay, with stream lifecycle
  checks. They use only `agent.Agent`, `agent.Message` and approvals.

The replay loop moves into `internal/transcript` as `ServeReplay` (above);
`cmd/codex-replay` calls it with `nil`.

**Confirmed, 6 November 2026; implemented in step 3.2:** move the runner and the collector into
`internal/transcript`, and switch Codex recording and replay to them in the
same change, before any Claude code. The caller supplies the notice anchor,
a log filter (`IsStderrLogStream`), and a path normalizer (the recording
directory). The Codex replay suite, unchanged, proves the move. The
alternative is to copy about 500 lines into the Claude recorder and replay
test, which then need updating with every `Expect` change. The move touches
Codex test code beyond the schema change, which the non-goals above excluded
for step 2; it is a move with no behaviour change. `RunActions` drives prompts, notices and model
actions; `Collector` builds recording expectations and `CheckExpectations`
checks replay messages and stream lifecycles. `CollectorOptions` supplies the
notice anchor, ignored-log predicate and path normalizer.

### Live scenario sweep

Designed on 11 November 2026. Replay tests the adapter against old
recordings, so it cannot notice that the Claude CLI changed. Claude Code is
not pinned and updates itself, so the CLI can change without an upgrade
decision. The sweep runs the recorded scenarios against the installed CLI and
answers two questions:

- **Does a long session hold up?** Recordings start a fresh client per case,
  so replay never shows state carried across many turns: notices followed by
  approvals, prompts admitted right after a flush.
- **Has Claude's behaviour moved from the fixtures?** Each case is compared
  with its recording, so a failure says the fixtures need re-recording or the
  adapter needs a fix.

It is `TestClientLiveTranscriptScenarios` in
`internal/agent/claude/integ_transcripts_test.go`, behind the `integration`
build tag, so it runs with `make test-integration-claude`. It writes no
fixtures.

**Baseline.** The newest retained version directory, compared numerically.
The test logs the installed CLI version and the baseline version; a mismatch
alone is not a failure, since detecting its effects is the point.

**Sessions.** A case joins one shared session when it has no model actions
and its header matches the shared session's: `model: haiku`, no system
prompt. Every other case runs in a fresh session started with its own header.
On the current baseline:

| Session | Cases |
|---|---|
| shared | `turn-text`, `command-approved`, `file-change-approved`, `file-change-declined`, `notice-turn-flush`, `notice-silent-ack` |
| one each | `model-success`, `model-failure`, `reasoning-streaming` (model actions), `turn-failed` (unknown model) |

The rule uses the case contents, not names, so new cases are placed without
changing the test. Cases run in fixture order. Each session has its own
temporary working directory; the shared-session cases write different files.

**Approvals.** One listener per session gives each request the decision of
the running case's `approval` option (default `accept`), and records the
requests. The decision changes only between cases, when no turn runs.

**Checks.** Each case runs its actions through `transcript.RunActions` with a
collector, as recording does. Strict, because they do not depend on the
model:

- no `Read` error, and every action reaches its anchor flush;
- model actions meet their contract (`ModelActions`), including
  `model-settings` labels. A changed model ID fails here and means the
  fixture needs re-recording;
- the test rejects `model-interrupt` and a header `effort`, as the recorder
  does.

Loose, because they depend on the model: the collected expectations are
compared with the fixture's using `transcript.CompareUpgradeOutputs`, which
checks only that each kind of behaviour the recording showed is still
present (output, logs, commands, file changes, notice flushes, approvals),
not text or counts. In the shared session, reasoning is left out of the
comparison: whether Claude reasons depends on session state, which differs
from the recording's fresh session. Cases in their own session compare
reasoning too.

**Ownership against the hand-written live tests.** The hand-written tests in
`integ_live_test.go` each check one contract strictly, such as exit codes,
files on disk, recall of a notice, cancel and interrupts. The sweep checks
session health and drift, loosely. Neither replaces the other. The sweep gets
no assertion that a hand-written test already makes; a new contract that
needs live coverage gets a hand-written test, not a stricter sweep.

**Cost.** About ten small haiku turns per run, plus control traffic for the
model cases.

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
- Update the documented fixture layout and example version.

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
- Fold the prior Codex transcript design into this document and remove the
  superseded document.

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

Design: [Claude recording and replay](#claude-recording-and-replay). Each
sub-step is a separate change. Sub-steps 3.1 to 3.4 need no live run; 3.5
records live and runs only when the human asks.

**3.1 Shared replay loop and binding.**

- Move `serveReplay` into `internal/transcript` as `ServeReplay` with the
  `BindReplayValues` parameter, and move its tests. `cmd/codex-replay` calls
  it with `nil`.
- Table tests with synthetic lines: no binding; a bound value substituted into
  a later match and a later send; nested values; a value that only contains a
  bound string as a substring is left alone; a recorded value rebound to a
  different actual value fails; two recorded values bound to one actual
  value fail; an empty actual value fails.
- A replay test with a client line longer than 64 KiB.

Verify: `make test`; the Codex replay suite passes unchanged.

**3.2 Shared runner and collector** (if confirmed).

- Move the prompt and notice runner, the collector and the expectation
  assertions into `internal/transcript`, with the caller-supplied notice
  anchor, log filter and path normalizer.
- Switch `cmd/codex-record` and `internal/agent/codex/replay_test.go` to them
  and delete their copies.

Verify: Codex fixtures are unchanged, the Codex replay and model transcript
suites pass, and `internal/transcript` still names no protocol method or
message type.

**3.3 Claude adapter seams.**

- Stderr lines go to `claude:stderr`; export `claude.IsStderrLogStream`.
- Export `claude.BindReplayIDs` with a table test: a user line binds its
  uuid; a control response, a user line without `uuid`, and an actual line
  without `uuid` bind nothing; inputs are not mutated.
- Update `docs/design/pkg-agent-claude.md` for the stderr stream.

Verify: `make test`; existing Claude tests pass with the stream change.

**3.4 `cmd/claude-record`, `cmd/claude-replay` and the replay test.**

- `claude-record [<claude-version> [<test-case>]]`: no arguments select the
  installed version's directory only; one argument selects that version's
  cases; two select one case. Older directories remain replay inputs and
  cannot be recorded with a newer CLI. It checks the installed version against
  the selected directory, rejects a header `effort`, `model-interrupt` actions
  and unknown options, converts `approval` into the listener's decision,
  records with
  `transcript.NewObserver(nil)`, redacts, and writes `backend: claude`.
- `claude-replay --transcript <path>` calls `ServeReplay` with
  `claude.BindReplayIDs`.
- `internal/agent/claude/replay_test.go` (`package claude_test`) discovers
  every retained version, runs `claude-replay` through `WithCommand`, replays
  approval decisions from `expect.approvals`, uses the notice anchor
  `claude:notice-turn`, and rejects `model-interrupt`.
- Unit tests for the recorder's option and header validation and for
  redaction, both directions, including an echoed value.
- Authoring files for the scenarios in the version directory.

Like the Codex suite, the replay test fails when it finds no fixture, so it
does not pass vacuously. 3.4 and 3.5 therefore reach main together; the code
can be reviewed before the recording, but `make test` passes only once the
fixtures exist.

Verify: `make lint`; the recorder and replay command unit tests pass.

**3.5 Recording (on request).** Record every case on haiku with the
installed version, drop the conditional cases that do not show their
behaviour, and update this document with what the recording showed.

Verify:

- The Claude replay suite runs in `make test` without Claude Code installed
  (for example with `PATH` stripped of `claude`).
- `go list -deps -test ./internal/agent/claude` shows no
  `internal/agent/codex`.
- No fixture contains the account email, organization or home directory.
- Approval cases have nonempty `expect.approvals`, with `decline` in
  `file-change-declined`.
- Every recorded replay of our prompt carries the uuid the client sent in the
  preceding user line (the binding is exercised, not bypassed).

**3.6 Live scenario sweep.** Design:
[Live scenario sweep](#live-scenario-sweep).

- `internal/agent/claude/integ_transcripts_test.go` with
  `TestClientLiveTranscriptScenarios`, using the shared runner, collector
  and `CompareUpgradeOutputs`. Reuse the existing live test helpers in
  `integ_shared_test.go` where they fit.
- Session grouping, the per-case approval decision and the shared-session
  reasoning exclusion as designed.
- Failure messages name the case, the baseline version and the installed
  version.

Verify:

- `make test-integration-lint`, and `make test` is unaffected.
- One live run, only when the human asks, passes on the installed version.
  The test log shows each case's session and the two versions.
- The hand-written live tests are unchanged.

---

## Open questions

None for step 3. Both conditional cases were recorded and retained.

Resolved on 6 November 2026: the capability requirements and scenarios, the
generated IDs (replay-time binding; the session ID needs nothing), the
version directory (the installed version that recorded the baseline; newer
versions get a new directory and the previous one is retained), and
interruption (`model-interrupt` does not apply to Claude; no activity
detector).
