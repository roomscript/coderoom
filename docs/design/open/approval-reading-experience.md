# Approval reading experience

Status: implemented file-change design, 2026-11-16.

Implementation: the compact box uses at most half the room height. It budgets
whole wrapped paths after reserving choices and the omitted count. If even the
heading cannot fit, it prioritizes the omission summary; at extremely small
sizes only the selected choice and overflow markers may fit. Full evidence and
identity remain available in history and transcript export.

Presentation polish: target lists and evidence section labels use project-relative
paths for targets inside the configured workspace root, and absolute paths for
outside targets. Both history and the compact box use the same formatter; request
targets and raw diff evidence retain their complete identity and original paths.
History omits the generic `approve file changes` description when it merely
repeats the heading, while retaining backend explanations, permission scope and
unavailable-evidence notices. Diff additions, deletions, headers and hunks reuse
the existing file-change colors. Approval headings and decisions retain approval
styling. All evidence is escaped before styling and wrapping, and plain-text
transcript export retains complete readable evidence without terminal controls.

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

## Agreed next increment: command approvals

Status: implemented, 2026-11-17.

Implementation: manual commands now use the existing retained approval record
and authoritative Session settlement events, including withdrawal. The compact
box shows a parser-derived action and `Full command in history`; Page Up/Down
scrolls history with approval focus. Full original commands, authoritative cwd
(or explicit unknown cwd), and unshortened supplied explanations remain in
history and transcript export. Display parsing never changes policy inputs.
Long command summaries are bounded with an ellipsis after reserving wrapped
rows for the history hint and existing choices; tiny panels prioritize the hint
over the summary when both cannot fit.
Focused tests cover summaries, evidence, settlement, mixed file/command
projection, withdrawal, escaping, inspection, composer restoration and small
terminals. Permission/tool presentation remains separate scope.

Before this increment, the command box combined the adapter's shortened `Ask` with complete
command evidence. Codex shortens command text to 160 characters and cwd to 80;
the full command and authoritative cwd remain in `CommandOperation`. Only file
requests acquired retained approval room records. Commands now follow that pattern.

Extend the existing manual file pattern to commands: retain the
complete command and cwd in one room record, settle it in place, and use a
bounded decision box with participant, action summary and existing choices.
Show `Full command in history` in the box. Preserve the complete literal command
in history; do not infer a friendly description from shell syntax. Show
unknown cwd explicitly rather than substituting the workspace root. Backend
explanations should be preserved when supplied, without presenting them as a
policy verdict or inventing a reason for manual review.

Derive the action summary from parsed shell syntax:

- A single simple command shows the binary without arguments, using its basename
  for display. Keep its full executable path in the evidence.
- Special-case only `git` and `make` initially: show `git <subcommand>` or
  `make <target>`. Account for recognized options and their operands before that
  argument, such as `git -C repo diff` and `make -C repo test`. Multiple make
  targets fall back to `make`; ambiguous option parsing falls back to
  `shell command`.
- Unwrap recognized shell invocations such as `/bin/zsh -lc 'git diff --check'`
  only when the command argument is literal and parses successfully. Unrecognized
  invocation forms fall back to `shell command`.
- Pipes, command lists (`;`, `&&`, `||`), redirects, substitutions, background
  execution and other complex or unsupported syntax show `shell command`.
  Do not show a shortened executable label that conceals nested execution.
- Parse errors or missing command evidence show `shell command`, with an
  explicit unavailable-evidence notice in history when needed.

Examples: `rg -n approval internal/` becomes `rg`; `git diff --check` becomes
`git diff`; `make test` becomes `make test`; `/bin/zsh -lc 'git diff --check'`
becomes `git diff`; `git diff | head`, `cd repo && make test`, and
`echo "$(deploy)"` become `shell command`.

