# Next session: approval presentation and identification

Handoff date: 2026-11-13. The user accepts the approval implementation as a
first pass and will start a new session using the latest coderoom. Next scope:
make approvals recognizable on screen and identifiable in retained logs.
This document preserves requirements and proposals; it is not a completed
presentation design or authorization to implement production changes as architect.

## Read first

- AGENTS.md and docs/code-review.md.
- docs/design/concept.md and architecture.md.
- docs/design/approval-policy-test-cases.md: current implemented policy and tests.
- docs/design/pkg-ui-approval-display.md: terminal escaping requirements.
- Current Session, interpreter transcript and UI approval flow before designing.

Historical restart/review notes describe intermediate states. The current
policy design and source supersede claims that roles gate approval or Ubuntu
Gitleaks is incompatible.

## User intent

Reduce approval exhaustion while keeping decisions visible and inspectable.
The user wants an on-screen record detailing the approval, its content and the
decision, with a dedicated color so these records are easy to identify. They
also asked whether logs can distinguish automatic approvals from human ones.
Policy first, presentation second was the agreed sequence; the policy first
pass is now accepted.

Proposals discussed, not yet finalized: one persistent record per request,
updated from pending to resolved for manual decisions; compact expandable
automatic records; participant, action, decision origin and reason visible;
one approval accent with text/symbols for outcomes, retaining participant
identity colors. Full inspectable content must be redacted where sensitive.
Do not invent an expansion interaction before reading current UI conventions.

## Current mechanics and gaps

Session enables internal/approval, which returns explicit Approve/Decline/Review.
Session owns manual FIFO queue, cancellation and backend-choice translation.
Adapters retain normalized typed operations and raw protocol input.
Session currently emits textual AgentLog records:

    approval policy v2 request=<digest> outcome=approve reason=<code> scanner=... config=...
    approval policy v2 request=<digest> origin=human decision="accept"

Room projects AgentLog to ordinary transcript log records. Manual approval DTOs
include shortened prompt and raw backend details, currently rendered in the
terminal-escaped scrollable panel. They are not a structured persistent
approval history model. Do not parse these log strings to reconstruct state.

The --agent-log file records backend protocol traffic only. Both automatic and
human approval produce the same backend response, so that file alone cannot
identify decision origin. Policy AgentLog records are not written to it by
current wiring. Design an explicit persisted decision/audit path if later
analysis is required; decide whether a separate structured log or a compatible
extension is smallest. Do not assume the conceptual events.jsonl is implemented.

The existing SHA-256 request digest is a correlation aid, not necessarily a
unique occurrence ID for repeated identical actions. Trace identity needs
across automatic and queued/manual paths before choosing a record key.

## Architecture constraints for the design

- Session publishes structured lifecycle/decision facts, not display markup.
- Interpreter owns canonical transcript records; UI owns rendering/color.
- Reuse the existing manual queue. Add no second decision owner or queue.
- Separate decision origin (policy/human) from outcome (approved/declined/etc.).
  Review is a pending need for human input, not a final decline.
- Preserve reason codes, optional scanner metadata and policy identity.
- Define creation, update, completion, withdrawal/cancellation, backend exit
  and shutdown behavior. Approval is not evidence of execution success.
- Account for decisions already returned while cancellation is in flight;
  do not show canceled evaluation as a completed automatic approval.
- Observers/callbacks remain outside locks and causal ordering is preserved.
- Reuse one structured event source for transcript and persistence where it
  fits. Avoid independent UI/log interpretations or duplicated state.
- Sensitive URLs/headers/content must not leak into persistent audit records.
  Preserve terminal escaping on all inspectable display content.
- Do not edit docs/for-humans without discussion, stage or commit without an
  explicit instruction, or implement production changes while in architect role.

## First-pass behavior to preserve

- Roles do not affect approval. The former builder-only condition was an
  unnecessary restriction introduced by Ada's guidance and has been removed.
- Ordinary contained structured file operations qualify; protected AGENTS.md/
  Git metadata, ignored writes, outside targets and ambiguous links ask.
- Shell parsing supports a bounded literal subset and compound commands. Claude
  Bash currently lacks cwd evidence, and login-shell startup remains unsupported.
- GET: missing Gitleaks asks; clean scan approves; findings/validation errors
  decline. Ubuntu 8.16 detect --pipe and modern stdin are supported by capability
  discovery. No package replacement or strict version formatting is required.
- Raw/decoded gitleaks:allow markers decline, even harmless occurrences.
- Claude WebFetch cannot expose redirects, so installed Gitleaks yields
  incomplete-validation decline for that tool. Supported nonredirecting literal
  curl can scan. The scanner does not send candidate HTTP requests.
- Historical replay: 519 requests, 162 approvals, 357 reviews, zero declines.
  All approvals are structured project writes. This does not predict future
  sessions or prove historical filesystem state.

## Next session starting point

Trace the current event/transcript/UI pipeline, then propose the smallest
structured record and audit design with example renders. Resolve content
redaction, compact/full inspection, occurrence identity and persistence before
asking a builder to implement. Keep policy-coverage expansion separate unless
the user changes scope. Verify the running binary/version and actual new log
path before interpreting new-session observations. Never read large logs whole;
extract relevant events incrementally.
