# Approval presentation and retained decisions

Status: broader proposal deferred; targeted presentation increment, 2026-11-14.
Scope follows [the handoff](approval-presentation-handoff.md). Approval policy
coverage and backend permissions remain separate work.

Manual file changes now follow the implemented
[reading experience](open/approval-reading-experience.md): full room evidence,
compact file box and retained actual decisions. The presentation-only scope
below describes the earlier increment and continues to apply to other types.

## Current scope: presentation only

The active manual panel uses a normal participant header in its assigned color,
followed by yellow `⚡ Approval:`, full existing request evidence and
`Decision: Waiting for you`. Escaping happens before wrapping and trusted
styling. Existing paging, option priority on tiny terminals, focus restoration,
FIFO policy and dismissal after a response remain in place. Participant colors
exclude yellow/amber, with regression coverage for actual generated colors.
ANSI color 3 uses the terminal's yellow; contrast depends on its theme.

This increment does not add occurrence identities, retained transcript records,
new lifecycle events, cancellation changes, shutdown draining or audit files.
The panel still disappears after resolution. Automatic decisions remain in the
existing diagnostic logs. Full on-screen evidence requires no new retention
policy decision; persistence to an audit file is a separate question.

The remaining sections describe a deferred history/audit proposal, not
requirements for this presentation patch.

## Problem and acceptance criteria

Approvals currently appear as ordinary diagnostic text, and the manual panel
disappears after a response. Raw backend traffic cannot distinguish a policy
decision from a human choice. The next change should make each occurrence
recognizable, preserve its decision and explanation, and support later analysis
without reconstructing state from log strings.

Acceptance criteria:

- Every admitted request has one retained transcript record, updated in place.
  Identical requests receive distinct identities, including automatic requests.
- Records show participant, action category, lifecycle state, decision origin,
  final choice/scope and stable policy reason when present.
- One approval accent distinguishes records and the active panel. Participant
  names retain their assigned colors; words carry meaning without color.
- Show the full request inline for automatic and manual records, without
  truncation or collapsed previews. Manual decisions use the single FIFO queue.
- A structured audit distinguishes automatic/human choices, cancellation and
  failure. Neither policy Review nor a canceled evaluation counts as approval.
- Approval does not assert that a backend received a response or executed work.
- No raw request, secret-bearing command, URL, header, patch or scanner finding
  enters the audit. All approval display paths preserve terminal escaping.

## Observed implementation

`internal/session/approval_policy.go` evaluates policy before entering
`approvalHub`; its SHA-256 digest includes evidence and raw backend bytes.
`approvalQueue.approvalID` is currently allocated only for manual requests.
`approvalHub` owns FIFO admission, choice validation and cancellation, and drains
its event queue outside its mutex. Only the queue head emits ApprovalRequested.
Queued cancellation and resolution followed by a successor do not necessarily
emit a clear event for the previous request.

Policy AgentLog is emitted before a final context check. Human logging follows
the listener return. `approvalHub.request` can discard a choice already removed
from the queue when cancellation wins its subsequent context check. Thus these
logs are not a reliable decision lifecycle.

`internal/room/events.go` projects AgentLog into KindLog. Canonical records live
in `internal/room`, owned through the interpreter; indexed TranscriptChanged
deltas already support replacing a record. `monolith/approval.go` independently
projects the active panel and clears it after successful ResolveApprovalCommand.
There is no structured approval history today.

`internal/ui/room/history/record` already provides compact command previews and
full RenderTranscript output. Ctrl+O focuses history; Ctrl+G exports a transcript
to a temporary file for the editor. There is no established per-record expansion
interaction. Approval-panel PgUp/PgDn already pages request content.

`cmd/coderoom/main.go` opens optional --agent-log and passes its writer only to
backend factories. No general events.jsonl persistence is wired here. The
architecture's session-directory event log is a target architecture, not a
dependency this change may assume exists.

## Recommended boundary

Extend the existing approval hub to own occurrence admission and settlement for
all approvals. Keep policy evaluation in Session, outside locks. Do not introduce
another queue, approval manager, or decision database. The hub keeps only active
occurrences; the canonical transcript keeps settled history.

