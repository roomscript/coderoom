# Approval wording: UI-only proposal

Status: implemented, 2026-11-20. Generic approval wording confirmed.

## Scope and observed boundaries

The previous implementation was reverted in full. Restart with display wording
only. Do not change agent contracts, adapters, Session, interpreter DTOs, room
records, policy, offered choices or backend responses.

The active UI already receives option IDs and labels. Its `formatOption` in
`internal/ui/room/approval/view.go` maps the four known IDs to display strings.
History and transcript export share `renderApprovalTranscript` in
`internal/ui/room/history/record/render_approval.go`. That renderer receives an
existing `Decision` string. No additional retained state is necessary.

The active approval DTO has a broad permissions kind, not a plan-versus-turn-grant
semantic distinction. Retained records likewise contain no such structured
context. Do not infer semantics by parsing summaries, evidence or backend prose.

## Agreed wording

| Existing option ID | Active label |
| --- | --- |
| accept | Approve |
| acceptForSession | Approve for this session |
| decline | Decline |
| cancel | Decline and stop turn |

Use the existing label/ID fallback for unknown options.

At rendering time only, translate these exact existing decision values:

| Existing decision | Displayed decision |
| --- | --- |
| Approved by you · once | Approved by you |
| Approved by you · for session | Approved by you · for session |
| Declined by you | Declined by you |
| Canceled by you | Declined by you · turn stop requested |
| Waiting for you | Waiting for you |
| Withdrawn | Withdrawn |

Pass other values through unchanged. The generic approval label avoids claiming
an unsupported grant duration. Session wording describes the selected option;
it does not assert that the backend installed a rule. Stop wording describes a
request, not confirmed interruption.

The UI uses generic approval wording. Separate plan/turn wording would
require an additional design discussion about missing presentation inputs; it
must not silently expand this UI-only change.

## Implementation handoff after confirmation

1. Change the known-option strings in `formatOption`; preserve option selection,
   IDs, order, fallback and navigation.
2. Add a small pure decision-display helper in the history record package. Call
   it in `renderApprovalTranscript` before escaping and composing the decision
   line. Use exact whole-value matching, not substring replacement.
3. Keep canonical records untouched. Viewport and exported transcript use the
   same formatter. Do not rewrite records when processing snapshots or events.
4. Update focused approval and history renderer tests for the agreed wording.
   Cover unknown-value passthrough, waiting, withdrawal, selection IDs, stop text,
   viewport/export consistency and narrow panel bounds using existing fixtures.
5. Run focused UI tests, `make pre-commit` and `git diff --check`. Review the diff
   to ensure production changes are confined to these two UI packages. Leave
   staging and commits to the human.

## Ownership and tradeoff

Room remains the source of truth for the existing settled decision. Session
still arbitrates resolution versus withdrawal. The UI applies a pure display
translation on every render; it owns no additional state, queue, callbacks,
locks or cleanup. Admission, execution, failure, cancellation, shutdown and work
in flight are unaffected.

Exact decision-string translation relies on the current room contract. This is
acceptable for the bounded wording change; tests should exercise current room
values. Introducing a structured decision contract is separate architecture
work, not a prerequisite here. The alternative of changing canonical room text
would also affect non-UI consumers and is outside the requested UI boundary.
