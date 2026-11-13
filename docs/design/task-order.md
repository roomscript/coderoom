# Task order

Agreed direction, 24 October 2026: close bounded work before returning to the
interpreter refactor. This records sequencing, not approval of new runtime designs.

## 1. Finish participant details after `/model`

**Done.** #25 and #24 are closed.

Complete [#25](https://github.com/roomscript/coderoom/issues/25) before
[#24](https://github.com/roomscript/coderoom/issues/24): `/who` consumes the
startup resume identity supplied by invitation work.

- #25: invitation feedback includes role, backend, model, effort and full backend
  resume ID. Adapters supply startup metadata; Session owns the snapshot. Cover
  role and settings fallbacks, startup IDs and Claude launch arguments. Verify
  that Claude's first `system/init` echoes the supplied session ID in integration.
- #24: publish a detached participant snapshot as a structured transcript record.
  Render state, alias, role, backend, model, effort and full resume ID in alias
  order. Cover empty rooms, fallbacks, colors, narrow widths, resize and historical
  snapshot isolation. Rendering reads captured data, never live Session state.

Use existing settings labels and lifecycle moments. Update the relevant design
and manual pages with each issue. Backend-specific prompt guidance (#72) remains
separate from this presentation work.

## 2. Complete Claude integration

**Done** on 11 November 2026. Evidence and remaining risks are in the status
of `pkg-agent-claude.md`; the temporary plan is retired.

Reconcile `internal/agent/claude/PLAN.md` with current code first. The adapter is
registered and has scripted-peer unit tests and live integration tests; it has
no recorded transcript replay suite yet. README support is already present.

The remaining testing work in that plan starts with backend-neutral capability
requirements, then transcript recording and replay for both backends
(`agent-transcripts.md`), reusing existing Codex infrastructure where it fits. Define
scenario coverage and the smallest shared boundary before implementing it;
sharing infrastructure does not require rewriting unrelated Codex tests.

Completion includes recorded Claude regression coverage and the plan's mixed
Codex/Claude demo with Claude as builder and, separately, reviewer. Live recording
and demo runs use quota and must follow the explicit-run agreement in the Claude
plan. Record verification evidence, reconcile completed checklist items, and
retire the temporary plan when its completion criteria are met.

## 3. Return to the interpreter

Resume #61 only after the first two groups are complete. Begin with a fresh
ownership design for completion and ordered publication, using
`open/interpreter-architecture.md` as historical review feedback rather than an
approved target. Set structural acceptance criteria before implementation; retain
one execution lane, Session authority, causal ordering and shutdown draining.

Reconcile the root `PLAN.md` at that point: its package-removal next step predates
the paused architecture review. Package relocation alone does not close #61.

Read first: `AGENTS.md`, `concept.md`, `architecture.md`,
`docs/code-review.md`, `open/interpreter-architecture.md` and the root
`PLAN.md`.