Allocate a monotonic occurrence number before evaluation, under the hub mutex.
Reuse it as the manual queue ID, rather than translating two counters. A random
run UUID plus this number identifies occurrences across appended audit files.
The existing digest is optional correlation metadata, never the record key or
an authorization input. Do not retain raw backend request IDs in audit records.

Publish one typed `ApprovalChanged` Session event carrying an immutable snapshot
of that occurrence. It contains facts, no colors, markup or backend JSON. The
same ordered event feeds room projection and the CLI audit observer. Suggested
contract fields (names may follow existing package conventions):

| Field | Contract / concrete consumer |
|---|---|
| run ID, occurrence ID, revision, UTC time | Audit correlation, ordering and deduplication |
| participant alias with UUID, backend, kind | Attribution across participant replacement |
| phase | evaluating, queued, awaiting_human, decided, withdrawn, failed |
| policy outcome, reason, policy identity, optional scanner version/config | Explain evaluation independently from a final response |
| decision | Present only for decided: origin policy/human and normalized choice |
| withdrawal/failure code | Static category, never arbitrary error text |
| safe action category and safe detail blocks | Transcript rendering and inspection only; omitted from audit serialization |

Derive approved/declined/interrupted labels and once/session scope from the
normalized choice; do not store redundant outcome booleans. Human OptionCancel
means a selected backend interruption choice; withdrawal means no choice was
committed. Policy Review is an evaluation result, never a terminal decision.
Policy-disabled sessions still admit and record human requests.

Add KindApproval and a detached approval payload to room.Record. Room maps live
occurrence IDs to canonical indices, appends on admission and replaces the same
index on updates. Remove the live index entry on settlement; the record remains.
The interpreter exposes detached values through its existing API boundary. UI
imports neither Session nor agent packages. Approval records do not become
agent output, handoff sources, or input broadcast to other participants.

## Ownership, races and cleanup

| Transition | Owner and trigger |
|---|---|
| absent -> evaluating | Hub admits a validated participant request before policy work |
| evaluating -> queued / awaiting_human | Session supplies Review or unavailable automatic choice; hub adds to existing FIFO |
| queued -> awaiting_human | Hub promotes the head after settlement/removal |
| evaluating -> decided | Session supplies policy choice; hub commits if still live |
| awaiting_human -> decided | Existing resolve command validates ID and offered choice; hub commits |
| any unsettled -> withdrawn | Request/participant/session cancellation wins settlement |
| any unsettled -> failed | Non-cancellation internal failure prevents a choice |

Settlement is a single mutex-protected operation shared by cancellation,
automatic results and human resolution. Check cancellation at this boundary.
The first accepted terminal transition wins; subsequent results are ignored.
If cancellation occurs after commitment, preserve decided and return its choice
from the listener. Remove the current post-choice context veto. The adapter may
still fail to send it; the record explicitly means committed listener decision,
not delivered protocol response. Tracking delivery would require a separate
adapter acknowledgement contract and is outside this increment.

Append lifecycle events under the same mutex, then drain them outside it using
the existing reentrant ordered publication mechanism. Never evaluate policy,
notify observers, write audit data or run callbacks while holding that mutex.
Terminal event for the old head precedes activation of its successor. A response
must not become available to the adapter before its committed event has entered
the ordered publication path. Reentrant resolution must remain supported.

Keep evaluation contexts linked to request and Session lifetime even when policy
is disabled. Shutdown stops admission, cancels outstanding evaluation and queued
requests, waits for listener/evaluator cleanup, drains lifecycle publication,
then closes audit output. Scanner cleanup remains the evaluator's responsibility.
Participant exit/removal cancels its request contexts without touching another
participant instance with the same name. Tests must establish that adapter exit
actually reaches these contexts. Decisions already committed survive shutdown.
Unexpected process termination may leave nonterminal audit entries; analysis
must call these incomplete, never infer withdrawal or approval.

