# File approval history preview

Status: implemented, 2026-11-19. See [Bill's handoff](../file-approval-history-handoff.md).

The viewport now uses detached room-owned file evidence with eight shared
change-text lines and ten file rows. Full transcript rendering explicitly uses
canonical text and the actual decision. Unified hunk lengths are validated;
Claude replacement counts are labeled snippet counts and Write counts describe
supplied content. Unsupported evidence has unavailable counts. Supplemental
scope evidence stays in the transcript with a visible inspection hint.
Codex explicit additions use supplied plain content for added-line counts,
including empty content; other Codex evidence requires validated unified hunks.
Unified metadata must precede the hunks, with paired file headers when present.
Trailing metadata or incomplete sections make counts unavailable.

## Problem and agreed direction

Complete file approval evidence currently dominates visible history. Show a
summary, affected files with their supplied change type and available added/deleted
line counts, and the first lines of changes. Preserve complete evidence for the
standard history editor interaction. This changes viewport presentation, not
approval authority or retained evidence.

## Implementation before this change

- `internal/room/approval.go` composes complete approval evidence into
  `Record.Text`; settlement updates `Decision` by request ID and alias.
- `internal/ui/room/history/record/render_approval.go` renders all that text.
  Transcript rendering currently falls through to the same renderer.
- Ordinary file-change records already have separate viewport and transcript
  renderers in `render_filechange.go`, with an eight-line body preview.
- Ctrl+O toggles compose/history focus. History Ctrl+G calls
  `openEditorWithTranscript`, obtaining `history.PlainText()` in transcript mode
  and opening a read-only temporary transcript in the configured editor.
  Ctrl+O does not expand an individual record. Preserve these bindings.
- Claude Write evidence is replacement content labeled `write`, not proof of
  creation. Edit evidence describes one replacement or all matching occurrences;
  it does not establish actual occurrence counts. Missing evidence is possible.

## Recommended design

Keep one canonical approval record with complete text and the existing decision.
Add optional detached file presentation data to that record, projected through
the existing interpreter boundary: display path, supplied change type and supplied
change text. Its concrete consumer is the viewport renderer, which otherwise
would have to parse the composed document to recover file boundaries. Do not
retain the request, raw envelopes or policy operations in the UI. Do not store
another mutable preview string, cached counts or decision.

Room derives both full text and file presentation data from the same admitted
request at activation. Keep summary and supplied explanation visible, then file
rows and bounded change previews, then the actual decision. Reuse existing
escaping, diff styling and preview helpers where their contracts fit. Full
transcript rendering continues to use complete canonical text and decision.
Command and permission approvals keep their current presentation.

Initial budget: eight change-text lines across the whole request,
following the existing file-change preview convention, and at most ten whole
file paths. Distribute preview lines in request order, showing the first lines
without reconstructing file contents. Explicitly report omitted files and
change-text lines and show `Ctrl+O history, Ctrl+G open transcript` when content
is omitted. Bound preview line width using existing column truncation. Keep
decision and hint outside the preview budget. Use these as implementation
defaults; they are display constants, not new configuration options.

Count additions/removals only where supplied evidence supports the claim. For
recognized unified diffs, count hunk addition/deletion lines, excluding metadata
headers; reject incomplete or unrecognized formats as unavailable. Claude Edit
counts describe the supplied replacement snippet, explicitly labeled as such;
replace-all does not establish total file counts. Claude Write remains `write`
with a supplied-content line count, without invented deletion counts or a `new`
label. Missing/opaque evidence gets an explicit unavailable notice. Never read
the filesystem or execute commands to fill these gaps. Preserve exact supplied
change types unless their normalization is unambiguous.

## Ownership and implementation plan

1. Extend the room record with the minimal detached file presentation data and
   derive it at approval activation. Inspect snapshot/projection/copy paths and
   detach slices there; retain full text. No additional lifecycle owner.
2. Separate approval viewport and transcript rendering. The viewport consumes
   structured file data; transcript uses the complete existing document. Avoid
   parsing formatted `Text` or introducing a generic renderer framework.
3. Add pure helpers for supported evidence counts and bounded previews. Reuse
   existing file-change display helpers without changing ordinary file records.
4. Update approval renderer tests: large/multiple files, whole-path omission,
   narrow widths, new/delete/update diffs, replacement content, replace-all,
   absent/opaque evidence and escaped controls. Verify full trailing evidence
   remains in transcript mode while viewport stays bounded.
5. Retain editor/focus/composer regressions and settlement tests: one record,
   waiting to accepted/declined/withdrawn decision, unchanged after panel clears.
   Run focused room/interpreter/UI tests, pre-commit and `git diff --check`.
6. Update the presentation contract and reading-experience docs when implemented;
   do not edit `docs/for-humans/` without discussion.

Session remains the sole admission/decision authority. Queued requests acquire
no record until activation. Settlement changes only the existing decision;
cancellation, failure and shutdown preserve existing withdrawal rules. Preview
data is immutable for the record lifetime. No new lock, observer, callback,
goroutine, cleanup owner or backend response behavior is needed.

Truncating canonical text would lose editor evidence. Parsing that text would
couple UI behavior to composition punctuation. A detached structured file view
is the smallest additional representation needed by this concrete consumer.
The main risk is misleading counts for backend evidence that is not an actual
file diff; explicit evidence-specific labels and unavailable counts bound it.
