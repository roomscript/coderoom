# Approval presentation contract

Status: implemented, 2026-11-19.

This contract replaces the field/composition rules in
[the permission handoff](approval-permissions-handoff.md). Existing
file, command and permission UX, policy and settlement requirements remain.
The implemented behavior is described in
[approval reading experience](open/approval-reading-experience.md).

## Problem and constraints

The current UX needs three things: an action summary, an optional supplied
explanation, and complete request evidence retained with the actual decision.
The model does not express those responsibilities consistently:

- Ask is a permission label but a formatted command/file prompt.
- CommandOperation.Explanation and ApprovalDetails.Explanation own the same
  concept for different kinds.
- Display includes the permission explanation. History assumes any nonempty
  Display includes it, while the panel reads the separate Explanation field.
- Interpreter Prompt and Summary can both carry the permission label.

These are observed properties of internal/agent/approvals.go,
internal/room/approval.go and monolith/approval_views.go. Current Codex
construction keeps the duplicated reason consistent; the type does not enforce
that relationship. No parsing of formatted strings should be needed to fix it.

Keep existing policy operations, backend response semantics, manual FIFO,
participant/request identity, transcript ownership and UI dependency boundaries.
Do not introduce a renderer framework, new approval kinds, queues or lifecycle
state. Preserve full commands, cwd, file changes, permission scope, tool inputs,
plans, unknown input fields and supplied context. Do not read the filesystem or
execute commands to manufacture evidence.

## Recommended contract

Separate backend/operation data from presentation inputs on ApprovalRequest:

```go
type ApprovalRequest struct {
    Kind         ApprovalKind
    Details      ApprovalDetails
    Presentation ApprovalPresentation
    Options      []ApprovalOption
}

type ApprovalPresentation struct {
    Summary     string
    Explanation string
    Evidence    string
}
```

This is a replacement for Ask, Details.Display, Details.Explanation and
CommandOperation.Explanation, not an additional representation alongside them.
Details retains Backend, RequestID, Tool, Raw and Operation. CommandOperation
retains Command and Cwd. Existing file-change evidence remains on FileOperation;
moving that structure is outside this change.

The fields have these meanings across all kinds:

| Field | Meaning | Consumer |
| --- | --- | --- |
| Summary | Action label supplied where the common operation cannot determine it | Room presentation derivation; then panel and history heading |
| Explanation | Full backend-supplied reason, stored once as presentation context | Panel and history composition |
| Evidence | Additional request evidence not rendered from the common operation, excluding separately extracted explanatory context | History composition |

Evidence is a body fragment, not a complete standalone request document. It
contains no approval heading, outcome, participant header or history hint.
Empty Explanation means none supplied; do not invent one. Preserve values
without shortening, styling or terminal escaping at this boundary.

Room owns one pure derivation, for example DeriveApprovalPresentation(request),
returning the final Summary, Explanation and Evidence plus existing detached
Paths. This is a derived value, not a second mutable request or cached owner.
Room history and interpreter projection both use this derivation. The UI receives
the detached result and does not inspect operations, backend names or raw data.

For command and file kinds, producers leave Presentation.Summary empty. Room
derives command summaries with the existing shell summarizer and uses File
changes for files. Permission and unknown-kind producers supply Summary because
their operations do not define the requested human decision. This is an explicit
per-kind producer contract, not a priority contest between supplied and derived
labels. Tests reject nonempty command/file summaries in adapter fixtures.

Room alone applies missing-label fallbacks: shell command, File changes,
Permission request, or Approval request for an unknown kind. Empty labels never
hide a pending approval or its choices. Diagnostics may use the same generic
fallback vocabulary, but do not reconstruct legacy Ask strings. Failure diagnostics
also retain full command/cwd or affected file paths derived from Operation, plus
supplemental command/file evidence (including grant roots), because these failures
may never create a manual history record. No diagnostic prompt is stored.

## Composition and completeness