The active Approval DTO remains a queue-head projection. Replace the independent
optimistic clear in resolveApprovalOperation with lifecycle-driven presentation:
clear only the matching settled head, then apply any successor. A stale command
cannot clear a newer panel. No transcript or UI consumer chooses the winner.

## Presentation and inspection

User-agreed presentation: preserve the normal participant header (`● ada:`) in
the participant's existing color. Below it, render the lightning marker and
`Approval:`, all request details and the final `Decision:` line in yellow.
Reserve yellow for approvals; remove nearby yellow/gold/amber colors from the
participant palette at its existing assignment owner. Select the exact yellow
token and palette exclusions with light/dark terminal readability checks.
Do not encode outcome with separate red/green palettes. Verify lightning text
presentation and cell width on supported terminals. Plain-text equivalents:

```text
● ada:

  ⚡ Approval:
  Write docs/design/architecture.md
  Reason: file.project_contained
  Decision: Approved automatically · once

● turing:

  ⚡ Approval:
  <full command and request details>
  Reason: shell.cwd_unvalidated
  Decision: Waiting for you

● ada:

  ⚡ Approval:
  HTTP GET <redacted request details>
  Reason: get.secret_detected
  Decision: Declined automatically
```

The waiting record changes in place to `Decision: Approved by you · once`,
`Declined by you`, or the other actual outcome; it is not appended twice.
Withdrawal says `Decision: Withdrawn · participant stopped` (or the actual
cause), distinct from a human's cancel choice. Queued requests say
queued, rather than implying that they are currently selectable. Keep the
existing focus and staged-composer behavior. Automatic decisions never steal
focus. The active panel shows the same ID, participant and policy explanation,
followed by safe details and existing choices; preserve paging and tiny-terminal
option priority. Show the full request inline in the normal transcript too:
no line-count limit, ellipsis, collapse or separate expansion interaction.
The user expects most requests to occupy 1–10 lines and explicitly prefers to
revisit length only after observing actual use. Ordinary viewport scrolling and
manual-panel paging still apply; neither drops content. Fullness concerns
presentation, not permission to persist secrets; the content boundary below
remains a separate decision. Occurrence IDs remain structured metadata and
available in transcript inspection rather than cluttering the normal header.
Ctrl+G writes a file, so it must never bypass
redaction. No new expansion keys or interactive history record state are needed.

## Content boundary and pending product choice

The metadata audit below is the recommended minimum. A user clarification is
pending on whether retained content should also be persisted. Do not implement
content persistence or claim arbitrary text can be safely redacted until that
choice and its contract are settled.

Even session-only inspection needs a defined safe-content boundary. Gitleaks is
optional and pattern-based; a clean scan cannot certify arbitrary command or
patch text as secret-free. Recommend conservative typed summaries for retained
records: action category, number of targets and validation completeness. Omit
raw URLs, headers, bodies, arbitrary shell operands, file contents and paths by
default. Known sensitive fields are wholly replaced with labeled omissions.
Unknown operation/tool payloads show category and an explicit content-unavailable
notice, never a raw JSON fallback. Preserve reason codes and scanner identity.

This conservative choice limits useful command inspection. If the human wants
full commands/patches retained, the design must specify the accepted residual
secret risk or a trusted redaction source before builder handoff. That requirement
is unresolved; metadata and lifecycle work can proceed independently. Active
manual review currently exposes raw details; replacing that with only summaries
would impair informed consent and requires the same clarification. Do not
silently remove decision evidence as part of a presentation refactor.

Terminal escaping is separate from redaction. Reuse the exact escaping contract
in pkg-ui-approval-display.md for every untrusted approval field, before wrapping
and trusted styling, including alias, reason fallback, option label, summaries,
full transcript output and the manual panel. Extract the existing helper into a
small UI utility only when adding the second renderer. Store source values, not
parallel escaped caches. Do not render approval evidence as Markdown, links or
ANSI supplied by the backend.

## Audit persistence

