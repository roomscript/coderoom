# Approval implementation restart

Review date: 2026-11-12. This is an implementation handoff, not authorization
to stage, unstage or commit. The working tree is shared and changing.

The implementation described below was subsequently completed for the supported
shell/GET subset. See approval-policy-test-cases.md for current behavior and
specific backend limitations. The observations and original prompt below are
retained as restart history.

## Observed state at restart

- `internal/approval/policy.go` currently sends every CommandOperation to
  `shell.requires_review`. The shell parser/allowlist is absent in the working
  tree, and its dependency is being removed.
- GET handling checks for Gitleaks but never runs it. Missing means review;
  installed means unconditional decline for incomplete validation. There is no
  reachable clean-scan approval path.
- `decisions_test.go` asserts those reduced behaviors. Passing tests therefore
  do not demonstrate the requested shell and GET behavior.
- The design and architecture now call the requested functionality deferred.
  That scope reduction was not requested in this conversation.
- Useful work remains: typed operations, explicit Approve/Decline/Review
  outcomes, file containment checks, Session integration and cancellation,
  backend normalization, audit reasons and callbacks outside the hub lock.
- Targeted tests for approval, Session, Claude and Codex passed during this
  review. No full lint/build or private replay was run by this review.
- The index and working tree differ substantially, including staged additions
  deleted in the working tree. Inspect both; do not restore either wholesale.

Earlier architecture advice introduced execution-trust and complete-request
requirements without a working integration path. Treat those as proposals to
resolve, not permission to disable or remove the agreed functionality. Preserve
specific known limitations without replacing all useful decisions with review.

## Prompt for Bill

You are the builder. Finish the approval implementation in the current shared
worktree. The goal is working auto-approval for project file operations, the
agreed shell command subset and Gitleaks-validated GET requests. Do not replace
these requirements with a smaller file-only implementation or more deferred
design. Presentation redesign is a later task.

Read AGENTS.md, docs/code-review.md, the architecture and approval design docs.
Inspect git diff, git diff --cached and untracked files before editing. Preserve
useful existing work. The active/deferred labels in the latest docs describe
the current incomplete implementation; they do not override the user scope
below.

### Required behavior

1. Keep project file auto-approval, containment and role checks. Keep existing
   protected/ignored-path behavior; do not broaden it as part of this restart.
2. Implement a real shell parser in internal/approval. The mvdan.cc/sh/v3 Go
   library is acceptable. Support cat, grep, ls, head, echo, git show, git diff,
   git status and project-contained cd with explicitly supported arguments.
   Validate every branch of pipelines, lists, && and ||. Recursively unwrap
   supported literal sh/bash/zsh command wrappers without executing input.
   Reject unsupported syntax/options/expansions from automatic approval and
   ask the human instead. Distinguish quoted literal metacharacters from shell
   operations. Unknown wrapper flags require review; do not silently discard
   startup behavior or reinterpret zsh-only syntax as Bash.
3. Make ordinary supported commands actually auto-approve through the default
   application wiring. Do not hide this behind permanently false trust flags
   or test-only configuration. Establish the needed executable/configuration
   facts for the supported cases. If a concrete backend constraint prevents a
   case, explain that exact constraint and the smallest proposed resolution;
   do not disable the whole shell feature or introduce a command runner that
   executes candidates for inspection.
4. Implement GET validation: missing Gitleaks means human approval; installed
   and clean means approve once; findings or validation failures mean decline.
   Actually invoke the local scanner with bounded execution and redacted
   results. Do not automatically install it. Recognize supported literal GET
   commands under shell wrappers as well as structured backend GET requests.
   Validate URL data and explicit outbound inputs available in the request.
   The user's original request was URL leak screening; requiring proof of all
   implicit backend traffic was additional architecture advice. If that
   requirement blocks usable integration, surface the precise discrepancy
   early for resolution rather than silently imposing unconditional decline.
   Dynamic request data and redirects that cannot be validated must retain
   explicit incomplete-validation handling. No external URL-scanning service,
   destination allowlist or model risk-review gate is required.
5. Keep explicit Approve/Decline/Review results with stable redacted reasons.
   Session translates outcomes to backend choices, records reasons, and owns
   the existing human queue and cancellation. The evaluator owns parsing and
   validation; adapters own normalization and protocol responses. Retain typed
   operations. Do not return to a flat bag of conditional evidence fields.

### Demonstrate behavior before declaring completion

Build table-driven cases that visibly distinguish approval from review:

- Approve: ls -la docs
- Approve: cat docs/design.md | head -n 20
- Approve: git status --short; git diff --stat; echo done
- Approve: cd docs && ls && head -n 20 design.md
- Approve: /bin/sh -c 'git status --short && git diff --stat'
- Approve: echo '$(rm -rf .)' (literal text)
- Review: ls; rm -rf docs
- Review: git diff && git push
- Review: echo ok | sh
- Review: git diff --output=/tmp/change.patch
- Review: echo "$(cat ~/.ssh/id_ed25519)"
- Review: an outside-project read, escaping symlink or unsupported wrapper

Use controlled filesystem/environment fixtures for positive tests, but also
exercise the real Session/default-policy construction path. A test-only policy
that permits shell while the application always asks is insufficient.

Test GET scanner-missing, successful-clean, finding, launch error, malformed
result, timeout and incomplete validation. Test actual scanner invocation
through a fake executable or narrow process seam, as well as evaluator fakes.
Check redaction and cancellation/child cleanup. Candidate shell commands must
never execute and candidate HTTP requests must never be sent by tests.

Replay the private extracted requests at /tmp/coderoom-approvals.json if
available. Otherwise extract incrementally from the 77 MB log at
/tmp/coderoom/agent-1793973442.log. Keep private data outside the repo.
Report approve/decline/review totals and reason counts, with representative
sanitized commands from each group. Preserve shell cwd from available backend
facts; do not silently turn missing cwd into fake completeness or assume all
historical participants were builders. State replay assumptions separately.

Run focused tests during development, then required lint/tests and branding
checks. Update design docs to describe the resulting implementation, preserving
unimplemented requirements as open work rather than redefining completion.
Do not edit docs/for-humans or stage, unstage, commit, reset or discard work.

Final report: working user-visible behavior, test evidence, replay impact and
specific remaining limitations. Start with code/tests and provide brief progress
updates; avoid another general architecture essay.