Room derives operation evidence with existing formatters, appends supplemental
Presentation.Evidence when present, and retains Explanation separately. The
complete history document is composed in this order:

1. Approval heading using the derived summary and existing command cwd placement.
2. Supplied explanation, when present, rendered once as an Explanation section.
3. Derived operation evidence and additional evidence, with explicit notices for
   missing required command/file/scope/tool evidence.
4. The existing separately owned decision field, rendered by the record renderer.

For command history, retain the complete cwd in the heading, or cwd: unknown;
do not repeat a working-directory line in the body. Explanation is no longer
rendered by commandApprovalEvidence. A complete inspection document is composed
from the same parts; no caller treats Evidence alone as the complete request.

Do not use presence of Evidence to decide whether to include Explanation.
Do not search, compare or remove strings to avoid repetition. Adapters separate
known explanatory fields before formatting evidence. Unknown fields and complete
opaque tool inputs remain intact; a coincidental repetition of reason text in
such data is not removed. Raw protocol envelopes remain internal and are never
a display fallback.

For example, a Codex request with reason Run the project tests becomes:

```text
Summary:     Additional permissions: network
Explanation: Run the project tests
Evidence:    Working directory: /project
             Requested permissions:
             { ...complete scope, including unknown fields... }
```

The panel shows summary and explanation. History composes both with Evidence
and the actual decision. There is no stored second copy of the reason inside
the formatted permission evidence.

## Adapter mapping

| Request | Summary source | Explanation source | Evidence source |
| --- | --- | --- | --- |
| Codex command | Room shell summarizer | Supplied reason | Command and cwd from Operation |
| Claude Bash | Room shell summarizer | Bash description | Command from Operation; cwd remains explicitly unknown |
| File change | Room fixed label | Supplied reason, if defined | Operation paths/changes plus adapter grantRoot context when supplied |
| Codex permissions | Adapter filesystem/network label | Supplied reason | Complete cwd and permission scope; omit extracted reason |
| Claude tool | Adapter exact tool label | Only a field with defined explanatory semantics | Full tool inputs, preserving unknown fields |
| Claude plan | Adapter Plan approval label | None unless separately supplied by protocol | Full plan and additional input fields |
| Unknown request | Adapter readable request label | Only defined supplied context | Available normalized action evidence; explicit unavailable notice otherwise |

File grantRoot is scope evidence, not an exhaustive target list or an explanation.
Move it out of the old formatted Ask using the decoded field. Do not parse Ask.
Retain malformed/missing tool input fallbacks. Plans are evidence, not invented
explanations derived from their first line. Existing summary algorithms remain
display-only and never replace authoritative operations.

## Interpreter and UI contract

Interpreter Approval exposes ID, Alias, Kind, Summary, Explanation, Evidence,
Paths and Options. Remove Prompt and rename the ambiguous Details string to
Evidence. These presentation fields are the detached room-derived result.
History remains delivered through canonical transcript records, not a second
UI-created approval record.

Commands and permissions use the shared compact layout: participant, summary,
optional explanation, history hint and offered choices. Keep the current file
panel's whole-path budgeting and omission count; retain file explanations in
history without expanding its panel in this refactor. Kind selects these existing
layout rules and hint wording; store one kind rather than independent booleans.
Rename commandRequestLines to describe the shared compact layout.

UI owns wrapping, escaping, styling, ellipsis, focus and selection. Reserve
choices and history hints before bounding summary/explanation. Only the panel
truncates. Retained history/export preserve full readable content using existing
escaping rules. Page Up/Down scroll history; inspection and composer restoration
retain their existing behavior. Active approval presence depends on an admitted
request and offered choices, not a backend-provided nonempty string.

## Authority, lifetime and concurrency

Adapters construct request data before admission; published presentation data
and slices are detached and treated as immutable. Policy reads Operation and
existing policy context, never Presentation. Session remains the only admission
and decision authority. No presentation function changes a request or decision.