Recommend a separate optional `--approval-log <file>` JSONL stream. Mixing new
records into --agent-log risks breaking existing protocol readers and couples
safe decision metadata to deliberately raw traffic. A general session event
store would exceed the current requirement. With no flag, retained history is
in memory only; make that distinction clear in CLI help.

CLI owns file opening, observer registration and closing. Open before starting
participants, append with mode 0600 for new files, and serialize writes through
the ordered approval event drain. Reject the same destination as --agent-log
(including existing aliases to the same file). Use a schema-versioned allowlist
serializer of ApprovalChanged, never generic event marshaling: safe display
detail blocks and raw request data are excluded structurally. Audit includes
each revision, so analysis can select the last event per (run, occurrence).

Example terminal event, abbreviated:

```json
{"schema":1,"run":"<uuid>","occurrence":41,"revision":2,"phase":"decided","participant_id":"<uuid>","participant":"ada","backend":"codex","kind":"fileChange","decision":{"origin":"policy","choice":"accept"},"policy":{"identity":"approval-policy-v2","outcome":"approve","reason":"file.project_contained"}}
```

Opening failure fails startup if the flag was requested. A write failure latches
the audit as failed, emits one visible structured diagnostic outside approval
locks, and closes/disables the sink. Continue approvals under existing policy;
do not manufacture a decline or claim audit completeness. Report failure on
stderr at exit too. This is an inspection log, not a durable authorization
journal: no fsync or fail-closed permission semantics. Synchronous small writes
avoid another worker/queue; document that slow storage can delay publication.
Revisit buffering only with measured need and an explicit overflow contract.

## Implementation sequence and verification

1. Resolve retained/manual content policy above. Add typed lifecycle contracts
   and safe metadata projection with explicit allowlists; retain policy behavior.
2. Extend approvalHub admission and settlement across automatic/manual paths.
   Allocate IDs before evaluation; reuse the current FIFO and event drain.
   Add race tests for cancellation during evaluation, commitment and publication,
   head promotion, queued withdrawal, stale choices and reentrant observers.
3. Project KindApproval into canonical room records and indexed deltas; drive
   the panel from the same lifecycle. Test detached snapshots, exactly one record
   per occurrence, replacement identity, ordering and stale-panel clearing.
4. Add normal participant headers, yellow full-request rendering, palette
   exclusions and shared terminal escaping. No compact approval format.
   Test long and malicious fields, resize, monochrome, selection/paging, staged
   composer and transcript export. Verify no details reach agent prompts.
5. Add CLI JSONL observer and flag. Test append across runs, schema fields,
   origin/scope, duplicate identical requests, all terminal states, secret canary
   exclusion, write/open failures and shutdown draining with work in flight.
6. Remove policy v2 AgentLog emission in the same cutover that enables the typed
   projection and audit. Preserve unrelated AgentLog. No text-parsing migration.
   Remove legacy approval requested/cleared events once all consumers use the
   typed lifecycle; do not leave competing event paths indefinitely.

Run focused Session/room/interpreter/UI tests with the race detector, repository
lint and full unit suite, then git diff --check. Recorded adapter tests should
verify choices remain protocol-compatible. Live backend/quota tests are not
needed for rendering; cancellation wiring may need a focused existing adapter
integration test if recorded/fake coverage cannot establish process-exit behavior.

## Alternatives and remaining risks

- Styling AgentLog alone is smaller but cannot provide occurrence identity,
  reliable cancellation or one mutable record. Reject it.
- Persisting interpreter text would duplicate UI interpretation and risk leaking
  evidence. Use Session lifecycle facts and a separate allowlist serializer.
- Compact previews and inline expansion are deferred by user choice. Show full
  requests and reconsider only if real use demonstrates excessive length.
- A decision is permission intent, not execution success or delivery receipt.
  Do not join it to command success by matching command text or request digest.
- Current concept docs describe private approval tabs, but the current canonical
  transcript/UI is a room presentation. Keep records local to the human UI and
  out of agent context; a private-tab redesign is separate scope.
- docs/for-humans may need updated approval/log instructions after implementation.
  Discuss proposed wording with the human before editing those files.
