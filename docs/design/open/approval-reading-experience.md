# Approval reading experience

Status: implemented file-change design, 2026-11-16.

Implementation: the compact box uses at most half the room height. It budgets
whole wrapped paths after reserving choices and the omitted count. If even the
heading cannot fit, it prioritizes the omission summary; at extremely small
sizes only the selected choice and overflow markers may fit. Full evidence and
identity remain available in history and transcript export.

## Agreed file-change behavior

The first increment covers manually reviewed file changes. Other approval types
keep their existing presentation; automatic approval history and audit files are
separate scope.

- When a file request becomes the active manual queue head, append one
  participant-associated approval room record with heading, affected files and
  full proposed changes, using the current approval style and escaping contract.
- The approval box shows the participant, file-change action, affected files and
  existing choices. It does not repeat the diff.
- Ctrl+O toggles history/approval focus. In history, Ctrl+G opens the existing
  full transcript snapshot in the editor, including the complete approval
  evidence. This is inspection, not an approval submission or live editor view.
- Confirmed Page Up/Down behavior: with the compact file approval box
  focused, scroll room history so the evidence can be read without switching
  focus. In history focus retain normal history paging. This changes today's
  approval-input routing, which pages the approval body instead.
- Agreed file-list overflow behavior: show the first 10 files,
  one per line, followed by `and N more files — see history` when needed. The
  user selected this simpler alternative over packing additional files into
  comma-separated rows. The complete list and evidence remain in history.
  Ten is a maximum file count, not a promise of 10 physical rows: long names wrap,
  and terminal height can require fewer entries. Reserve choices and an explicit
  omitted-file count; do not silently truncate paths or make the box fill the
  whole screen. Exact available-height budgeting remains implementation design.
- After settlement, update the same record, preserving heading/files/diff and
  appending `Decision: Approved by you · once`, `Approved by you · for session`,
  `Declined by you`, or `Canceled by you`, according to the actual choice.
  External cancellation is `Withdrawn`, not a human decline.
- Reuse the existing Session-wide manual FIFO across approval types. Waiting
  requests stay undisplayed. Settle the visible record before publishing the
  next request's record and box. Already settled records remain in history.

The current FIFO already publishes only its head. It does not publish a complete
settlement event before every promotion, so retained decisions require a small
structured lifecycle extension. Session owns acceptance/cancellation arbitration;
the interpreter projects facts into the canonical room record and active box.
The UI must not mark a decision final on keypress or infer it from box dismissal.
Use the existing request ID and immutable participant alias for association;
no new global occurrence scheme is needed for this manual-only increment.

Cancellation and resolution must compete at one Session-owned settlement boundary.
An accepted decision remains committed even if the request context is canceled
afterward; cancellation that wins first withdraws it. Publish the terminal fact
before promotion and outside the hub lock. Current listener post-response context
checks can discard an accepted choice and must be reconciled with that contract.
Backend exit/shutdown withdraw displayed unsettled requests via their request
contexts; canceled queued requests never acquire visible records. Confirm that
exit/shutdown actually cancel these contexts before declaring cleanup complete.

Verification must cover mixed-type FIFO, terminal-update-before-successor order,
stale IDs, concurrent cancellation/resolution, backend exit/shutdown, preserved
staged composer, Ctrl+O/Ctrl+G during pending approval, full evidence in export,
history paging and large file lists. No new decision queue or audit sink.

## Implementation sequence

1. Extend Session manual approval events with accepted settlement facts keyed
   by the existing ID and alias. Resolve cancellation races at the hub boundary,
   retain ordered publication outside locks and settle before FIFO promotion.
2. Add a file-approval room record and detached interpreter/UI representation.
   Append only on activation, update the same record on settlement, and include
   complete evidence in transcript export. Keep these records out of agent
   messages, handoff output and agent context. Retain an explicit unavailable
   evidence notice when proposed changes are missing.
3. Project affected paths as structured data from existing file operations,
   rather than parsing formatted prompts/diffs. Render the compact file box and
   full room evidence from the same request. Keep other types unchanged.
4. Route file-approval Page Up/Down to history. Preserve Ctrl+O, history Ctrl+G,
   option navigation, original choice IDs and staged-composer restoration.
5. Add focused Session race, room/interpreter projection and UI interaction
   coverage for the scenarios above. Run relevant tests, repository lint/unit
   checks and `git diff --check`.

Before adding layout fields or lifecycle state, check whether the existing
request, active head and canonical record already supply them. Ten filenames
is an upper bound; derive the visible count from wrapped height while reserving
choices and the omitted count. Test very long names and extremely small panels.

## Scope relative to earlier proposals

This design supersedes the full-evidence-in-box requirement for manual file
approvals in `../approval-presentation.md` and
`../pkg-ui-approval-display.md`. Their terminal escaping requirements still apply.
Issue #81 combines related room history work with broader automatic-approval
lifecycle and optional disk audit work. Only the manual file-change record and
settlement support needed here belong in this increment. No GitHub issue has
been changed as part of this design.

The screenshots in the project root motivated this discussion; they are local
examples and are not required implementation artifacts. Shell commands,
permission requests and other tools need separate presentation discussions.