Queued reviews have no retained record. On activation, room composes one record
and interpreter publishes the active panel. Session settlement updates that
record by ID and immutable alias before successor activation. Accepted choices
remain committed if cancellation follows; cancellation that wins first withdraws
the request. Backend exit/shutdown withdraw unsettled active requests through
existing contexts. Queued canceled requests never become visible. An accepted
choice does not claim that backend execution completed successfully.

Room preserves settled evidence. UI clears local selection/focus state when the
active request clears or changes; it never infers settlement from a keypress.
No new goroutine, lock, callback, cleanup owner or in-flight response rule is
introduced. Existing observers continue to run outside locks.

Session's approvalIdentity explicitly hashes backend identifiers, raw data and
operation fields, excluding Presentation. This diagnostic digest is not the FIFO
request ID. Tests establish that presentation-only changes leave it unchanged,
while backend identity/raw data and operation changes still change it. Backend
raw bytes remain intact even when they contain a supplied reason. No persisted
digest migration is needed.

## Alternatives and tradeoff

Keeping complete Display plus a separate reason is a smaller patch, but requires
every producer to synchronize two representations and history to trust that
synchronization. Keeping only complete Display requires parsing formatted text
for the panel. Neither meets the explicit ownership goal.

A fully structured union for permission scopes, tool inputs and plans could
derive every evidence string centrally, but adds several payload types and
formatters without a current need. Retain adapter-formatted additional evidence;
centralize composition, not every backend schema.

The recommended change touches both adapters and common projections, and changes
internal fixture APIs. That cost removes the old fields rather than maintaining
compatibility rules indefinitely. There is no external ApprovalRequest producer
API identified in the inspected code. Confirm with repository-wide references
before implementation.

## Implementation and acceptance

1. Update agent request types and both adapters together. Extract explanations
   into Presentation, keep command/file summaries derived, and preserve file
   grantRoot and all currently retained evidence. Replace diagnostic Ask use
   with a derived description from presentation/operation, without a stored prompt.
2. Add the single room derivation and explicit history composition. Reuse path
   formatting and command summary helpers. Remove reason formatting from command
   and permission evidence bodies. Make digest inputs explicit in Session.
3. Update interpreter DTO/projection and UI consumers, removing Prompt, legacy
   fallback concatenation and redundant kind booleans. Preserve existing layout,
   choice IDs and transcript delivery.
4. Update existing adapter, room, interpreter and UI fixtures/tests. Cover every
   mapping above, empty labels, absent evidence with present reason, present
   evidence with present reason, long/multiline context, unknown fields,
   malformed inputs, escaped controls, full cwd and transcript export. Assert
   extracted reasons appear once in composed history and survive panel resets.
5. Verify presentation changes cannot affect policy outcomes, offered choices,
   response payloads or diagnostic correlation identity. Retain mixed-kind FIFO,
   settlement-before-promotion, cancellation/shutdown, stale IDs, tiny-panel,
   paging, inspection and composer regressions. Run focused tests, make
   pre-commit and git diff --check; live backend calls are not required.
6. Update architecture.md, pkg-agent-approvals.md, pkg-ui-approval-display.md and
   the reading-experience/handoff docs to the implemented contract. Until then,
   those describe current behavior; this proposal is the explicit replacement
   candidate. Do not edit docs/for-humans without the required discussion.

Perform this as one internal cutover. No dual-read/dual-write compatibility
path: completion requires removing Ask, Display, the old explanation locations
and interpreter Prompt from production code and fixtures. Implementation was
authorized separately by the human; no runnable prototype is required.

## Validation

Focused agent, room, interpreter, Session and UI tests passed. Repository
pre-commit checks passed with zero lint issues and all race-enabled unit tests.
Coverage includes explanation/evidence composition, missing labels and evidence,
unknown tool fields, raw identity preservation, detached paths, mixed-kind
settlement, bounded panels, export escaping and composer restoration. No live
backend or keyboard checks were performed. Existing staged changes were left
under human ownership.
