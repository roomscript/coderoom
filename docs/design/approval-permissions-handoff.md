# Bill: shared permission approval presentation

Status: agreed implementation scope, 2026-11-18. Implement across Codex and
Claude together. Ada prepared this design; production implementation belongs to
Bill. No prototype is required.

Update, 2026-11-19: the implemented
[approval presentation contract](approval-presentation-contract.md) supersedes
this handoff's field and composition rules. All kinds now use Presentation with
Summary, Explanation and supplemental Evidence, replacing Ask, Display and the
old explanation locations. Room composes the complete document. UX, evidence
preservation, policy and lifecycle requirements below remain applicable.

## Read first

- AGENTS.md, docs/code-review.md, docs/design/concept.md and architecture.md.
- docs/design/open/approval-reading-experience.md (implemented file/command UX).
- docs/design/pkg-agent-approvals.md and pkg-ui-approval-display.md.
- Current code listed below; older broad audit proposals are not this scope.

## Problem and outcome

Commands and file changes retain full evidence and actual decisions in history,
with compact active panels. ApprovalPermissions still displays evidence in the
panel and has no retained approval record. This shared category includes Codex
filesystem/network grants, Claude tool use (including Read, WebFetch and unknown
tools), and Claude ExitPlanMode. Improve all of these in one increment through
the existing abstraction. Do not create backend-specific UI paths or new
approval kinds solely to select presentation.

The user should see a clear summary and supplied explanation beside the choices,
with complete evidence and the actual outcome retained in history. The content
must distinguish a permission grant, tool use and a plan without treating these
as different queues or decision protocols.

## Smallest shared contract

Reuse ApprovalDetails.Display as complete adapter-normalized display evidence.
Reuse Ask as the compact permission label and add display-only Explanation to
agent.ApprovalDetails for supplied context independent of full evidence.
Project Ask and Explanation through the existing interpreter Approval
Summary/Explanation fields; broaden their command-only comments. Commands keep
deriving these from CommandOperation as today. Do not add a second evidence
store, parse Display/Ask to recover structure, or use presentation fields as
policy inputs. Keep Raw and Operation and all policy behavior unchanged.

Adapters populate these fields as follows:

| Case | Ask (compact label) | Explanation and full evidence |
| --- | --- | --- |
| Codex additional permissions | Additional permissions: filesystem, network, or both when reported; generic Additional permissions otherwise | Full supplied reason as Explanation; complete cwd, reason and permission scope in Display |
| Claude tool request | Tool: followed by the exact tool name | Existing complete tool input evidence in Display; Explanation only when a field has defined explanatory semantics |
| Claude ExitPlanMode | Plan approval | Full plan and additional input fields in Display; do not use the plan's first line as an invented explanation |

For Codex, retain unknown permission fields in full evidence, even when the
summary cannot classify them. Missing/null scope or cwd must be explicitly
unavailable/unknown, never guessed from workspace state. Preserve supplied
reason verbatim except escaping/wrapping at rendering. Claude tool JSON remains
complete, including unknown fields. Preserve existing malformed/missing input
fallbacks; give absent display evidence an explicit unavailable notice. Do not
fall back to raw protocol envelopes containing backend metadata.

The fallback for any ApprovalPermissions producer with empty Ask is
Permission request. Keep nonempty Ask in the full history heading so older
producers lose no visible information; do not repeat it as a separate context
line. An empty label must not hide the panel or choices.

## History and active panel

Extend internal/room/approval.go so ApprovalRequested creates one KindApproval
record for permissions too, using the existing ID and participant identity.
Heading: lightning Approval marker and summary; then supplied context and full
evidence, followed by the existing decision line. Retain all content without
ellipsis in normal history and Ctrl+G export. Reuse the existing renderer,
participant color and approval accent. Do not add an expansion interaction.

