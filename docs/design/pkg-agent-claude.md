# Package design: internal/agent/claude

Status: **implemented**, registered as backend `claude`. Decisions agreed on
16 October 2026 are marked **Decided**. Evidence: `docs/playground/poc-claude-stream-json.md` (E1 to E10).

The adapter was built in `internal/agent/claude` without changes to other
packages. Backend selection and registration came after it
(`multi-backend-config.md`). [Changes outside the package](#changes-outside-the-package)
lists what is done and what remains.

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
| `WithContext` | worker lifecycle. Unlike codex, cancelling it also stops the process: the approval worker ends with the lifecycle, so a running process would wait forever on its next permission request |
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
| `Send(prompt)` | fits | write `{"type":"user","message":{"role":"user","content":prompt},…}`; return the turn anchor (below). `ErrTurnInProgress` while a turn is in flight, as codex. A leading `/` is escaped, see [Prompts starting with /](#prompts-starting-with-) |
| `SendNotice(prompt)` | fits with a weaker guarantee | same prompt prefix and `{` heuristic as codex: reply text starting with `{` is held until the turn ends and dropped if it is a JSON object with `"acknowledge": true`; any other reply is relayed as `Reasoning` on `claude:notice-relay`. Thinking is dropped; tool calls, approvals and logs pass through, as with Codex delivery notices. **Gap:** Codex also sends a per-turn output schema; Claude's `--json-schema` is per process and not enforced (E7), so the instruction is prompt-only. Haiku complied in the live test |
| `Read()` | fits | translate stdout lines (mapping below); stderr lines as `Log`; error only when the process exits. Lines have no length limit (one line can carry a whole file); a read error is logged and the process killed, so `Read()` returns an error instead of hanging |
| `Interrupt()` | fits | `control_request{subtype: interrupt}`. The turn then ends with `result{subtype: error_during_execution, terminal_reason: aborted_streaming}`, or `aborted_tools` when interrupted during an approval; the adapter treats it as a normal turn end (Log + anchor flush), not a `Read()` error. No-op when idle. With no turn running, it releases a prompt that outlived a turn unanswered (see [Turn anchor](#turn-anchor)) |
| `Stop()` | fits | close stdin (exits in about 1 s, E8); after a grace period SIGTERM, then kill; always reap. After the kill, our pipe ends are closed too, so a child that inherited them (a wrapper, an MCP server) cannot keep `Stop()` waiting. Not safe during `Start()`, which the session never does: it attaches an agent only after `Start()` returns. Cancelling the `WithContext` parent aborts a `Start()` in progress |
| `Keepaliver` | **postponed** | `Keepaliver` exists to keep a backend warm (Codex takes about 5 min from cold to warm). No equivalent cold state has been seen for Claude: the session lives in the process and the local transcript. The only idle effect observed is prompt-cache expiry (1 h cache in the E1 to E10 runs): the first turn after a long idle re-reads the context uncached, costing seconds and money, not minutes. Not measured on a large context. If Claude turns out to have a pause or cold mode, or cache misses hurt, implement it (for the cache, a minimal turn every ~50 min). The session treats `Keepaliver` as optional, so nothing breaks meanwhile |

### Turn anchor

The adapter owns the turn boundary: every turn ends with exactly one `result`.

- `Send` returns a constant anchor `claude:turn`; on `result` the adapter emits
  `{StreamID: "claude:turn", Mode: ModeFlush, Content: Output{}}` after
  flushing any open item streams.
- `SendNotice` returns `claude:notice-turn`, flushed the same way, as codex.
- The replayed user message (`isReplay: true`, about 1 s after the write) decides
  whose turn it is: a turn is ours once Claude replays the prompt `Send` or
  `SendNotice` wrote. Each prompt is sent with a random `uuid`, which Claude
  echoes in the replay (checked live on 2.1.303); the replay claims the turn
  only if the uuid matches the pending prompt. The text is not compared,
  since Claude may change it. A replay with another uuid is of a prompt
  `Interrupt` released (below), and its turn is marked self-started at the
  replay, so it cannot be claimed for the pending prompt even if it ends
  without output. Without the uuid, a late answer to the released prompt
  would complete the next prompt's turn. Recordings show the order `system/init`,
  replay, first output in every turn, so model output before the replay
  belongs to a turn Claude started on its own (see
  [Self-started turns](#self-started-turns)). Any `stream_event` counts as
  model output, even one the decoder maps to nothing, such as an unmapped
  tool. A local slash command's synthetic assistant message has no stream
  events, so it does not count. If the replay arrives inside a self-started
  turn, the turn becomes ours from there: the output before it is
  summarised, and items still open are dropped, so their remaining output
  is ignored instead of joining our turn. A turn that ends before any
  replay or output is ours if our prompt is pending: an API error or a local
  slash command ends it without a replay. A `result` with no `system/init`
  counts the same. A `user` message with string content but no `isReplay` (a
  sub-agent's prompt) is ignored.
- One case leaves the prompt unanswered: a self-started turn with output
  that ends while our prompt is pending, followed by a turn answering the
  prompt without a replay. Not observed; self-started turns are prevented.
  `Interrupt` recovers from it: a prompt that outlived a turn without being
  claimed is released with a `Log` and the anchor flush. A prompt that has
  seen no turn end yet is left alone, since Claude may be about to start its
  turn; releasing it would leave the room idle while the turn runs. If
  Claude answers a released prompt after all, its replay carries the old
  uuid, so the answer is summarised instead of joining a later turn.

This answers open question 8: the anchor is adapter-local and needs no protocol
id.

### Prompts starting with /

Room text reaches `Send` unchanged, so "@ada /clear the cache" arrives as
`/clear the cache`. Claude Code runs a prompt that starts with `/` as one of
its own commands instead of passing it to the model. Checked live on 2.1.303:

- `/cost` printed usage locally: `system/init`, one assistant message with
  model `<synthetic>`, then a successful `result`. No replay, no stream
  events, no model call. Before the turn claiming rules above, this left the
  client busy until `Stop`.
- `/nonexistent` (not a command) reached the model and was replayed.
- `!touch bang.txt` and `#remember …`, the bash and memory shortcuts of the
  interactive mode, reached the model and were replayed. Nothing ran
  locally: the model chose a Bash call for the first, which asked for
  approval through `can_use_tool`. Only a leading `/` needs escaping.
- `/clear`, `/compact` and `/model` would act on the participant's session
  behind the room's back.

`Send` puts a backslash before a leading `/` (`\/clear the cache`), and the
model reads it as text. A visible escape was chosen over a leading space,
which also worked, because a space is easy to miss in logs and diffs and
would break if Claude started trimming prompts.

`TestClientLiveSlashPrompt` guards this, since only a live run notices a
change in Claude's rules. It sends `/cost …` and expects a reply: `/cost` is
harmless, and run locally it streams no output, so an empty reply means the
escape stopped working. If room users should ever run such commands, they
need a coderoom command that the adapter maps, not text passed through.

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
| `user` `tool_result` + `tool_use_result.structuredPatch` | `FileChangeSet{Status: completed\|failed, Changes: [{Path, Diff, ChangeKind}]}` then `ModeFlush`. Write: `type: create` gives `add`, with the content as `Diff`; anything else gives `update`, with unified diff text from the hunks. An unrecognised shape is an `update` without a diff, never an `add`. Denied by coderoom: `declined` |
| `user` `tool_result` for NotebookEdit | `update`. Its result has its own snake_case shape (`notebook_path`, `cell_id`, `edit_mode`, `old_source`, `new_source`) and no hunks; `Diff` is the cell source rendered as `-` and `+` lines under `@@ cell <id> (<edit_mode>) @@` |
| other `tool_use` (Read, Grep, Glob, WebFetch, Task, MCP tools, …) | **not mapped** in the first version, as Codex drops minor items. Option: a one-line `Log` per tool call for visibility |
| `control_request{can_use_tool}` | `ApprovalListener`, see below; not a `Read()` message |
| `result{is_error: false}` | flush open streams, then the anchor flush |
| `result{is_error: true}` | `Log` with the provider's `result` explanation and any nonblank `errors[]` details, then the anchor flush. When only `errors[]` supplies text, prefix it with "turn failed: ". This applies even when `subtype` is `success`. Without explanatory text, log "turn failed" with the subtype (except `success`) and `terminal_reason` when available. `aborted_streaming` and `aborted_tools` (interrupted during an approval) still log "turn interrupted" |
| tool call still open at `result` (cut off by an interrupt) | file change: `failed`; command: `ExitCode` -1; then the flush |
| `system/init`, `system/status`, `rate_limit_event`, `system/thinking_tokens` | dropped (observer only) |
| `system/permission_denied` | dropped; the decline already shows as a `declined` item |
| stderr line | `Log` `ModeSingle` |
| process exit | `Read()` error |

The partial `stream_event`s are the primary source for text and reasoning; the
complete `assistant` message per block is the source for `tool_use` input
(complete JSON instead of `input_json_delta` fragments).

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
- Each request gets its own context, derived from a per-turn context, derived
  from the client lifecycle context. Claude withdraws a pending request with
  `control_cancel_request` when the turn is interrupted (verified live); that
  cancels the request's context. The end of the turn cancels whatever is
  left, and `Stop()`, process exit and the end of the parent context cancel
  everything. A withdrawn request
  is not answered (Claude ignores late answers), is not logged as declined,
  and is dropped without asking the listener if it was still queued. This is
  best effort: a withdrawal that lands between the worker's check and its
  write still gets an answer, which Claude ignores.
  `Stop()` waits for the worker.
- A denied file change is reported as `declined`, not `failed`. The adapter
  records each tool call it denies before answering; the tool result cannot
  tell, because its text is our message on deny and Claude's own wording on
  cancel. Claude emits no `system/permission_denied` for host denials
  (checked live), so this cannot be derived from the stream.

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
   and `CLAUDE_CODE_DISABLE_CRON=1`, and add `CronCreate`, `CronDelete`,
   `CronList`, `ScheduleWakeup`, `Monitor` to `--disallowedTools`. Verified on
   2.1.301: the cron switch removes the three cron tools from `system/init`;
   the background switch removes `run_in_background` from Bash, so the call
   fails validation. The model may then fall back to a shell `(...) &`, which
   Claude does not track and cannot start a turn from.
2. **Defend:** a turn whose first stream event arrives before our prompt is
   replayed is self-started (see [Turn anchor](#turn-anchor)). The adapter
   suppresses its stream messages, emits one `Log` summarising it ("agent
   started a turn on its own: …" with the final text), and returns
   `ErrTurnInProgress` from `Send*` until it ends. Permission requests in
   that turn are denied without asking the listener, with a `Log`: the room
   shows the participant as idle, so nobody could judge them. Ownership is decided from
   the replay, not from local state, so a self-started turn that begins just
   after `Send` is not mistaken for the reply.

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
3. **Tools during notices:** as in Codex delivery notices, tool calls and
   approvals during a notice turn pass through to `Read()` and the approval
   listener. (Codex treats tools as violations only in keepalive turns, which
   this adapter does not have.)
4. **Version drift:** Claude Code auto-updates (2.1.300 → 2.1.301 overnight).
   Record the version in transcripts; replay tests catch shape changes.
5. **Permission behaviour differs:** read-only commands auto-approve; path
   checks can override allow rules (E9, open question 5). Documented, not
   mapped.

---

## Changes outside the package

Needed for the phase 3 "done when" or for full fidelity.

| Change | Package | Why | Adapter-only fallback |
|---|---|---|---|
| Select the Claude backend on `/invite` | `config`, `internal/agent/registry`, `session`, `cmd/coderoom` | **Done:** the `backend` field in the participant config, shown wherever the participant appears (invite line, `/who`). See `multi-backend-config.md`. An `/invite` flag can be added later as an override | not needed |
| Generic transcript recorder | rename `cmd/codex-record` to `cmd/agent-record`; `internal/transcript` (backend-neutral `Config` and version field, existing Codex fixtures stay readable) | phase 3 replay fixtures. The actions, expectations, observer and record loop are already backend-neutral; only config, version field, client construction and a hard-coded notice anchor are Codex-specific. Replay peers stay per backend. **Decided:** `agent-record`. Discuss the concrete changes when it is needed | a test helper inside the package |
| Question approvals (free-form options, answers in the decision) | `agent` (`ApprovalRequest`/`ApprovalDecision`), `session`, `ui` | structured `AskUserQuestion` (later, optional) | disallowed; the model asks in its reply (above) |
| Agent-initiated turns | `agent` (a way to signal one), `session` (accept output while idle, mark the participant working) | background work and cron | prevent + defend (above) |
