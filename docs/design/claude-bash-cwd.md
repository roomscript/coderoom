# Design: per-call cwd for Claude Bash approvals

Status: proposed, option A verified. Evidence: E14 and E15 in
`docs/playground/poc-claude-stream-json.md`.

## Problem

Claude Bash approvals reach policy without a cwd. `approvalRequestFor`
(`internal/agent/claude/approvals.go`) sets `CommandOperation{Command}` only,
so `Policy.evaluateShell` returns `shell.cwd_unvalidated` for every Claude Bash
request (197 of 519 in the replay corpus, `approval-policy-test-cases.md`) and
the room shows `cwd: unknown`.

The cwd cannot be derived on our side. The Bash shell is session state: a `cd`
inside the project persists across calls and turns, and a `cd` outside it is
reset after the call (E6, E14). The launch cwd and the per-turn `system/init`
cwd are both wrong after a `cd` (E14 call 7), and `can_use_tool` has no cwd
field. `approval-policy-test-cases.md` rules out inventing one: the cwd must be
backend evidence.

## Evidence

Observed in Claude Code 2.1.325, three runs (E14):

- The `PreToolUse` hook input carries `cwd` and `tool_use_id`. Its `cwd` was
  the shell cwd at the start of the call in every call, including after a
  persisted `cd`, a `cd` earlier in the same turn, the reset after leaving the
  project, and a `cd` by an earlier call of the same parallel batch.
- Claude waits for the hook to finish before it sends `can_use_tool` for the
  same call.
- Bash calls requested in parallel ran one at a time (observed, not
  documented).

E15, same version: the CLI accepts the hook in the `initialize` request
(`hooks: {PreToolUse: [{matcher: "Bash", hookCallbackIds: [<id>]}]}`) and
calls it with `control_request{subtype: "hook_callback", callback_id,
tool_use_id, input}`, one per Bash call, before that call's `can_use_tool`,
and waits for the answer. `input` is the E14 hook input; `input.cwd` matched
E14 for every call. Answering `{}` left approvals, execution and results
unchanged.

## Acceptance criteria

1. A Claude Bash approval carries `CommandOperation.Cwd` equal to the hook
   `cwd` for its `tool_use_id`, so policy evaluates it like a Codex command.
2. Without a hook entry for the call, `Cwd` stays empty and the result is
   `shell.cwd_unvalidated`, as today. The adapter never substitutes the launch
   cwd or a cwd from another call.
3. The hook never changes Claude's behavior: it does not allow, deny or
   rewrite anything, and a malformed hook input still gets the neutral answer.
4. No new files, processes or settings outside the Claude process.

## Options

| Option | Delivery | Cost | Verdict |
|---|---|---|---|
| A. In-band SDK hook | register in `initialize`, answer `hook_callback` on the stdout reader | small: one registration, one control subtype, one map | **Recommended**. Verified by E15 |
| B. Command hook through `--settings` | a shell command appends the hook input to a per-session file; the adapter reads it on `can_use_tool` | a temp file per session, its cleanup, a shell process per Bash call, partial-line reads, a second `--settings` source next to the user's | Rejected: same evidence as A at a higher cost. Verified by E14 |
| C. Infer from the commands | track `cd` across calls in the adapter | must model Claude's reset rule and `--add-dir`; not backend evidence | Rejected |
| D. `blocked_path`, `addDirectories` suggestion, `system/init` cwd | already on the wire | absent or stale for many calls (E14) | Rejected |

A keeps all state in the stdout reader goroutine, needs no file or subprocess,
and is the mechanism the Agent SDK itself uses. Its ordering follows from the
protocol: Claude waits for our `hook_callback` answer before it can send the
call's `can_use_tool`, and the reader writes that answer before it reads the
next line.

## Design (option A)

### Registration

`Start` sends, instead of `"hooks": nil`:

```json
{"subtype": "initialize",
 "hooks": {"PreToolUse": [{"matcher": "Bash", "hookCallbackIds": ["coderoom-bash-cwd"]}]}}
```

The user's own hooks still run; ours is added to them (acceptable, decided
with the human on 2026-11-20).

### Ownership and state