Make permission panels compact through the existing component boundary. Order:
participant, summary, supplied explanation when present, Full request in history,
then backend-offered choices. Reuse the bounded command layout mechanics with
the appropriate hint; keep Full command in history unchanged for commands.
Reserve choices and history hint before summary/explanation rows. Truncate only
the active panel with explicit ellipsis. Escape terminal controls before styling
and wrapping, including names, summaries, reasons, tool inputs and plans.

PgUp/PgDn while focused on a compact permission panel scrolls history, matching
commands/files. Preserve Ctrl+O, Ctrl+G, choice navigation, Esc behavior, focus
restoration and staged composer content. Keep existing file and command UX.

## Ownership and transitions

Session and its existing approvalHub remain the decision authority. Policy
evaluation precedes manual queue admission. This increment concerns manual
requests only; automatic-decision history and persistent audit files are out
of scope. Display fields do not change admission or policy evaluation.

Queued requests become visible and get their retained record only on existing
ApprovalRequested activation. ApprovalCleared updates that same record through
existing ID plus alias matching: actual choice when resolved, Withdrawn when
cancellation, backend departure or shutdown clears it without a choice. Do not
invent history for queued requests that never activated. Preserve settlement
before successor activation and existing first-winner cancellation behavior.
Already committed choices remain decisions even if cancellation happens while
the adapter response is in flight; approval never claims execution succeeded.

Room owns projection, interpreter exposes detached DTOs, UI owns rendering and
focus. Reuse existing lifetime/cleanup and serialized event delivery; add no
goroutine, queue, lifecycle state, decision cache or lock. Observers/callbacks
stay outside locks. No temporary migration path is needed: update both adapters
and consumers together, retaining the generic producer fallback above.

Preserve existing offered choices and backend response translation. Do not add
accept-for-session, cancellation options, or claim grant duration. Generic
accept/decline labels remain for this increment. In particular the existing
retained accept wording is not evidence of a backend grant's duration; changing
choice/outcome wording across approval types is separate work requiring a traced
backend semantics contract.

## Implementation order and affected components

1. Inspect internal/agent/approvals.go and both adapters' approvals code, including
   Codex rpc_approvals.go. Add and populate the shared presentation fields with
   focused adapter tests. Preserve complete existing Display evidence.
2. Extend room approval history construction using Ask as the permission label;
   reuse ApprovalCleared handling. Cover full context/evidence and fallbacks.
3. Update internal/interpreter/api/types.go and monolith/approval_views.go to
   project detached fields. Keep UI independent of agent/session packages.
4. Update internal/ui/room/approval model/layout and existing Compact-based
   routing in internal/ui/room/update.go. Reuse shared compact mechanics without
   introducing a generic renderer framework. Add flow/rendering regressions.
5. Update the reading-experience doc and relevant package design descriptions
   to describe the final behavior. Do not edit docs/for-humans; report stale
   summaries and proposed wording if encountered. Leave staging/commits to human.

## Acceptance and verification

- Table-driven adapter cases: filesystem/network/combined/unknown scope,
  full long/multiline reason and cwd, missing scope; Claude Read, WebFetch,
  unknown tool, full plan with additional fields, malformed/missing input.
- Every manual permission activation has one retained record; choice or
  withdrawal replaces its decision. Repeated identical requests remain distinct.
- Full tool inputs, plan, reason and scope survive history and transcript export;
  terminal controls are escaped. No raw protocol envelope is displayed.
- Bounded long summaries/reasons and tiny panels keep hint/choices reachable;
  request changes and Clear reset summary/explanation with no stale content.
- Mixed file/command/permission FIFO, stale response, cancellation/departure/
  shutdown, settlement-before-successor, history paging and composer restoration
  remain correct. Extend existing fakes/tests rather than add parallel harnesses.
- Run focused agent, room, interpreter and UI tests, then make pre-commit and
  git diff --check. Run Go normally first; follow repo Snap escalation guidance
  if confinement fails. Live quota-consuming integration is not required for
  this presentation change. Report any live keyboard checks separately from
  automated verification.

No unresolved product decision blocks this scope. The main risks are losing
unknown backend fields, accidentally changing authorization via display data,
or implying grant scope from generic labels; the contracts above bound them.
