# Package design: internal/agent/claude

Status: **draft (phase 2 gap analysis)**. Decisions agreed on 16 October 2026
are marked **Decided**. Evidence: `docs/playground/poc-claude-stream-json.md` (E1 to E10).

Scope of the current step: a new `agent.Agent` implementation in
`internal/agent/claude`, with **no changes to other packages**. Where the
analysis shows that another package should change, it is listed under
[Changes outside the package](#changes-outside-the-package) for discussion, and
the adapter-only fallback is described.

---

## What this package is

A thin process wrapper around one long-running `claude` process speaking the
bidirectional stream-json protocol (the one the Agent SDK uses). Same shape as
`internal/agent/codex` (see `pkg-agent-codex.md`): blocking calls, `Read()` as
the single point of consumption, a `ProtocolObserver` on raw lines, an optional
`ApprovalListener`.

It is not responsible for session logic, routing, or restarts.

### Why a direct integration

Three options were considered:

| Option | Outcome |
|---|---|
| Speak stream-json directly from Go | **Chosen.** One runtime, same model as the Codex app-server integration, full fidelity for approvals, interrupts and turn boundaries |
| Sidecar using the official Agent SDK (Node or Python) | Rejected: a second runtime to install and ship, for the same protocol |
| ACP through the Claude Code adapter | Rejected for now: Claude Code does not speak ACP natively; the adapter wraps the Agent SDK, adding a sidecar and a lossy common layer on top of `agent.Agent`. ACP may come later as a generic backend for tools that speak it natively (e.g. Gemini CLI) |

The cost of the chosen option is that stream-json is an SDK-level protocol, not
a documented standalone interface. Only the installed Claude Code version is
targeted; version pinning comes later, and replay transcripts record the
version so shape changes are caught.

### Invocation

```
claude --print --verbose \
  --input-format stream-json --output-format stream-json \
  --replay-user-messages --include-partial-messages \
  --permission-prompts host --permission-prompt-tool stdio \
  --thinking-display summarized \
  --disallowedTools <list> \
  [--model <model>] [--append-system-prompt <participant prompt>]
```

`--disallowedTools` always includes `AskUserQuestion` (see
[AskUserQuestion](#askuserquestion)), the scheduling tools (see
[Self-started turns](#self-started-turns)) and the tools that reach outside the
room (see [Reaching outside the room](#reaching-outside-the-room)):

```
AskUserQuestion,CronCreate,CronDelete,CronList,ScheduleWakeup,Monitor,
ListAgents,SendMessage,PushNotification,RemoteTrigger
```

- Working directory: the room cwd (`cmd.Dir`). E6 showed `PWD` does not matter.
- Environment: inherited minus `CLAUDECODE`, `CLAUDE_CODE_*`, `CLAUDE_PID`,
  `CLAUDE_EFFORT`, so a coderoom started from inside Claude Code does not spawn
  a nested session. Plus the switches in [Self-started turns](#self-started-turns).
- Personal config loads, as with Codex.

### Options (mirroring codex)

| Option | Claude mapping |
|---|---|
| `WithContext` | worker lifecycle, as codex |
| `WithObserver(ProtocolObserver)` | every raw stdin/stdout line, as codex |
| `WithApprovalListener` | `can_use_tool` requests, see [Approvals](#approvals) |
| `WithSystemPrompt` | `--append-system-prompt`. Appends to Claude Code's own system prompt instead of replacing it, so tools and conventions keep working. Same role as Codex `developerInstructions`: not visible in the conversation |
| `WithModel` | `--model` |
| `WithCommand(name, args...)` | override the executable, for replay peers in tests (as `WithAppServerCommand`) |

---

## agent.Agent: method by method

| Method | Fit | Claude mechanism |
|---|---|---|
| `Start()` | fits | spawn; send `control_request{subtype: initialize}`; block until the matching `control_response` (under 3 s in E1). Unlike Codex, there is no thread to create: the session id appears only with the first turn's `system/init` |
| `Send(prompt)` | fits | write `{"type":"user","message":{"role":"user","content":prompt},…}`; return the turn anchor (below). `ErrTurnInProgress` while a turn is in flight, as codex |
| `SendNotice(prompt)` | fits with a weaker guarantee | same prompt wrapper and `{` heuristic as codex. **Gap:** Codex also sends a per-turn output schema; Claude's `--json-schema` is per process and not enforced (E7), so the instruction is prompt-only. Acknowledgement filtering logic can be the same |
| `Read()` | fits | translate stdout lines (mapping below); stderr lines as `Log`; error only when the process exits |
| `Interrupt()` | fits | `control_request{subtype: interrupt}`. The turn then ends with `result{subtype: error_during_execution, terminal_reason: aborted_streaming}`; the adapter treats it as a normal turn end (Log + anchor flush), not a `Read()` error. No-op when idle |
| `Stop()` | fits | close stdin (exits in about 1 s, E8); after a grace period SIGTERM, then kill; always reap |
| `Keepaliver` | **postponed** | `Keepaliver` exists to keep a backend warm (Codex takes about 5 min from cold to warm). No equivalent cold state has been seen for Claude: the session lives in the process and the local transcript. The only idle effect observed is prompt-cache expiry (1 h cache in the E1 to E10 runs): the first turn after a long idle re-reads the context uncached, costing seconds and money, not minutes. Not measured on a large context. If Claude turns out to have a pause or cold mode, or cache misses hurt, implement it (for the cache, a minimal turn every ~50 min). The session treats `Keepaliver` as optional, so nothing breaks meanwhile |

### Turn anchor

The adapter owns the turn boundary: every turn ends with exactly one `result`.

- `Send` returns a constant anchor `claude:turn`; on `result` the adapter emits
  `{StreamID: "claude:turn", Mode: ModeFlush, Content: Output{}}` after
  flushing any open item streams.
- `SendNotice` returns `claude:notice-turn`, flushed the same way, as codex.
- The replayed user message (`isReplay`, about 1.5 s after the write) is used
  only to confirm the turn started; it is not needed for the anchor.

This answers open question 8: the anchor is adapter-local and needs no protocol
id.

---

## Read(): event mapping

Stream IDs are built from Claude ids and are opaque to consumers.

| Claude stdout | agent.Message |
|---|---|
| `stream_event` `content_block_start{type: text}` + `text_delta` | `Output` `ModeStream` on `claude:output:<message_id>:<index>` |
| `content_block_stop` of a text block | `Output` `ModeFlush`, same ID |
| `thinking_delta` (needs `--thinking-display summarized`) | `Reasoning` `ModeStream` on `claude:reasoning:<message_id>:<index>`, flushed on block stop. Empty deltas (no summary) are dropped |
| `assistant` `tool_use{name: Bash}` | `Command{Command: input.command}` `ModeStream` on `claude:command:<tool_use_id>` |
| `user` `tool_result` for that Bash call | `CommandWithOverrideOutput(output)` with `ExitCode` `ModeStream`, then `Command` `ModeFlush`. `Cwd` is not reported. Exit code: see below |
| `assistant` `tool_use{name: Write\|Edit\|NotebookEdit}` | `FileChangeSet{Status: inProgress}` `ModeStream` on `claude:filechange:<tool_use_id>` |
| `user` `tool_result` + `tool_use_result.structuredPatch` | `FileChangeSet{Status: completed\|failed, Changes: [{Path, Diff, ChangeKind}]}` then `ModeFlush`. Write: `type: create` gives `add`; Edit gives `update`. `Diff` is rendered as unified diff text from the hunks (Write: all `+` lines). Denied: `declined` |
| other `tool_use` (Read, Grep, Glob, WebFetch, Task, MCP tools, …) | **not mapped** in the first version, as Codex drops minor items. Option: a one-line `Log` per tool call for visibility |
| `control_request{can_use_tool}` | `ApprovalListener`, see below; not a `Read()` message |
| `result{is_error: false}` | flush open streams, then the anchor flush |
| `result{is_error: true}` | `Log` with `errors[]` / `terminal_reason` (as `pkg-agent-codex-turn-errors.md`), then the anchor flush |
| `system/init`, `system/status`, `rate_limit_event`, `system/thinking_tokens` | dropped (observer only) |

### Bash results and exit codes

Claude reports a Bash result in one of two shapes (recorded on 2.1.301):

| Outcome | `is_error` | `tool_use_result` | Output | `ExitCode` |
|---|---|---|---|---|
| exit 0 | false | `{stdout, stderr, interrupted, ...}` | stdout and stderr joined | 0 |
| non-zero exit Claude reads as success (`grep` with no match, `diff` with differences) | false | object plus `returnCodeInterpretation` (`"No matches found"`) | stdout and stderr, then `[<interpretation>]` | nil: the code is non-zero but not reported |
| non-zero exit | true | a string, `"Error: Exit code N\n<stdout>\n<stderr>"` | the text after the `Exit code N` line; stdout and stderr arrive merged | N |
| failed without an exit code (denied, blocked by settings, interrupted) or an unparsable `Exit code` line | true | a string, `"Error: <reason>"` | the text | -1 (`unknownExitCode`, as `os.ProcessState.ExitCode` for no normal exit) |

The code comes from human-readable text in the `tool_result` content, not a
protocol field. A format change falls back to -1, never to a wrong code.
| `system/permission_denied` | dropped; the decline already shows as a `declined` item |
| stderr line | `Log` `ModeSingle` |
| process exit | `Read()` error |

The partial `stream_event`s are the primary source for text and reasoning; the
complete `assistant` message per block is the source for `tool_use` input
(complete JSON instead of `input_json_delta` fragments).

---

## Approvals

Claude sends `control_request{subtype: can_use_tool, tool_name, input,
permission_suggestions, blocked_path, tool_use_id}` (E3). The adapter answers
with `control_response{behavior: allow, updatedInput}` or `{behavior: deny,
message, interrupt?}`.

### Mapping to ApprovalRequest

| tool_name | Kind | Ask |
|---|---|---|
| `Bash` | `commandExecution` | `approve command execution: <command>` (+ description) |
| `Write`, `Edit`, `NotebookEdit` | `fileChange` | `approve file changes: <path>` |
| `ExitPlanMode` | `permissions` | `approve plan:` + first lines of `input.plan` |
| `AskUserQuestion` | disallowed; if it ever arrives, denied without forwarding (see below) | |
| anything else (WebFetch, MCP tools, …) | `permissions` | `approve tool <name>: <short input summary>` |

### Mapping ApprovalDecision back

| Choice | Response |
|---|---|
| `accept` | `allow`, `updatedInput` = original input |
| `acceptForSession` | `allow` + `updatedPermissions` = Claude's `permission_suggestions` **rewritten to `destination: session`**. Claude's suggestions often target `localSettings`, which would write `.claude/settings.local.json` into the user's project. No suggestions: treat as `accept` and log it |
| `decline` | `deny`, message "The coderoom user declined this action." |
| `cancel` | `deny` with `interrupt: true`. Verified live: the turn ends with `aborted_streaming`, and Claude replaces our message with its own rejection text in the tool result |
| listener error / no listener / invalid choice | `deny`, plus a `Log` saying why |

Offered options: Bash and file changes get all four; `permissions` gets
accept/decline, as codex.

**Decided:** `acceptForSession` is supported without interface changes. Its
scope follows Claude's suggestion and is only roughly the same as Codex's:

- Bash: a rule for that exact command (comparable to Codex).
- Write/Edit: switch the session to accept edits (`setMode: acceptEdits`), so
  every later edit in the session is accepted. Broader than one change.

This difference is documented, not mapped. Verified live: after one
`acceptForSession`, a second write in the same turn is not asked, and nothing
is written under the project's `.claude/`.

### Implementation notes

- The stdout reader only parses `can_use_tool` and queues it. A separate
  worker asks the listener and writes the response, one request at a time, so
  a pending human decision never stalls `Read()`, stderr or `Stop()`.
- The listener's context is the client lifecycle context, cancelled by
  `Stop()` and on process exit. `Stop()` waits for the worker.
- A denied file change is reported as `declined`, not `failed`. The adapter
  records each tool call it denies before answering; the tool result cannot
  tell, because its text is our message on deny and Claude's own wording on
  cancel.

### AskUserQuestion

`ApprovalRequest` cannot carry the answers Claude expects (`updatedInput.answers`
keyed by question text, labels as values): `ApprovalOption` is a fixed set.

**Decided:** add `AskUserQuestion` to `--disallowedTools`. The model then asks
its questions in plain text, which in a room is visible to every participant and
answered with a normal send. No tool round-trip, and no dependence on the
wording of a denial.

As a fallback, if an `AskUserQuestion` request still arrives, the adapter denies
it without forwarding, with the message "Ask the question in your reply; the
user will answer in the room." (E10 showed the model then asks in plain text.)

Structured questions can come back later with a new approval kind (free-form
choices, answers in the decision). See
[Changes outside the package](#changes-outside-the-package).

---

## Self-started turns

Background Bash and cron start turns with no user message (E10). The session
drops stream fragments that arrive while a participant is idle and logs
"protocol: received stream fragment while idle" (`session.shouldDropIdleStreamFragment`),
and a `Send` during such a turn would get `ErrTurnInProgress` while the
participant looks idle. So these turns cannot be surfaced faithfully without
session changes.

Adapter-only approach:

1. **Prevent:** start the process with `CLAUDE_CODE_DISABLE_BACKGROUND_TASKS=1`
   and `CLAUDE_CODE_DISABLE_CRON=1` (both present in the binary; behaviour to
   verify), and add `CronCreate`, `CronDelete`, `CronList`, `ScheduleWakeup`,
   `Monitor` to `--disallowedTools`.
2. **Defend:** if a `system/init` arrives with no turn in flight, the adapter
   marks a self-started turn, suppresses its stream messages, emits one `Log`
   summarising it ("agent started a turn on its own: …" with the final text),
   and returns `ErrTurnInProgress` from `Send*` until its `result`.

**Decided:** prevent + defend for now. Self-started turns as a real feature are
a later session change.

---

## Reaching outside the room

`ListAgents` saw the user's other Claude sessions without a prompt (E10).

**Decided:** start participants with `ListAgents`, `SendMessage`,
`PushNotification` and `RemoteTrigger` in `--disallowedTools`. Adapter-only and
cheap; none of these has a role in a room. Broader isolation stays with #59.

---

## Remaining gaps and risks

1. **Bash exit code:** parsed from the `Exit code N` text of failed results,
   not from a structured field. Non-zero exits that Claude interprets as
   success have no code (nil). On failure stdout and stderr arrive merged.
   See "Bash results and exit codes".
2. **Notice reliability:** prompt-only acknowledgement; expect more
   non-compliant notices than with Codex. They surface as reasoning, which is
   already the designed fallback.
3. **Tools during notices:** Codex auto-declines approvals during notice turns
   and treats tool items as violations. The same applies here, but read-only
   tools (Read, Grep, `ls`) never prompt, so they cannot be blocked per turn;
   the adapter can only suppress and report them.
4. **Version drift:** Claude Code auto-updates (2.1.300 → 2.1.301 overnight).
   Record the version in transcripts; replay tests catch shape changes.
5. **Permission behaviour differs:** read-only commands auto-approve; path
   checks can override allow rules (E9, open question 5). Documented, not
   mapped.

---

## Changes outside the package

Not part of the current step. Listed because they are needed for the phase 3
"done when" or for full fidelity.

| Change | Package | Why | Adapter-only fallback |
|---|---|---|---|
| Select the Claude backend on `/invite` | `config` (a `backend` field in `.coderoom/participants/<alias>.yaml`, default `codex`), `session` (`AgentBackend`), `cmd/coderoom` (factory) | **Required** to use the adapter in a room at all. Today `/invite` always builds Codex unless the echo policy is on. **Decided:** backend lives in the participant config, and is shown wherever the participant appears (invite line, `/who`) so it is not hidden. An `/invite` flag can be added later as an override | none; without it the adapter is only reachable from tests |
| Generic transcript recorder | rename `cmd/codex-record` to `cmd/agent-record`; `internal/transcript` (backend-neutral `Config` and version field, existing Codex fixtures stay readable) | phase 3 replay fixtures. The actions, expectations, observer and record loop are already backend-neutral; only config, version field, client construction and a hard-coded notice anchor are Codex-specific. Replay peers stay per backend. **Decided:** `agent-record`. Discuss the concrete changes when it is needed | a test helper inside the package |
| Question approvals (free-form options, answers in the decision) | `agent` (`ApprovalRequest`/`ApprovalDecision`), `session`, `ui` | structured `AskUserQuestion` (later, optional) | disallowed; the model asks in its reply (above) |
| Agent-initiated turns | `agent` (a way to signal one), `session` (accept output while idle, mark the participant working) | background work and cron | prevent + defend (above) |

The first row is the only one the current step cannot do without. Proposal:
implement and test the adapter in isolation, then make the `/invite` change as a
small, separate step once the adapter passes its replay and integration tests.