| State | Owner | Written by | Read by | Cleared by |
|---|---|---|---|---|
| `approvalTurn.bashCwd map[tool_use_id]cwd` | stdout reader goroutine | `hook_callback` for our id | `queuePermission` for a Bash `can_use_tool` (entry deleted on read) | `endApprovalTurn`, and `Start` through `newApprovalTurn` |

Source of truth: Claude's hook input. The map is a short-lived relay between
two messages about the same call; nothing else decides a cwd.

No lock: the map lives next to `approvalTurn.pending`, which has the same
single-goroutine rule. The approval worker receives the cwd as a value in
`permissionRequest`, never the map.

### Flow

1. Claude decides to run Bash and sends `hook_callback` (our callback id,
   `input.tool_use_id`, `input.cwd`).
2. `handleControlRequest` routes `hook_callback` to a new handler on the
   reader: if `input.cwd` is an absolute path and `tool_use_id` is set, store
   it; in every case answer `control_response{subtype: success, response: {}}`
   at once. An unknown callback id gets the existing control error.
3. Claude sends `can_use_tool`. `queuePermission` takes the entry for its
   `tool_use_id` (if any) into `permissionRequest.bashCwd`.
4. The approval worker calls `approvalRequestFor(tool, bashCwd)`, which sets
   `CommandOperation{Command, Cwd: bashCwd}`. Presentation and the room
   heading already read `Operation.Cwd`.

A Bash call that needs no approval (allow rule, read-only) leaves an entry
that is never read; the turn end clears it.

### Failure, cancellation, shutdown

- No `hook_callback` (older CLI, hooks disabled by managed settings, the hook
  skipped): no entry, empty `Cwd`, today's behavior.
- Interrupt: the hook answer is immediate, so nothing waits on us. A
  `control_cancel_request` for a hook request finds nothing pending and is
  ignored.
- Self-started turns: the hook is answered as usual; `queuePermission` still
  denies the tool and the entry is cleared at turn end.
- Stop or process exit: the map goes with the turn state; there is no file or
  goroutine to clean up.

### Out of scope

- `agent.Command.Cwd` in `Read()` output. The `tool_use` message is decoded
  before the hook runs, so this would need a later fill-in; no consumer needs
  it now.
- Subagent Bash calls (`agent_id` in the hook input). They get whatever cwd
  the hook reports; a cwd outside the project still goes to review.

## Implementation plan

1. `wire.go`: `controlHookCallback` constant, callback id, `hookCallback`
   request body (`callback_id`, `tool_use_id`, `input.cwd`, `input.tool_use_id`).
2. `client.go`: registration in the `initialize` request.
3. `workers.go` / `approvals.go`: route `hook_callback`, keep the map in
   `approvalTurn`, hand the cwd to `permissionRequest`, extend
   `approvalRequestFor`.
4. Tests (fakes, table-driven): cwd attached when the hook preceded the
   request; empty without it; malformed or relative cwd ignored and still
   answered; entry not reused by another `tool_use_id` or after the turn ends;
   unknown callback id rejected; initialize payload carries the registration.
5. Transcripts: the replay matcher compares the `initialize` line in full, so
   the recorded scenarios (2.1.304) must be re-recorded, and new recordings
   contain `hook_callback` exchanges. Add a scenario where a Bash call runs
   after a `cd`.
6. Docs at implementation: `pkg-agent-claude.md` (invocation, approvals),
   `approval-presentation-contract.md` (Claude Bash row: cwd from Operation),
   `approval-policy-test-cases.md` (remove the open item). Point out to the
   human any `docs/for-humans/` text that becomes stale.

## Verification

- `make test`, `make lint`.
- `make test-integration-claude`: a live run where `cd sub` then a write in
  `sub` shows `cwd: <root>/sub` in the approval, and policy no longer returns
  `shell.cwd_unvalidated` for it.
- Replay with the re-recorded fixtures.

## Risks

- Undocumented CLI internals: the `hook_callback` shape and the hook `cwd`
  semantics may change with Claude Code updates. The fallback is safe (review),
  and the live sweep catches drift.
- The parallel-call result rests on Claude running Bash calls one at a time.
  If Claude ran them concurrently, a hook could report the cwd before an
  earlier call's `cd`. The policy would then judge the command against the
  wrong directory. The live sweep should keep a parallel case.
- `--add-dir` directories may keep a `cd` instead of resetting it; the hook
  reports whatever Claude uses, so this affects only expectations, not
  correctness.