Reuse the existing mvdan shell parser for syntax recognition, keeping display
summarization separate from policy decisions. Never execute a command to derive
its label, and never replace the original operation with the unwrapped command.
The label is a summary, not a description of argument-dependent effects:
`git push --dry-run` and `git push origin main --force` both show `git push`.
Escape evidence and labels before terminal styling and wrapping.

Page Up/Down scrolls history while the command box is focused, matching file
approvals. Preserve Ctrl+O, history Ctrl+G, option navigation and staged-composer
restoration. Bound the box as for file approvals and reserve choice rows when
terminal space is limited.

Keeping full evidence in the scrollable box was considered; the agreed compact
summary avoids repeated evidence and gives commands the retained history and
outcome already available for files.

Implementation would extend `room.handleApprovalEventLocked`, interpreter
approval projection, and the approval component's layout/input routing. Reuse
the existing Session FIFO, request IDs and settlement events: Session remains
the sole decision authority, including cancellation and shutdown withdrawal;
room only projects activation and settlement. No new queue, lifecycle state or
automatic-approval history is required. Keep callbacks outside locks.

Acceptance checks: table-driven summary examples, recognized shell wrappers,
options before git subcommands/make targets, multiple make targets, malformed
syntax and nested execution; complete long/multiline commands in history and
export; visible history hint and unknown cwd; escaped terminal controls; mixed
file/command FIFO with settlement before successor activation; withdrawal and
stale decisions; bounded small-terminal layout; preserved composer and existing
inspection keys. Run focused room/interpreter/UI tests and `git diff --check`.

After commands, discuss permission/tool requests separately: show the requested
scope and supplied explanation clearly, then consider choice wording that
accurately communicates each backend's actual session and cancellation scope.

## Agreed command presentation refinement, 2026-11-18

Status: implemented, 2026-11-18. This section supersedes the command
box content and history cwd placement described above; other contracts remain.

Live testing in `screenshot-1.png` confirmed a manual command approval with
complete evidence and a correctly summarized `shell command` box. The user
found that summary insufficient for deciding: the supplied explanation belongs
in the compact box as well. The screenshot establishes pending presentation,
not paging, transcript export or final settlement behavior. The screenshot is
a local reference, not a required implementation artifact.

- Compact box order: participant, command summary, supplied explanation when
  present, `Full command in history`, existing choices. Preserve the explanation
  verbatim except for terminal escaping, wrapping and explicit visual truncation.
  It is backend-supplied context, not a policy verdict. Do not invent a fallback
  explanation or substitute the adapter's shortened Ask.
- Reserve the history hint and choices before allocating summary/explanation
  rows. Bound long explanations with an ellipsis and retain the existing long
  summary regression guarantee. Use remaining rows for the participant and
  bounded summary first, then explanation. At tiny sizes retain the existing
  hint/choice priority; neither content field may crowd those out.
- History starts with `⚡ Approval: <summary> (cwd: <path>)`, naturally wrapped.
  Use the complete authoritative cwd, or `(cwd: unknown)`; do not substitute
  the workspace root or shorten the path. Remove the separate working-directory
  line from the retained command record. Full original command and full supplied
  explanation remain in that record and its transcript export.
- Pass the explanation as structured detached presentation data derived from
  CommandOperation.Explanation. Do not parse Details or history text to recover
  it. Policy inputs, parser summaries, Session FIFO, settlement and withdrawal
  remain unchanged. No new decision owner, queue or lifecycle state is needed.

Implementation and verification instructions: see
`../approval-command-refinement-handoff.md`.

Validation: focused approval, room, interpreter and UI tests, plus repository
pre-commit checks (lint and race-enabled unit tests). Regression coverage includes
structured explanation projection/reset, multiline and escaped content, bounded
long summaries/explanations and tiny panels, complete cwd headings without
duplication, and full command/explanation/cwd evidence in transcript export.
Existing settlement, FIFO, paging, inspection and composer-restoration tests
remain passing. No new live manual prompt or keyboard verification was performed.

